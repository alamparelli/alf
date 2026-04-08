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
// Supports fire-and-forget mode with on_complete chain callbacks.
type LLMInvokeHandler struct {
	ToolRegistry  *tooling.Registry
	TierStore     TierStore
	CurrentConvID func() string // returns current CC conversation ID for chain origin
}

type llmInvokeRequest struct {
	Tier          string                 `json:"tier"`
	Prompt        string                 `json:"prompt"`
	System        string                 `json:"system,omitempty"`
	FireAndForget bool                   `json:"fire_and_forget,omitempty"`
	OnComplete    *tooling.LLMOnComplete `json:"on_complete,omitempty"`
	MaxDepth      int                    `json:"max_depth,omitempty"`
	Origin        *tooling.ChainOrigin   `json:"origin,omitempty"`
}

type llmInvokeResponse struct {
	Text    string `json:"text,omitempty"`
	ChainID string `json:"chain_id,omitempty"`
	Status  string `json:"status,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (h *LLMInvokeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req llmInvokeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyLarge)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Tier == "" || req.Prompt == "" {
		respondError(w, http.StatusBadRequest, "tier and prompt required")
		return
	}

	tool := h.ToolRegistry.GetNative("llm")
	if tool == nil {
		respondError(w, http.StatusServiceUnavailable, "llm tool not registered")
		return
	}

	// Build tool args JSON including chain fields.
	toolArgs := map[string]any{
		"tier":   req.Tier,
		"prompt": req.Prompt,
	}
	if req.System != "" {
		toolArgs["system"] = req.System
	}
	if req.FireAndForget {
		toolArgs["fire_and_forget"] = true
		toolArgs["max_depth"] = req.MaxDepth
		if req.OnComplete != nil {
			toolArgs["on_complete"] = req.OnComplete
		}
		// Propagate origin for callback routing; default to CC with current conv.
		if req.Origin != nil {
			toolArgs["origin"] = req.Origin
		} else {
			origin := tooling.ChainOrigin{Source: "cc"}
			if h.CurrentConvID != nil {
				origin.ConvID = h.CurrentConvID()
			}
			toolArgs["origin"] = origin
		}
	}

	argsJSON, _ := json.Marshal(toolArgs)

	// For fire-and-forget, no timeout on the initial dispatch (it returns immediately).
	// For sync, resolve timeout from tier definition.
	if req.FireAndForget {
		result, err := tool.Run(r.Context(), string(argsJSON))
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(result))
		return
	}

	// Sync mode: apply tier timeout.
	timeout := 10 * time.Minute
	if h.TierStore != nil {
		tiers := h.TierStore.Current()
		for _, t := range tiers.Tiers {
			if t.Name == req.Tier && t.Enabled && t.TimeoutMin > 0 {
				timeout = time.Duration(t.TimeoutMin) * time.Minute
				break
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	result, err := tool.Run(ctx, string(argsJSON))
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(llmInvokeResponse{Text: result})
}
