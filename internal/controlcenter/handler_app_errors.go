package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxErrorLogEntries = 100

// AppErrorEntry represents a single app error log entry.
type AppErrorEntry struct {
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
	Stack     string `json:"stack,omitempty"`
	Source    string `json:"source,omitempty"`
}

// AppErrorHandler provides per-app error logging.
//
//	POST /api/apps/{slug}/errors → append error to log (ring buffer, max 100)
//	GET  /api/apps/{slug}/errors → read error log
type AppErrorHandler struct {
	DataDir string
	mu      sync.Mutex
}

func (h *AppErrorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/apps/{slug}/errors
	rest := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 || parts[1] != "errors" {
		http.NotFound(w, r)
		return
	}
	slug := parts[0]
	if !validName.MatchString(slug) {
		respondError(w, http.StatusBadRequest, "invalid app name")
		return
	}

	// Cross-app access check: app iframe may only write to its own error log.
	if callerSlug := extractAppSlugFromReferer(r); callerSlug != "" && callerSlug != slug {
		respondError(w, http.StatusForbidden, "cross-app error access denied")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, slug)
	case http.MethodPost:
		h.handlePost(w, r, slug)
	case http.MethodDelete:
		h.handleClear(w, slug)
	default:
		methodNotAllowed(w)
	}
}

func (h *AppErrorHandler) errorsPath(slug string) string {
	return filepath.Join(h.DataDir, "apps", slug, "data", "errors.json")
}

func (h *AppErrorHandler) load(slug string) ([]AppErrorEntry, error) {
	data, err := os.ReadFile(h.errorsPath(slug))
	if err != nil {
		if os.IsNotExist(err) {
			return []AppErrorEntry{}, nil
		}
		return nil, err
	}
	var entries []AppErrorEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return []AppErrorEntry{}, nil
	}
	return entries, nil
}

func (h *AppErrorHandler) save(slug string, entries []AppErrorEntry) error {
	p := h.errorsPath(slug)
	os.MkdirAll(filepath.Dir(p), 0o775)
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o664)
}

func (h *AppErrorHandler) handleGet(w http.ResponseWriter, slug string) {
	h.mu.Lock()
	entries, err := h.load(slug)
	h.mu.Unlock()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "read failed")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"errors": entries, "count": len(entries)})
}

func (h *AppErrorHandler) handlePost(w http.ResponseWriter, r *http.Request, slug string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16)) // 64KB max per error
	if err != nil {
		respondError(w, http.StatusBadRequest, "read body failed")
		return
	}

	var entry AppErrorEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if entry.Message == "" {
		respondError(w, http.StatusBadRequest, "message required")
		return
	}

	entry.Timestamp = time.Now().UTC().Format(time.RFC3339)

	h.mu.Lock()
	defer h.mu.Unlock()

	entries, err := h.load(slug)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "read failed")
		return
	}

	entries = append(entries, entry)
	// Ring buffer: keep only last N entries
	if len(entries) > maxErrorLogEntries {
		entries = entries[len(entries)-maxErrorLogEntries:]
	}

	if err := h.save(slug, entries); err != nil {
		respondError(w, http.StatusInternalServerError, "write failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "logged"})
}

func (h *AppErrorHandler) handleClear(w http.ResponseWriter, slug string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	os.Remove(h.errorsPath(slug))
	respondJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}
