package tooling

import (
	"context"
	"testing"

	"github.com/alamparelli/alf/internal/capability"
)

// fakeCapability is the minimal capability.Capability for these tests.
// It exists so RegisterWasmTool's dual-registration into capRegistry can
// be exercised without spinning up a real wasm.Adapter.
type fakeCapability struct {
	id   capability.ID
	kind capability.Kind
}

func (f *fakeCapability) Manifest() capability.Manifest {
	return capability.Manifest{ID: f.id, Kind: f.kind, Name: string(f.id)}
}
func (f *fakeCapability) Permissions() capability.PermissionSet { return capability.PermissionSet{} }
func (f *fakeCapability) Execute(_ context.Context, _ capability.Input) (capability.Output, error) {
	return capability.Output{}, nil
}

// TestRegisterWasmTool_AddsToSchemas pins the headline behaviour: a
// wasm-tool registered via RegisterWasmTool ends up in r.schemas and
// is returned by AllSchemas() — the surface the chat engine builds the
// LLM tool list from. Without this wiring, wasm-tools are invisible to
// the LLM (the pre-#423 state).
func TestRegisterWasmTool_AddsToSchemas(t *testing.T) {
	reg := NewRegistry(t.TempDir())
	cap := &fakeCapability{id: "http-hello", kind: capability.KindTool}
	reg.RegisterWasmTool(ToolSchema{
		Name:        "http-hello",
		Description: "Fetch a URL via the WASM http handle",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string"},
			},
		},
	}, cap)

	got, ok := reg.Get("http-hello")
	if !ok {
		t.Fatal("Get(http-hello) returned false — schema not registered")
	}
	if got.Description != "Fetch a URL via the WASM http handle" {
		t.Errorf("Description=%q", got.Description)
	}
	if got.Parameters["type"] != "object" {
		t.Errorf("Parameters.type=%v", got.Parameters["type"])
	}
}

// TestRegisterWasmTool_AllSchemasIncludesWasm pins that AllSchemas()
// returns the wasm-tool entry alongside any registered native tools.
// This is what the API-LLM tool-loop iterates to build tool definitions.
func TestRegisterWasmTool_AllSchemasIncludesWasm(t *testing.T) {
	reg := NewRegistry(t.TempDir())
	reg.RegisterWasmTool(ToolSchema{Name: "http-hello", Description: "h"}, nil)

	all := reg.AllSchemas()
	var found bool
	for _, s := range all {
		if s.Name == "http-hello" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AllSchemas() does not include http-hello: %v", all)
	}
}

// TestRegisterWasmTool_MirrorsToCapRegistry pins the dual-registration
// pattern (mirrors RegisterNative): when a capability.Registry is
// attached via SetCapabilityRegistry, RegisterWasmTool also registers
// the underlying Capability there. The chat engine resolves tool_use
// dispatches via capRegistry.Get, so without this mirror the wasm-tool
// would appear in the LLM surface but the dispatch would 404.
func TestRegisterWasmTool_MirrorsToCapRegistry(t *testing.T) {
	cr := capability.NewRegistry()
	reg := NewRegistry(t.TempDir())
	reg.SetCapabilityRegistry(cr)

	cap := &fakeCapability{id: "http-hello", kind: capability.KindTool}
	reg.RegisterWasmTool(ToolSchema{Name: "http-hello", Description: "h"}, cap)

	if _, ok := cr.Get("http-hello"); !ok {
		t.Error("capRegistry does not contain http-hello — mirror failed")
	}
}

// TestRegisterWasmTool_NilCapIsAllowed pins that a nil Capability is
// accepted (the schema-only path: a test or scaffolder may want to
// register the schema for surface validation without a live adapter).
// The mirror is silently skipped in that case.
func TestRegisterWasmTool_NilCapIsAllowed(t *testing.T) {
	cr := capability.NewRegistry()
	reg := NewRegistry(t.TempDir())
	reg.SetCapabilityRegistry(cr)

	reg.RegisterWasmTool(ToolSchema{Name: "schema-only", Description: "x"}, nil)

	if _, ok := reg.Get("schema-only"); !ok {
		t.Error("schema-only not registered in tooling.Registry")
	}
	if _, ok := cr.Get("schema-only"); ok {
		t.Error("nil cap should not be registered in capRegistry")
	}
}

