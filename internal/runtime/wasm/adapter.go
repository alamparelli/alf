package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
)

// Guest ABI names (WASM.md §7.2). The adapter calls these on the
// instantiated guest to ferry JSON payloads across the module
// boundary. The names are not archtest-enforced (guests can export
// anything), but NativeGuestABI below documents the contract for
// guest authors.
const (
	fnAlfAlloc  = "alf_alloc"
	fnAlfInvoke = "alf_invoke"
)

// Adapter wraps a wasm.Module behind the capability.Capability
// interface. One adapter per module — mutation of guest memory is
// serialised by adapterMu because wazero module instances are not
// safe for concurrent invocation.
//
// The adapter is constructed by the boot-time loader (step 9) and
// registered in capability.Registry so the LLM tool-loop sees the
// WASM capability alongside native Go capabilities without caring
// about its kind.
type Adapter struct {
	mod *Module

	// mu serialises Execute — wazero modules are single-threaded
	// and the alf_alloc / Memory.Write / alf_invoke / Memory.Read
	// sequence is a compound operation that must not interleave
	// across callers.
	mu sync.Mutex
}

// NewAdapter wraps the given Module. The Module is owned by the
// adapter — closing the adapter closes the module. Callers must
// not close the module independently.
func NewAdapter(m *Module) *Adapter {
	return &Adapter{mod: m}
}

// Close tears down the wrapped module. Idempotent + nil-safe.
func (a *Adapter) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mod.Close(ctx)
}

// Manifest returns the capability.Manifest projection of the
// verified envelope manifest. Today the permissions mirror what
// Instantiator.permissionsFromEnvelope produces; the full
// envelope-native Manifest contract arrives with the schema
// migration (follow-up ticket).
func (a *Adapter) Manifest() capability.Manifest {
	m := a.mod.Manifest
	return capability.Manifest{
		ID:          capability.ID(m.ID),
		Kind:        envelopeKindToCapabilityKind(m.Kind),
		Name:        m.Name,
		Version:     m.Version,
		Description: m.Description,
		Permissions: a.Permissions(),
	}
}

// Permissions returns the declared permission set. Only FilePaths
// is populated for 0.8.0 (fs is the only Tier 3.1 handle kind in
// the envelope schema); Networks / Secrets arrive with 0.9.0.
func (a *Adapter) Permissions() capability.PermissionSet {
	out := capability.PermissionSet{}
	for _, p := range a.mod.Manifest.FS.Reads {
		out.FilePaths = append(out.FilePaths, p.Path)
	}
	for _, p := range a.mod.Manifest.FS.Writes {
		out.FilePaths = append(out.FilePaths, p.Path)
	}
	return out
}

// Execute marshals the input to JSON, hands it to the guest via
// alf_alloc + Memory.Write + alf_invoke, reads the response, and
// unmarshals into capability.Output. The adapter exposes one
// method to the host — the guest decides internally whether it
// has multiple operations by inspecting the JSON payload.
//
// ABI (WASM.md §7.2, 0.8.0 simplified form):
//
//	alf_alloc(size i32) → i32         // guest pointer, 0 on OOM
//	alf_invoke(ptr i32, len i32) → i64 // (out_ptr << 32) | out_len
//
// The guest's response body is the JSON-encoded capability.Output
// — Data + Error in the same wire format the adapter consumes.
// Guest-side errors arrive via the Error field; host-side errors
// (memory failure, missing export) produce an Output with Data
// nil and Error populated.
func (a *Adapter) Execute(ctx context.Context, in capability.Input) (capability.Output, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.mod == nil || a.mod.Guest == nil {
		return capability.Output{}, fmt.Errorf("wasm: adapter has no live guest module")
	}

	payload, err := json.Marshal(in)
	if err != nil {
		return capability.Output{}, fmt.Errorf("wasm: marshal input: %w", err)
	}

	allocFn := a.mod.Guest.ExportedFunction(fnAlfAlloc)
	if allocFn == nil {
		return capability.Output{}, fmt.Errorf("wasm: guest does not export %s", fnAlfAlloc)
	}
	invokeFn := a.mod.Guest.ExportedFunction(fnAlfInvoke)
	if invokeFn == nil {
		return capability.Output{}, fmt.Errorf("wasm: guest does not export %s", fnAlfInvoke)
	}

	// 1. Allocate a buffer in guest memory for the input payload.
	allocRes, err := allocFn.Call(ctx, uint64(len(payload)))
	if err != nil {
		return capability.Output{}, fmt.Errorf("wasm: alf_alloc: %w", err)
	}
	if len(allocRes) != 1 {
		return capability.Output{}, fmt.Errorf("wasm: alf_alloc returned %d values, want 1", len(allocRes))
	}
	ptr := uint32(allocRes[0])
	if ptr == 0 && len(payload) > 0 {
		return capability.Output{}, fmt.Errorf("wasm: alf_alloc(%d) returned 0 (guest OOM)", len(payload))
	}

	// 2. Write payload into guest memory. Memory.Write is
	// bounds-checked by wazero so an out-of-range ptr returns
	// false — we surface that as a clean error rather than a
	// panic.
	if len(payload) > 0 {
		if ok := a.mod.Guest.Memory().Write(ptr, payload); !ok {
			return capability.Output{}, fmt.Errorf("wasm: Memory.Write out of range ptr=%d len=%d", ptr, len(payload))
		}
	}

	// 3. Invoke the guest. Result is packed (out_ptr << 32) | out_len.
	invokeRes, err := invokeFn.Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		return capability.Output{}, fmt.Errorf("wasm: alf_invoke: %w", err)
	}
	if len(invokeRes) != 1 {
		return capability.Output{}, fmt.Errorf("wasm: alf_invoke returned %d values, want 1", len(invokeRes))
	}
	packed := invokeRes[0]
	outPtr := uint32(packed >> 32)
	outLen := uint32(packed & 0xFFFFFFFF)

	// 4. Zero-length response is legal — empty Output is a valid
	// capability result.
	if outLen == 0 {
		return capability.Output{}, nil
	}

	// 5. Read response bytes. Memory.Read returns a slice into
	// live guest memory; we copy before unmarshal so the guest
	// can safely reuse its allocator buffers.
	raw, ok := a.mod.Guest.Memory().Read(outPtr, outLen)
	if !ok {
		return capability.Output{}, fmt.Errorf("wasm: Memory.Read out of range outPtr=%d outLen=%d", outPtr, outLen)
	}
	body := make([]byte, len(raw))
	copy(body, raw)

	var out capability.Output
	if err := json.Unmarshal(body, &out); err != nil {
		return capability.Output{}, fmt.Errorf("wasm: unmarshal output: %w (raw=%q)", err, body)
	}
	return out, nil
}

// envelopeKindToCapabilityKind bridges the envelope kind enum to the
// legacy capability.Kind. Duplicates the mapping from
// runtime.mapEnvelopeKind (itself unexported) — when the schema
// migration unifies the two enums, both copies collapse.
//
// Only two kinds flow through the 0.8.0 wasm loader: wasm-tool and
// wasm-app. Others (skill / provider / marketplace-app) are rejected
// earlier by the boot loader (step 9), which filters skills.d/wasm/
// to kinds it can host.
func envelopeKindToCapabilityKind(k envelope.ManifestKind) capability.Kind {
	switch k {
	case envelope.KindWASMApp:
		return capability.KindApp
	default:
		return capability.KindTool
	}
}
