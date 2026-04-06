package controlcenter

import (
	"encoding/json"
	"net/http"

	"github.com/alamparelli/alf/internal/tooling"
)

// ChainHandler exposes a chain invocation endpoint for the system-tools CLI.
// POST /api/tasks/chain with {steps: [{tier, prompt, system?}, ...]}
type ChainHandler struct {
	ToolRegistry  *tooling.Registry
	CurrentConvID func() string
}

type chainRequest struct {
	Steps  []tooling.ChainStep  `json:"steps"`
	Origin *tooling.ChainOrigin `json:"origin,omitempty"`
}

func (h *ChainHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req chainRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyLarge)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if len(req.Steps) < 2 {
		http.Error(w, "at least 2 steps required", http.StatusBadRequest)
		return
	}

	tool := h.ToolRegistry.GetNative("task")
	if tool == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "task tool not registered"})
		return
	}

	// Build tool args as JSON — delegate to the native task tool's chain action.
	toolArgs := map[string]any{
		"action": "chain",
		"steps":  req.Steps,
	}

	argsJSON, _ := json.Marshal(toolArgs)

	// Inject chain origin for routing results back to the correct channel.
	ctx := r.Context()
	var origin tooling.ChainOrigin
	if req.Origin != nil {
		origin = *req.Origin
	} else {
		origin = tooling.ChainOrigin{Source: "cc"}
		if h.CurrentConvID != nil {
			origin.ConvID = h.CurrentConvID()
		}
	}
	ctx = tooling.WithChainOrigin(ctx, origin)

	result, err := tool.Run(ctx, string(argsJSON))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(result))
}
