package handle

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// HandleKind is one entry in the runtime HandleRegistry. The pair
// (Namespace, ID) uniquely identifies the kind across all installed
// providers + the daemon's bundled core kinds.
//
// #392 Stage 2 shipped (Namespace, ID). Stage 4 added ScopeFields:
// the per-kind typed-field schema the consumer's `[[depends]].scope`
// is validated against at forge time. M8 audit finding: validation
// happens Runtime-side (resolveDepends drives it from the registry),
// never inside the provider — a buggy provider implementation cannot
// accept input broader than what its manifest declared.
type HandleKind struct {
	// Namespace is either the reserved "alf" string (for the daemon's
	// bundled core kinds) or a publisher fingerprint short. The empty
	// string is rejected at Register time.
	Namespace string

	// ID is the canonical handle-kind name within the namespace.
	// Lowercase / digits / dot / hyphen — same shape as
	// providerExportIDPattern in envelope/schema.go. Empty string
	// rejected at Register time.
	ID string

	// ScopeFields is the closed list of fields a consumer's
	// `[[depends]].scope` table may carry for this kind. Empty (nil
	// or zero-length) means the kind takes no scope — depends.scope
	// must also be empty. See ScopeFieldType for the type enum.
	//
	// Stage 4 stores this on alf:* core kinds as nil (the daemon's
	// bundled handles don't go through this path; they're forged
	// directly from `[fs]` / `[events]` / `[tools]` blocks). Provider
	// exports populate this field via RegisterProviderExports.
	ScopeFields []ScopeField
}

// ScopeFieldType is the closed enum of types a scope field may carry.
// Mirrors envelope.ScopeFieldType — the package boundary keeps the
// runtime registry independent of the envelope package, but the
// values are identical so callers translating one to the other can
// rely on string equality.
type ScopeFieldType string

const (
	ScopeFieldTypeString     ScopeFieldType = "string"
	ScopeFieldTypeInt        ScopeFieldType = "int"
	ScopeFieldTypeBool       ScopeFieldType = "bool"
	ScopeFieldTypeStringList ScopeFieldType = "string-list"
	ScopeFieldTypeIntList    ScopeFieldType = "int-list"
)

// ScopeField is one entry in HandleKind.ScopeFields. Same shape as
// envelope.ScopeField (different package boundary so the registry
// doesn't depend on envelope at compile time).
type ScopeField struct {
	Name     string
	Type     ScopeFieldType
	Required bool
}

// FullName returns the manifest-syntax form "<ns>:<id>". Used by
// callers comparing against a [[depends]].handle string from a
// validated envelope.Manifest.
func (k HandleKind) FullName() string {
	return k.Namespace + ":" + k.ID
}

// AlfNamespace is the reserved namespace exposed by the daemon's
// bundled forge code. Capability providers cannot register kinds
// under this namespace — see Register's invariant check.
const AlfNamespace = "alf"

// AlfCoreHandleIDs lists the handle kinds the daemon ships under
// AlfNamespace. RegisterCore writes one HandleKind per entry into a
// fresh HandleRegistry. The list intentionally matches
// envelope.coreHandleIDs verbatim — `[[depends]].handle = "alf:<id>"`
// validation in the envelope schema and the runtime registry must
// agree on what the daemon actually ships, otherwise a manifest could
// pass schema validation and then fail registry lookup at runtime.
//
// Adding a new core kind requires three coordinated changes:
//  1. Append the id here so RegisterCore picks it up at boot.
//  2. Update envelope/schema.go's coreHandleIDs map so manifest
//     validation accepts depends.handle = "alf:<new-id>".
//  3. Update the archtest at internal/archtest/raw_imports_classification_test.go
//     so the spec pin matches.
//
// The archtest pins the envelope side; this constant is the runtime
// side. A Stage 3 follow-up may collapse both into a single source if
// the wiring stays this tightly coupled.
var AlfCoreHandleIDs = []string{
	"fs",
	"http",
	"exec",
	"secrets",
	"events.pub",
	"events.sub",
	"tool",
}

// Registry-related sentinel errors. Each Register failure maps to one
// of these so callers can pattern-match without string parsing.
var (
	ErrInvalidRegistryToken     = errors.New("handle: invalid runtime token for HandleRegistry")
	ErrRegistryEmptyNamespace   = errors.New("handle: HandleKind.Namespace is empty")
	ErrRegistryEmptyID          = errors.New("handle: HandleKind.ID is empty")
	ErrRegistryDuplicate        = errors.New("handle: HandleKind already registered")
	ErrRegistryReservedNS       = errors.New("handle: namespace \"alf\" is reserved for daemon core handles")
	ErrRegistryUnknownCoreKind  = errors.New("handle: alf-namespaced id is not in AlfCoreHandleIDs")
)

