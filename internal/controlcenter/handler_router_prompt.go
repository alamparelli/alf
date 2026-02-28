package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// RouterPromptHandler handles GET/PUT /api/router-prompt.
type RouterPromptHandler struct {
	ConfigDir string
	Notifier  Notifier
}

func (h *RouterPromptHandler) path() string {
	return filepath.Join(h.ConfigDir, "router-prompt.md")
}

func (h *RouterPromptHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.get(w)
	case http.MethodPut:
		h.put(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *RouterPromptHandler) get(w http.ResponseWriter) {
	data, err := os.ReadFile(h.path())
	content := ""
	if err == nil {
		content = string(data)
	}
	resp, _ := json.Marshal(map[string]any{"content": content})
	w.Write(resp)
}

func (h *RouterPromptHandler) put(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxResourceSize+1024))
	if err != nil {
		http.Error(w, jsonErr("failed to read body"), http.StatusBadRequest)
		return
	}

	var payload struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, jsonErr("invalid JSON: "+err.Error()), http.StatusBadRequest)
		return
	}

	p := h.path()
	os.MkdirAll(filepath.Dir(p), 0o755)

	content := strings.TrimSpace(payload.Content)
	if content == "" {
		// Remove file if content is empty.
		os.Remove(p)
	} else {
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
			http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
			return
		}
		if err := os.Rename(tmp, p); err != nil {
			os.Remove(tmp)
			http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
			return
		}
	}

	if h.Notifier != nil {
		h.Notifier.Notify(ReloadTierFiles)
	}

	w.Write([]byte(`{"ok":true}`))
}
