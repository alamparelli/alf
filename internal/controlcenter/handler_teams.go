package controlcenter

import (
	"crypto/rand"
	"encoding/hex"
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
	AgentStore  agents.Store
	DataDir     string // data directory (e.g. /home/alf/data)
	Notifier    Notifier
	EventBroker *EventBroker
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
		methodNotAllowed(w)
	}
}

func (h *TeamsHandler) agentsDir() string {
	return filepath.Join(h.DataDir, "agents", "teams")
}

func (h *TeamsHandler) list(w http.ResponseWriter) {
	if h.AgentStore == nil {
		respondJSON(w, http.StatusOK, map[string]any{"teams": []any{}})
		return
	}
	teams := h.AgentStore.All()
	if teams == nil {
		teams = []*agents.TeamConfig{}
	}
	respondJSON(w, http.StatusOK, map[string]any{"teams": teams})
}

func (h *TeamsHandler) save(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		respondError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	// Validate JSON structure.
	var tc agents.TeamConfig
	if err := json.Unmarshal(body, &tc); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if tc.Name == "" {
		respondError(w, http.StatusBadRequest, "team name is required")
		return
	}

	dir := h.agentsDir()
	os.MkdirAll(dir, 0o755)

	// Generate ID for new teams; existing teams keep their ID.
	if tc.ID == "" {
		b := make([]byte, 8)
		rand.Read(b)
		tc.ID = hex.EncodeToString(b)
	}

	// Use ID as filename for stable storage (supports rename).
	filename := tc.ID + ".json"

	// Clean up old name-based file if it exists and differs from ID-based file.
	// This handles migration from name-based to ID-based storage.
	safeName := sanitizeTeamName(tc.Name)
	if safeName != "" {
		oldPath := filepath.Join(dir, safeName+".json")
		newPath := filepath.Join(dir, filename)
		if oldPath != newPath {
			// Check if old name-based file exists.
			if _, err := os.Stat(oldPath); err == nil {
				os.Remove(oldPath)
			}
		}
	}

	// Also clean up any other file with the same ID (different old name).
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == filename {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var existing agents.TeamConfig
		if json.Unmarshal(data, &existing) == nil && existing.ID == tc.ID {
			os.Remove(path) // remove old file with same ID but different name
		}
	}

	// Pretty-print the JSON before saving.
	pretty, _ := json.MarshalIndent(tc, "", "  ")

	dest := filepath.Join(dir, filename)
	if err := os.WriteFile(dest, pretty, 0o644); err != nil {
		respondError(w, http.StatusInternalServerError, "write failed: "+err.Error())
		return
	}

	// Reload store immediately so the next GET sees the change.
	if h.AgentStore != nil {
		h.AgentStore.Reload()
	}
	notifyReload(h.Notifier, ReloadAgents)
	h.EventBroker.Emit(EventAgents)

	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "id": tc.ID, "file": filename})
}

func (h *TeamsHandler) del(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	id := r.URL.Query().Get("id")
	if name == "" && id == "" {
		respondError(w, http.StatusBadRequest, "missing name or id parameter")
		return
	}

	dir := h.agentsDir()
	var removed bool

	// Try ID-based file first.
	if id != "" {
		dest := filepath.Join(dir, id+".json")
		if err := os.Remove(dest); err == nil {
			removed = true
		}
	}

	// Fall back to name-based file.
	if !removed && name != "" {
		safeName := sanitizeTeamName(name)
		if safeName == "" {
			respondError(w, http.StatusBadRequest, "invalid team name")
			return
		}
		dest := filepath.Join(dir, safeName+".json")
		if err := os.Remove(dest); err == nil {
			removed = true
		}
	}

	// Last resort: scan for matching ID or name in files.
	if !removed {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var tc agents.TeamConfig
			if json.Unmarshal(data, &tc) == nil {
				if (id != "" && tc.ID == id) || (name != "" && tc.Name == name) {
					os.Remove(path)
					removed = true
					break
				}
			}
		}
	}

	if !removed {
		respondError(w, http.StatusNotFound, "team not found")
		return
	}

	if h.AgentStore != nil {
		h.AgentStore.Reload()
	}
	notifyReload(h.Notifier, ReloadAgents)
	h.EventBroker.Emit(EventAgents)

	respondOK(w)
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
