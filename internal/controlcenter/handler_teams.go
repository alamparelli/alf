package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alamparelli/alf/internal/agents"
)

// TeamsHandler serves agent team configurations (CRUD on agents/teams/*.json).
type TeamsHandler struct {
	AgentStore agents.Store
	DataDir    string // data directory (e.g. /home/alf/data)
	Notifier   Notifier
}

func (h *TeamsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.list(w)
	case http.MethodPut:
		h.save(w, r)
	case http.MethodDelete:
		h.del(w, r)
	default:
		http.Error(w, jsonErr("method not allowed"), http.StatusMethodNotAllowed)
	}
}

func (h *TeamsHandler) agentsDir() string {
	return filepath.Join(h.DataDir, "agents", "teams")
}

func (h *TeamsHandler) list(w http.ResponseWriter) {
	if h.AgentStore == nil {
		json.NewEncoder(w).Encode(map[string]any{"teams": []any{}})
		return
	}
	teams := h.AgentStore.All()
	if teams == nil {
		teams = []*agents.TeamConfig{}
	}
	json.NewEncoder(w).Encode(map[string]any{"teams": teams})
}

func (h *TeamsHandler) save(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, jsonErr("failed to read body"), http.StatusBadRequest)
		return
	}

	// Validate JSON structure.
	var tc agents.TeamConfig
	if err := json.Unmarshal(body, &tc); err != nil {
		http.Error(w, jsonErr("invalid JSON: "+err.Error()), http.StatusBadRequest)
		return
	}
	if tc.Name == "" {
		http.Error(w, jsonErr("team name is required"), http.StatusBadRequest)
		return
	}

	// Sanitize filename: only allow alphanumeric, hyphens, underscores.
	safeName := sanitizeTeamName(tc.Name)
	if safeName == "" {
		http.Error(w, jsonErr("invalid team name"), http.StatusBadRequest)
		return
	}

	dir := h.agentsDir()
	os.MkdirAll(dir, 0o755)

	// Pretty-print the JSON before saving.
	pretty, _ := json.MarshalIndent(tc, "", "  ")

	dest := filepath.Join(dir, safeName+".json")
	if err := os.WriteFile(dest, pretty, 0o644); err != nil {
		http.Error(w, jsonErr("write failed: "+err.Error()), http.StatusInternalServerError)
		return
	}

	// Reload store immediately so the next GET sees the change.
	if h.AgentStore != nil {
		h.AgentStore.Reload()
	}
	if h.Notifier != nil {
		h.Notifier.Notify(ReloadAgents)
	}

	json.NewEncoder(w).Encode(map[string]any{"ok": true, "file": safeName + ".json"})
}

func (h *TeamsHandler) del(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, jsonErr("missing name parameter"), http.StatusBadRequest)
		return
	}

	safeName := sanitizeTeamName(name)
	if safeName == "" {
		http.Error(w, jsonErr("invalid team name"), http.StatusBadRequest)
		return
	}

	dest := filepath.Join(h.agentsDir(), safeName+".json")
	if err := os.Remove(dest); err != nil {
		if os.IsNotExist(err) {
			http.Error(w, jsonErr("team not found"), http.StatusNotFound)
		} else {
			http.Error(w, jsonErr("delete failed: "+err.Error()), http.StatusInternalServerError)
		}
		return
	}

	if h.AgentStore != nil {
		h.AgentStore.Reload()
	}
	if h.Notifier != nil {
		h.Notifier.Notify(ReloadAgents)
	}

	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// sanitizeTeamName returns a safe filename component from a team name.
func sanitizeTeamName(name string) string {
	name = strings.TrimSpace(name)
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
