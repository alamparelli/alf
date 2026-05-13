package controlcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/tooling"
)

// ToolInvokeHandler dispatches a tool by name via tooling.Executor. The
// endpoint is the daemon-side counterpart of the wasm-tool CLI binary
// (#424): Claude Code subprocesses on a CLI-backend tier reach the
// agentic loop by spawning bash that runs `wasm-tool <id> <json-args>`,
// which then POSTs here over the unix tools.sock. The dispatcher is
// the same one #425.3 wired for the API tier path, so wasm-tool
// bundles, native tools, and tools.d/ binaries all funnel through one
// authority.
//
// Security
//   - Reachable only via the unix tools.sock (allowlisted in
//     tools_proxy.go) — the TCP endpoint declines on the
//     X-Tools-Socket header check upstream.
//   - Tool name is passed unmodified to Executor.Execute. The
//     Executor itself enforces hyphen/underscore desanitisation,
//     capability registry lookup, integrity guard checks, and the
//     §4.1 path traversal refusal.
//   - There is intentionally no auth header beyond the socket
//     boundary: socket presence IS the auth.
type ToolInvokeHandler struct {
	Executor *tooling.Executor
	Timeout  time.Duration // optional, defaults to the Executor's own timeout when zero
}

// ToolInvokeRequest is the wire format the wasm-tool CLI emits.
//
// `arguments` is the raw JSON string the LLM passed to the tool — we
// forward it unchanged so the Executor can route it through the
// capability layer (where it lands as capability.Input) or through the
// subprocess layer (where it lands as either CLI args via
// jsonToCLI or as stdin JSON). The handler doesn't reparse it.
type ToolInvokeRequest struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id,omitempty"`
}

// ToolInvokeResponse mirrors tooling.CallResult so the wasm-tool CLI
// can stream the output verbatim. `is_error` lets callers (and the
// bash exit code) distinguish a clean run from a tool-internal
// failure without parsing free-form `output`.
type ToolInvokeResponse struct {
	Output       string `json:"output"`
	IsError      bool   `json:"is_error,omitempty"`
	ExitCode     int    `json:"exit_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

func (h *ToolInvokeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if h.Executor == nil {
		respondError(w, http.StatusServiceUnavailable, "tool executor not configured")
		return
	}

	var req ToolInvokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Arguments == "" {
		// Tools that take no input still expect a JSON object on the
		// wire — substitute the empty object so Executor.jsonToCLI and
		// the capability dispatch path both see a well-formed payload.
		req.Arguments = "{}"
	}

	ctx := r.Context()
	if h.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.Timeout)
		defer cancel()
	}

	result := h.Executor.Execute(ctx, tooling.CallRequest{
		ID:        req.CallID,
		Name:      req.Name,
		Arguments: req.Arguments,
	})

	respondJSON(w, http.StatusOK, ToolInvokeResponse{
		Output:       result.Output,
		IsError:      result.IsError,
		ExitCode:     result.ExitCode,
		ErrorMessage: result.ErrorMessage,
	})
}
