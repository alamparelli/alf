package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AppStorageHandler provides per-app key/value JSON storage.
//
//	GET    /api/apps/{slug}/storage         → full store
//	GET    /api/apps/{slug}/storage?key=foo → single value
//	PUT    /api/apps/{slug}/storage         → merge keys from body
//	DELETE /api/apps/{slug}/storage?key=foo → delete key
type AppStorageHandler struct {
	DataDir string
	mu      sync.Mutex
}

func (h *AppStorageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/apps/{slug}/storage
	rest := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 || parts[1] != "storage" {
		http.NotFound(w, r)
		return
	}
	slug := parts[0]
	if !validName.MatchString(slug) {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid app name"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r, slug)
	case http.MethodPut:
		h.handlePut(w, r, slug)
	case http.MethodDelete:
		h.handleDelete(w, r, slug)
	default:
		methodNotAllowed(w)
	}
}

func (h *AppStorageHandler) storagePath(slug string) string {
	return filepath.Join(h.DataDir, "apps", slug, "data", "storage.json")
}

func (h *AppStorageHandler) load(slug string) (map[string]any, error) {
	data, err := os.ReadFile(h.storagePath(slug))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var store map[string]any
	if err := json.Unmarshal(data, &store); err != nil {
		return map[string]any{}, nil
	}
	return store, nil
}

func (h *AppStorageHandler) save(slug string, store map[string]any) error {
	p := h.storagePath(slug)
	os.MkdirAll(filepath.Dir(p), 0o775)
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o664)
}

func (h *AppStorageHandler) handleGet(w http.ResponseWriter, r *http.Request, slug string) {
	store, err := h.load(slug)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
		return
	}

	key := r.URL.Query().Get("key")
	if key != "" {
		val, ok := store[key]
		if !ok {
			respondJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{"key": key, "value": val})
		return
	}

	respondJSON(w, http.StatusOK, store)
}

func (h *AppStorageHandler) handlePut(w http.ResponseWriter, r *http.Request, slug string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "read body failed"})
		return
	}

	var incoming map[string]any
	if err := json.Unmarshal(body, &incoming); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	store, err := h.load(slug)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
		return
	}

	for k, v := range incoming {
		if v == nil {
			delete(store, k)
		} else {
			store[k] = v
		}
	}

	if err := h.save(slug, store); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}

	respondJSON(w, http.StatusOK, store)
}

func (h *AppStorageHandler) handleDelete(w http.ResponseWriter, r *http.Request, slug string) {
	key := r.URL.Query().Get("key")
	if key == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "key parameter required"})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	store, err := h.load(slug)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
		return
	}

	delete(store, key)

	if err := h.save(slug, store); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
