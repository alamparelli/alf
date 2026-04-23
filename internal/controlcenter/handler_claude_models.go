package controlcenter

import (
	"encoding/json"
	"net/http"
)

// ClaudeModelsHandler serves the user-editable list of Claude Code model
// identifiers used to populate the tier-form model dropdown. The store
// is looked up from the process-wide global so the handler stays
// independent of Deps wiring.
//
// GET /api/models/claude → {"models": ["claude-opus-4-7", ...]}
type ClaudeModelsHandler struct{}

func (h *ClaudeModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	models := []string{}
	if s := GetClaudeModelsStore(); s != nil {
		if cur := s.Current(); cur != nil {
			models = cur
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"models": models,
	})
}
