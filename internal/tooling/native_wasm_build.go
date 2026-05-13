package tooling

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/runtime/wasm/builder"
)

// WASMBuildNativeTool is the LLM-facing path from
// ARCHITECTURE-SECURITY.md §4.1: build a WASM capability inside the
// daemon, with daemon-observed compilation and manifest validation.
// The produced bundle is UNSIGNED at this layer — step 9's boot-time
// loader handles auto-signing with the daemon key before first
// instantiation.
//
// Import cross-check is deliberately NOT run here. The authoritative
// invariant lives at instantiate time (runtime/wasm.CheckImports,
// step 3 of #386). Running it a second time at build time would add
// an import of runtime/wasm to the tooling package — which creates a
// cycle because runtime imports tooling. The trade-off is a slightly
// later failure point (boot discovery vs. build tool) for lying
// manifests; catching them at instantiate time is sufficient and
// matches the single-source-of-truth architecture.
//
// Input JSON schema:
//
//	{
//	  "manifest_toml": "<raw envelope TOML>",
//	  "sources": { "<relative path>": "<file content>", ... }
//	}
//
// The manifest is the authoritative source of id/kind — the tool
// does not accept those as independent inputs because doing so
// would invite inconsistency between what's signed later and what
// the builder saw.
type WASMBuildNativeTool struct {
	// DataDir is the daemon's writable root. Bundles install under
	// <DataDir>/skills.d/wasm/<id>/.
	DataDir string
}

func (WASMBuildNativeTool) ToolName() string { return "wasm_build_tool" }

func (WASMBuildNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name: "wasm_build_tool",
		Description: "Compile a Go source tree into a WASM capability bundle and install it under skills.d/wasm/<id>/. " +
			"The manifest_toml must be a valid 0.8.0 envelope manifest (see docs/MANIFEST-SCHEMA.md). " +
			"The sources map keys are relative paths inside the module root (e.g., go.mod, main.go). " +
			"The bundle is installed unsigned; the daemon auto-signs at boot discovery.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"manifest_toml": map[string]any{
					"type":        "string",
					"description": "Raw TOML text of the envelope manifest — validates against MANIFEST-SCHEMA §3.",
				},
				"sources": map[string]any{
					"type":        "object",
					"description": "File map keyed by relative path inside the Go module root (e.g., go.mod, main.go, subpkg/foo.go).",
					"additionalProperties": map[string]any{
						"type": "string",
					},
				},
			},
			"required":             []string{"manifest_toml", "sources"},
			"additionalProperties": false,
		},
	}
}

type wasmBuildArgs struct {
	ManifestTOML string            `json:"manifest_toml"`
	Sources      map[string]string `json:"sources"`
}

type wasmBuildResult struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	BundleDir   string `json:"bundle_dir"`
	WasmSHA256  string `json:"wasm_sha256"`
	WasmBytes   int    `json:"wasm_bytes"`
	Unsigned    bool   `json:"unsigned"`
	SigningNote string `json:"signing_note"`
}

func (t WASMBuildNativeTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args wasmBuildArgs
	if err := parseArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if args.ManifestTOML == "" {
		return "", fmt.Errorf("manifest_toml is required")
	}
	if len(args.Sources) == 0 {
		return "", fmt.Errorf("sources map is empty")
	}
	if t.DataDir == "" {
		return "", fmt.Errorf("wasm_build_tool: DataDir not configured")
	}

	// 1. Parse + validate the manifest against the envelope schema.
	// This rejects deferred blocks (exec/secrets/memory) per
	// MANIFEST-SCHEMA §3.4 and enforces required fields. (`http` is
	// un-deferred since #421 Wave 1; `events` and `tools` since
	// #399/#389.)
	m, err := envelope.Validate([]byte(args.ManifestTOML))
	if err != nil {
		return "", fmt.Errorf("manifest validation: %w", err)
	}
	if m.Kind != envelope.KindWASMTool && m.Kind != envelope.KindWASMApp {
		return "", fmt.Errorf("kind %q not supported by wasm_build_tool (expected wasm-tool or wasm-app)", m.Kind)
	}
	if m.ID == "" {
		return "", fmt.Errorf("manifest id is empty after validation")
	}

	// 2. Build.
	srcFiles := make(map[string][]byte, len(args.Sources))
	for k, v := range args.Sources {
		srcFiles[k] = []byte(v)
	}
	wasmBytes, err := builder.Build(ctx, builder.Source{Files: srcFiles}, builder.BuildConfig{})
	if err != nil {
		return "", err
	}

	// 3. Install bundle.
	bundleDir := filepath.Join(t.DataDir, "skills.d", "wasm", m.ID)
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		return "", fmt.Errorf("install bundle dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "manifest.toml"), []byte(args.ManifestTOML), 0o600); err != nil {
		return "", fmt.Errorf("write manifest.toml: %w", err)
	}
	wasmPath := filepath.Join(bundleDir, m.ID+".wasm")
	if err := os.WriteFile(wasmPath, wasmBytes, 0o600); err != nil {
		return "", fmt.Errorf("write wasm: %w", err)
	}

	// 4. Compute sha256 for the signer (step 9 auto-signing uses
	// this hash via envelope.BuildTrustedComment).
	sum := sha256.Sum256(wasmBytes)
	res := wasmBuildResult{
		ID:          m.ID,
		Kind:        string(m.Kind),
		BundleDir:   bundleDir,
		WasmSHA256:  hex.EncodeToString(sum[:]),
		WasmBytes:   len(wasmBytes),
		Unsigned:    true,
		SigningNote: "Bundle is unsigned; daemon auto-signs at boot discovery (step 9 of #386).",
	}
	out, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(out), nil
}

// assert at compile time that the tool satisfies the NativeTool
// contract — prevents silent drift if NativeTool evolves.
var _ NativeTool = WASMBuildNativeTool{}
