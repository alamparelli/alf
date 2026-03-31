package controlcenter

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"nhooyr.io/websocket"

	"github.com/alamparelli/alf/internal/vault"
)

// SSHHandler proxies SSH requests from the UI/API to vault-proxy.
// WebSocket sessions are bridged bidirectionally; HTTP endpoints are proxied directly.
// Registered outside the main middleware stack to preserve http.Hijacker for WebSocket.
type SSHHandler struct {
	Manager       *vault.Manager
	AuthToken     string
	Sessions      *SessionStore
	AllowedOrigin string
}

func (h *SSHHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Manager == nil {
		http.Error(w, `{"error":"vault not available"}`, http.StatusServiceUnavailable)
		return
	}

	// Inline auth check (outside middleware stack).
	if !h.checkSSHAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Ensure vault is authenticated.
	if err := h.Manager.EnsureAuth(); err != nil {
		log.Printf("[ssh] vault auth failed: %v", err)
		http.Error(w, `{"error":"vault authentication failed"}`, http.StatusServiceUnavailable)
		return
	}

	// Parse: /api/ssh/{service}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/api/ssh/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, `{"error":"expected /api/ssh/{service}/{action}"}`, http.StatusBadRequest)
		return
	}
	service, action := parts[0], parts[1]

	// SEC: Reject path traversal in service name.
	if strings.Contains(service, "..") || strings.Contains(service, "/") {
		http.Error(w, `{"error":"invalid service name"}`, http.StatusBadRequest)
		return
	}

	// SEC: CSRF protection for non-WebSocket HTTP actions authenticated via session cookie.
	// Bearer token requests are not vulnerable to CSRF (token must be in header).
	if action != "session" {
		hasBearerToken := strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !hasBearerToken && r.Header.Get("X-Requested-With") == "" {
			http.Error(w, `{"error":"missing X-Requested-With header"}`, http.StatusForbidden)
			return
		}
	}

	switch action {
	case "session":
		h.handleSSHSession(w, r, service)
	case "exec", "upload", "download":
		h.proxySSHHTTP(w, r, service, action)
	default:
		http.Error(w, `{"error":"unknown SSH action"}`, http.StatusBadRequest)
	}
}

// proxySSHHTTP forwards HTTP SSH requests to vault-proxy.
func (h *SSHHandler) proxySSHHTTP(w http.ResponseWriter, r *http.Request, service, action string) {
	client := h.Manager.Client()
	vaultURL := fmt.Sprintf("%s/ssh/%s/%s", client.Addr, service, action)

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, vaultURL, r.Body)
	if err != nil {
		log.Printf("[ssh] create proxy request: %v", err)
		http.Error(w, `{"error":"SSH service unavailable"}`, http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Authorization", "Bearer "+client.Token)
	proxyReq.Header.Set("Content-Type", r.Header.Get("Content-Type"))

	resp, err := h.Manager.Client().DoRequest(proxyReq)
	if err != nil {
		log.Printf("[ssh] vault proxy error: %v", err)
		http.Error(w, `{"error":"SSH service unavailable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers and body.
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// handleSSHSession bridges a browser WebSocket to vault-proxy's SSH WebSocket.
func (h *SSHHandler) handleSSHSession(w http.ResponseWriter, r *http.Request, service string) {
	client := h.Manager.Client()

	// Build vault-proxy WebSocket URL.
	// SEC: Validate cols/rows as integers to prevent query parameter injection.
	wsURL := client.SSHSessionURL(service)
	if cols := r.URL.Query().Get("cols"); cols != "" {
		if _, err := strconv.Atoi(cols); err != nil {
			http.Error(w, `{"error":"cols must be an integer"}`, http.StatusBadRequest)
			return
		}
		wsURL += "?cols=" + cols
		if rows := r.URL.Query().Get("rows"); rows != "" {
			if _, err := strconv.Atoi(rows); err != nil {
				http.Error(w, `{"error":"rows must be an integer"}`, http.StatusBadRequest)
				return
			}
			wsURL += "&rows=" + rows
		}
	} else if rows := r.URL.Query().Get("rows"); rows != "" {
		if _, err := strconv.Atoi(rows); err != nil {
			http.Error(w, `{"error":"rows must be an integer"}`, http.StatusBadRequest)
			return
		}
		wsURL += "?rows=" + rows
	}

	// Accept browser WebSocket.
	var originPatterns []string
	if h.AllowedOrigin != "" {
		originPatterns = []string{h.AllowedOrigin}
	}
	browserConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: originPatterns,
	})
	if err != nil {
		log.Printf("[ssh] browser websocket accept: %v", err)
		return
	}

	// Dial vault-proxy WebSocket via Unix socket transport.
	vaultHeader := http.Header{}
	vaultHeader.Set("Authorization", "Bearer "+client.Token)
	unixHTTPClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", h.Manager.SocketPath())
			},
		},
	}
	vaultConn, _, err := websocket.Dial(r.Context(), wsURL, &websocket.DialOptions{
		HTTPHeader: vaultHeader,
		HTTPClient: unixHTTPClient,
	})
	if err != nil {
		browserConn.Close(websocket.StatusInternalError, "failed to connect to SSH service")
		log.Printf("[ssh] vault websocket dial: %v", err)
		return
	}

	log.Printf("[ssh] session bridging started: service=%s", service)

	// SEC: Limit message size to prevent memory exhaustion.
	browserConn.SetReadLimit(32768) // 32KB — sufficient for terminal I/O
	vaultConn.SetReadLimit(32768)

	ctx := r.Context()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			browserConn.Close(websocket.StatusNormalClosure, "session ended")
			vaultConn.Close(websocket.StatusNormalClosure, "session ended")
		})
	}
	defer cleanup()

	// Browser → Vault
	go func() {
		defer cleanup()
		for {
			typ, data, err := browserConn.Read(ctx)
			if err != nil {
				return
			}
			if err := vaultConn.Write(ctx, typ, data); err != nil {
				return
			}
		}
	}()

	// Vault → Browser
	for {
		typ, data, err := vaultConn.Read(ctx)
		if err != nil {
			return
		}
		if err := browserConn.Write(ctx, typ, data); err != nil {
			return
		}
	}
}

func (h *SSHHandler) checkSSHAuth(r *http.Request) bool {
	// Bearer token.
	if h.AuthToken != "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") && subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(h.AuthToken)) == 1 {
			return true
		}
	}
	// Session cookie.
	if h.Sessions != nil {
		if c, err := r.Cookie("cc_session"); err == nil && h.Sessions.Valid(c.Value) {
			return true
		}
	}
	return false
}
