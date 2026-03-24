package controlcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/alamparelli/alf/internal/tooling"
)

// LLMInvokeHandler exposes a synchronous LLM invocation endpoint.
// Used by the system-tools CLI binary to invoke tiers without needing
// its own Anthropic token — the daemon's provider registry handles auth.
type LLMInvokeHandler struct {
	ToolRegistry *tooling.Registry
}

type llmInvokeRequest struct {
	Tier   string `json:"tier"`
	Prompt string `json:"prompt"`
	System string `json:"system,omitempty"`
}

type llmInvokeResponse struct {
	Text  string `json:"text"`
	Error string `json:"error,omitempty"`
}

func (h *LLMInvokeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req llmInvokeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Tier == "" || req.Prompt == "" {
		http.Error(w, "tier and prompt required", http.StatusBadRequest)
		return
	}

	tool := h.ToolRegistry.GetNative("llm")
	if tool == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "llm tool not registered"})
		return
	}

	argsJSON, _ := json.Marshal(map[string]string{
		"tier":   req.Tier,
		"prompt": req.Prompt,
		"system": req.System,
	})

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	result, err := tool.Run(ctx, string(argsJSON))
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(llmInvokeResponse{Text: result})
}
