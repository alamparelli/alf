package controlcenter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// AppActionHandler proxies cross-app action calls. An app calls
// POST /api/app-action with {target, action, params} and the CC
// validates the action against the target's manifest before forwarding.
type AppActionHandler struct {
	Store AppStore
}

type appActionRequest struct {
	Target string          `json:"target"`
	Action string          `json:"action"`
	Params json.RawMessage `json:"params"`
}

type manifestWithActions struct {
	Actions map[string]struct {
		Params      []string `json:"params"`
		Description string   `json:"description"`
	} `json:"actions"`
}

func (h *AppActionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Parse request body.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySmall)
	var req appActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	// Validate target slug.
	if !validName.MatchString(req.Target) {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid target app"})
		return
	}

	// SEC: Validate action name to prevent path traversal.
	if !validName.MatchString(req.Action) {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid action name"})
		return
	}

	// Identify caller from Referer (set by browser for iframe requests).
	caller := extractAppSlugFromReferer(r)
	if caller == "" {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "caller app not identified"})
		return
	}

	// Read and validate target manifest.
	manifestData, err := h.Store.ReadFile(req.Target, "manifest.json")
	if err != nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "target app not found"})
		return
	}

	var manifest manifestWithActions
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid target manifest"})
		return
	}

	if _, ok := manifest.Actions[req.Action]; !ok {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "action not declared in target manifest"})
		return
	}

	// Resolve target port.
	portData, err := h.Store.ReadFile(req.Target, "data/port")
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]string{"error": "target app has no running server"})
		return
	}

	port, err := strconv.Atoi(strings.TrimSpace(string(portData)))
	if err != nil || port < 1024 || port > 65535 {
		respondJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid target app port"})
		return
	}

	log.Printf("[app-action] %s → %s/%s", caller, req.Target, req.Action)

	// Build params body (default to empty object).
	params := req.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}

	// Proxy to target app.
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	actionPath := "/api/actions/" + req.Action

	proxy := &httputil.ReverseProxy{
		Director: func(proxyReq *http.Request) {
			proxyReq.URL.Scheme = target.Scheme
			proxyReq.URL.Host = target.Host
			proxyReq.URL.Path = actionPath
			proxyReq.URL.RawQuery = ""
			proxyReq.Host = target.Host
			proxyReq.Method = http.MethodPost
			proxyReq.Body = io.NopCloser(bytes.NewReader(params))
			proxyReq.ContentLength = int64(len(params))
			proxyReq.Header.Set("Content-Type", "application/json")
			proxyReq.Header.Set("X-Caller-App", caller)
			// SEC-004: Strip sensitive headers.
			proxyReq.Header.Del("Cookie")
			proxyReq.Header.Del("Authorization")
			proxyReq.Header.Del("X-Tools-Socket")
			proxyReq.Header.Del("X-Requested-With")
			// SEC-007: Strip forwarded headers.
			proxyReq.Header.Del("X-Forwarded-Host")
			proxyReq.Header.Del("X-Forwarded-For")
			proxyReq.Header.Del("X-Forwarded-Proto")
			proxyReq.Header.Del("X-Real-Ip")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			respondJSON(w, http.StatusBadGateway, map[string]string{"error": "target app server unreachable"})
		},
	}

	proxy.ServeHTTP(w, r)
}