// HandleRegistry is the runtime registry of all known handle kinds.
// Populated at boot with the daemon's core kinds (under "alf:") and
// at provider-install with each provider's exports (under the
// provider's fingerprint short — Stage 3 of #392).
//
// The registry is concurrent-safe: Register and Lookup may run in
// parallel from any goroutine. Register is gated by RuntimeToken so
// only the runtime (or code holding the runtime's token) can mutate
// the registry; Lookup is unrestricted (read-only).
//
// Stage 2 ships metadata-only registration. Stage 3 will extend the
// registry with per-kind forge factories so [[depends]] resolution at
// load time produces an actual handle the guest can call.
type HandleRegistry struct {
	mu     sync.RWMutex
	byName map[string]HandleKind
}

// NewHandleRegistry returns an empty registry. The daemon's boot path
// follows up with RegisterCore to seed the alf: namespace; tests may
// register custom kinds directly via Register if they hold the runtime
// token.
func NewHandleRegistry() *HandleRegistry {
	return &HandleRegistry{
		byName: make(map[string]HandleKind),
	}
}

// Register adds k to the registry. Returns:
//   - ErrInvalidRegistryToken if tok is not the minted runtime token.
//   - ErrRegistryEmptyNamespace / ErrRegistryEmptyID for the obvious
//     malformed cases (defence in depth — schema validation should
//     catch these earlier, but the registry is the last line).
//   - ErrRegistryReservedNS if a non-core id is registered under the
//     "alf" namespace (prevents a provider from claiming a core id by
//     supplying its own RegisterCore-equivalent path).
//   - ErrRegistryDuplicate if (Namespace, ID) is already registered.
//
// The token check uses crypto/subtle constant-time compare matching
// ForgeInstance's existing pattern; both gates draw their authority
// from the same one-shot mintedToken.
func (r *HandleRegistry) Register(tok RuntimeToken, k HandleKind) error {
	if !mintedOK.Load() {
		return ErrInvalidRegistryToken
	}
	if subtle.ConstantTimeCompare(tok.key[:], mintedToken.key[:]) != 1 {
		return ErrInvalidRegistryToken
	}
	if k.Namespace == "" {
		return ErrRegistryEmptyNamespace
	}
	if k.ID == "" {
		return ErrRegistryEmptyID
	}
	if k.Namespace == AlfNamespace {
		if !isCoreHandleID(k.ID) {
			return fmt.Errorf("%w: id=%q", ErrRegistryReservedNS, k.ID)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	full := k.FullName()
	if _, exists := r.byName[full]; exists {
		return fmt.Errorf("%w: %s", ErrRegistryDuplicate, full)
	}
	r.byName[full] = k
	return nil
}

// RegisterCore is a convenience for the daemon's boot path: writes
// every entry in AlfCoreHandleIDs into the registry under the alf:
// namespace. Idempotent on a fresh registry; subsequent calls fail
// with ErrRegistryDuplicate (intentional — a second RegisterCore would
// indicate a wiring bug).
//
// Returns the first error encountered; partial state is left intact
// (Register that succeeded for "fs" is not rolled back if "http"
// fails). The expected use is as a one-shot at boot, so partial state
// is fine — the daemon panics on any RegisterCore error in practice.
func (r *HandleRegistry) RegisterCore(tok RuntimeToken) error {
	for _, id := range AlfCoreHandleIDs {
		if err := r.Register(tok, HandleKind{Namespace: AlfNamespace, ID: id}); err != nil {
			return fmt.Errorf("RegisterCore: alf:%s: %w", id, err)
		}
	}
	return nil
}

// Lookup returns the HandleKind for (ns, id) and a boolean indicating
// whether it was registered. The kind is returned by value so callers
// cannot mutate registry state via the result.
//
// Lookup is read-only, lock-protected, and concurrent-safe: many
// goroutines may call Lookup while one Register runs.
func (r *HandleRegistry) Lookup(ns, id string) (HandleKind, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.byName[ns+":"+id]
	return k, ok
}

// List returns every registered HandleKind, sorted by FullName for
// deterministic output (status surfaces, install-UX preview, audit
// snapshots). Returned slice is a fresh copy; callers may mutate it
// without affecting the registry.
func (r *HandleRegistry) List() []HandleKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]HandleKind, 0, len(r.byName))
	for _, k := range r.byName {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].FullName() < out[j].FullName()
	})
	return out
}

// Len returns the number of registered handle kinds. Useful for tests
// + boot diagnostics ("registry seeded with 7 core kinds + N provider
// exports"). Cheap — O(1) under the read lock.
func (r *HandleRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byName)
}

// isCoreHandleID returns true iff id appears in AlfCoreHandleIDs.
// Hot path: linear scan over ~7 entries is faster than a map lookup
// under typical CPU cache behaviour at this size.
func isCoreHandleID(id string) bool {
	for _, c := range AlfCoreHandleIDs {
		if c == id {
			return true
		}
	}
	return false
}
