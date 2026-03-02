package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// TierFilesHandler handles system prompt and skills CRUD for a specific tier.
// Routes:
//
//	GET    /api/tiers/{name}/system-prompt     → read
//	PUT    /api/tiers/{name}/system-prompt     → write
//	GET    /api/tiers/{name}/skills/           → list
//	GET    /api/tiers/{name}/skills/{skill}    → read
//	PUT    /api/tiers/{name}/skills/{skill}    → write
//	DELETE /api/tiers/{name}/skills/{skill}    → delete
type TierFilesHandler struct {
	TierFS   TierFSProvider
	Notifier Notifier
}

// TierFSProvider abstracts tierfs operations for the handler.
type TierFSProvider interface {
	EnsureDir(tierName string) error
	SystemPrompt(tierName string) string
	WriteSystemPrompt(tierName, content string) error
	SkillStore(tierName string) ResourceStore
}

func (h *TierFilesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Expected paths:
	//   /api/tiers/{name}/system-prompt
	//   /api/tiers/{name}/skills/
	//   /api/tiers/{name}/skills/{skill}
	path := r.URL.Path

	// Strip prefix to get: {name}/system-prompt or {name}/skills/{skill}
	const prefix = "/api/tiers/"
	if !strings.HasPrefix(path, prefix) {
		http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
		return
	}
	rest := path[len(prefix):]

	// Parse tier name.
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		http.Error(w, `{"error":"invalid path"}`, http.StatusBadRequest)
		return
	}
	tierName := rest[:slashIdx]
	subPath := rest[slashIdx+1:] // "system-prompt" or "skills/" or "skills/{skill}"

	if tierName == "" || !validTierName.MatchString(tierName) {
		http.Error(w, `{"error":"invalid tier name"}`, http.StatusBadRequest)
		return
	}

	if subPath == "system-prompt" {
		h.handleSystemPrompt(w, r, tierName)
		return
	}

	if strings.HasPrefix(subPath, "skills") {
		skillName := ""
		if len(subPath) > 7 { // "skills/" + something
			skillName = subPath[7:]
		}
		h.handleSkills(w, r, tierName, skillName)
		return
	}

	http.Error(w, `{"error":"unknown tier sub-resource"}`, http.StatusNotFound)
}

func (h *TierFilesHandler) handleSystemPrompt(w http.ResponseWriter, r *http.Request, tierName string) {
	switch r.Method {
	case http.MethodGet:
		content := h.TierFS.SystemPrompt(tierName)
		data, _ := json.Marshal(map[string]any{"name": "system-prompt", "content": content})
		w.Write(data)
	case http.MethodPut:
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
		if err := h.TierFS.WriteSystemPrompt(tierName, payload.Content); err != nil {
			http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
			return
		}
		if h.Notifier != nil {
			h.Notifier.Notify(ReloadTierFiles)
		}
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *TierFilesHandler) handleSkills(w http.ResponseWriter, r *http.Request, tierName, skillName string) {
	store := h.TierFS.SkillStore(tierName)
	rh := &ResourceHandler{
		Store:    store,
		Notifier: h.Notifier,
		Event:    ReloadTierFiles,
	}

	switch r.Method {
	case http.MethodGet:
		if skillName == "" {
			rh.list(w)
		} else {
			rh.get(w, skillName)
		}
	case http.MethodPut:
		if skillName == "" {
			http.Error(w, `{"error":"skill name required"}`, http.StatusBadRequest)
			return
		}
		rh.put(w, r, skillName)
	case http.MethodDelete:
		if skillName == "" {
			http.Error(w, `{"error":"skill name required"}`, http.StatusBadRequest)
			return
		}
		rh.del(w, skillName)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