// TestResolveWildcard_IncludesWasm pins #423's wildcard expansion:
// a tier with `tools = ["*"]` now includes WASM-backed tools alongside
// native + CLI tools. Without this, the operator would have to list
// each WASM bundle id explicitly in the tier config.
func TestResolveWildcard_IncludesWasm(t *testing.T) {
	reg := NewRegistry(t.TempDir())
	reg.RegisterNative(&fakeNativeTool{name: "native-tool"})
	reg.RegisterWasmTool(ToolSchema{Name: "wasm-tool-1", Description: "w1"}, nil)
	reg.RegisterWasmTool(ToolSchema{Name: "wasm-tool-2", Description: "w2"}, nil)

	tools := ResolveWildcard(t.TempDir(), reg)
	seen := make(map[string]bool)
	for _, n := range tools {
		seen[n] = true
	}
	for _, want := range []string{"native-tool", "wasm-tool-1", "wasm-tool-2"} {
		if !seen[want] {
			t.Errorf("ResolveWildcard missing %q: %v", want, tools)
		}
	}
}

// TestWasmToolNames_EmptyByDefault pins the empty-registry state — a
// freshly constructed registry without any RegisterWasmTool calls
// returns a nil slice (not an empty non-nil slice, matching the
// NativeToolNames convention for callers that range over the result).
func TestWasmToolNames_EmptyByDefault(t *testing.T) {
	reg := NewRegistry(t.TempDir())
	if got := reg.WasmToolNames(); got != nil {
		t.Errorf("WasmToolNames() = %v, want nil", got)
	}
}

// TestRegisterWasmTool_SurvivesRescan pins the boot-bug fix: a wasm-tool
// registered through RegisterWasmTool must survive a subsequent Rescan().
// Marketplace RestoreInstalled() and chat-session reloads call Rescan
// after setupWASMLoader has already registered Tier-3-signed bundles —
// without this guard those schemas would silently disappear from the
// LLM tool surface even though their capability adapters stay live.
func TestRegisterWasmTool_SurvivesRescan(t *testing.T) {
	reg := NewRegistry(t.TempDir())
	reg.RegisterWasmTool(ToolSchema{
		Name:        "http-hello",
		Description: "Fetch a URL via the bundle's scoped HTTP handle",
		Parameters:  map[string]any{"type": "object"},
	}, nil)

	if _, ok := reg.Get("http-hello"); !ok {
		t.Fatal("precondition: http-hello not registered")
	}

	reg.Rescan()

	got, ok := reg.Get("http-hello")
	if !ok {
		t.Fatal("Rescan() wiped wasm-tool http-hello from schemas")
	}
	if got.Description != "Fetch a URL via the bundle's scoped HTTP handle" {
		t.Errorf("Description=%q (mutated by Rescan?)", got.Description)
	}
}

// TestRegisterNative_SurvivesRescan pins the pre-existing native-tool
// preservation behaviour — same guarantee but for the dual-registration
// path that already existed before #423. Kept alongside the wasm-tool
// variant so a future refactor can't silently regress one without the
// other.
func TestRegisterNative_SurvivesRescan(t *testing.T) {
	reg := NewRegistry(t.TempDir())
	reg.RegisterNative(&fakeNativeTool{name: "my-native"})

	if _, ok := reg.Get("my-native"); !ok {
		t.Fatal("precondition: my-native not registered")
	}

	reg.Rescan()

	if _, ok := reg.Get("my-native"); !ok {
		t.Error("Rescan() wiped native tool from schemas")
	}
}
