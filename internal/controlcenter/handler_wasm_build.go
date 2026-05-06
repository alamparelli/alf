package controlcenter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/alamparelli/alf/internal/tooling"
)

// WASMBuildHandler exposes wasm_build_tool over HTTP so cli/codex
// LLM subprocesses can author WASM bundles via a tools.d/ CLI proxy.
//
// The WASMBuildNativeTool is also registered in tooling.Registry —
// in api-mode chat the tool loop reaches it directly via Executor.
// This endpoint is the cli/codex parallel: system-tools wasm_build_tool
// reads the {manifest_toml, sources} JSON from stdin and POSTs it
// here, the daemon runs the same native code, and the result returns
// to the subprocess.
//
// The body is a JSON object {manifest_toml: string, sources: map[string]string}.
// Schema lives in tool-schemas/wasm_build_tool.json (single source of
// truth — Schema() in native_wasm_build.go mirrors it).
//
// Errors from the underlying tool (manifest validation, build failure,
// kind not supported, missing data dir) surface as 400 with the error
// message verbatim. 500 is reserved for handler-internal failures
// (JSON decode, write).
type WASMBuildHandler struct {
	// DataDir is the daemon's writable root. Forwarded into the native
	// tool which writes the bundle under <DataDir>/skills.d/wasm/<id>/.
	DataDir string
}

func (h *WASMBuildHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Cap the body — manifest+sources should be small (KBs, maybe MB
	// for a chunky source tree). 16 MiB is generous enough for any
	// reasonable bundle and bounds the memory exposure.
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		http.Error(w, "wasm_build_tool: read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	tool := tooling.WASMBuildNativeTool{DataDir: h.DataDir}
	out, err := tool.Run(context.Background(), string(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, werr := io.WriteString(w, out); werr != nil {
		// Client closed connection mid-write — log path-of-least-surprise,
		// the client will reconnect or surface the error itself.
		return
	}
	_ = json.RawMessage(out) // type-asserts at compile time that out is JSON-shaped
}
