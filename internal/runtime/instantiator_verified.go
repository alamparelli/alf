package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// ErrDependsHandleNotRegistered is returned by InstantiateVerified
// when a manifest's [[depends]] entry references a <ns>:<id> that the
// runtime HandleRegistry does not know — either the namespace is not
// `alf:` and no provider with that fingerprint short has been loaded
// yet, or the id is not in that provider's exports.
//
// The error wraps the offending handle reference; callers (loader,
// install UX) display it verbatim. Per the §3.1 ocap promise the
// guest never runs — instantiation aborts before the forge is reached.
//
// #392 Stage 3 invariant: this is the only place a depends-resolution
// failure surfaces. Schema validation at envelope.Validate accepts the
// format; the registry lookup is the runtime authority.
var ErrDependsHandleNotRegistered = errors.New("runtime: [[depends]] handle is not registered with the runtime HandleRegistry")

// #392 Stage 4 — scope validation against the schema the provider
// declared at sign time. M8 audit finding: validation runs
// Runtime-side, never inside the provider, so a buggy provider
// implementation cannot accept input broader than declared.
//
// Each error wraps the offending handle reference + field name + the
// type info. Callers (loader, install UX) display them verbatim;
// instantiation aborts before any forge work runs.
var (
	ErrDependsScopeRequiredFieldMissing = errors.New("runtime: [[depends]].scope is missing a field marked required by the provider's schema")
	ErrDependsScopeUnknownField         = errors.New("runtime: [[depends]].scope contains a field the provider's schema does not declare")
	ErrDependsScopeFieldTypeMismatch    = errors.New("runtime: [[depends]].scope field has a value whose type does not match the provider's schema")
	ErrDependsScopeNonEmptyButNoSchema  = errors.New("runtime: [[depends]].scope is non-empty but the registered handle declares no scope fields")
)

// VerifiedInstantiation is the success payload of InstantiateVerified.
// It carries the forged handle Instance plus the typed envelope
// Manifest that produced it. Downstream loaders (WASM engine, skill
// registrar, marketplace installer) read declared scope, kind, and
// version from Manifest without re-parsing the TOML — envelope.Verify
// has already validated and canonicalised those bytes once, and that
// single pass is the source of truth per §7.10.
//
// The bundle bytes themselves stay with the caller: they passed them
// in via VerifyInput.Bundle and the same slice was hashed against the
// trusted comment's bundle_sha256. Re-reading from disk between verify
// and use would break TOCTOU safety; handing the bytes back here would
// just waste memory.
type VerifiedInstantiation struct {
	Instance *handle.Instance
	Manifest *envelope.Manifest

	// SignerID is the trust-store key fingerprint that signed the
	// envelope. Surfaced so callers (loaders, audit log, future
	// status surfaces) can correlate an Instance back to the key
	// that made it trusted. RevokeByKey on the Instantiator keys
	// off this same value (#396 deliverable 3).
	SignerID envelope.KeyID

	// SignedAt is the RFC 3339 timestamp the signer embedded in the
	// trusted comment. Used by the trust-store not-valid-after
	// enforcement (#396 commit 3) to reject bundles signed after a
	// post-compromise revocation timestamp.
	SignedAt time.Time
}

// InstantiateVerified is the production load path: it takes raw in-memory
// bytes (§7.10 TOCTOU-safe — no disk re-reads between verify and use),
// calls envelope.Verify under the runtime token, and forges the handle
// Instance on success. This is the SOLE call site of envelope.Verify in
// the codebase (archtest TestOneVerifyCallSite, landing alongside).
//
// The legacy Instantiate(ctx, SignedManifest) path — which uses a
// caller-supplied capability.Manifest and an injectable TrustVerifier —
// continues to work for tests and for the migration period. Production
// daemons must call InstantiateVerified instead.
//
// baseDir is the on-disk directory the bundle was loaded from. FSHandle
// scope paths are resolved against it so manifests can use relative
// paths. If the bundle is embedded (go:embed), pass an empty string and
// the forge produces nil FSHandles (read-only contexts).
func (i *Instantiator) InstantiateVerified(ctx context.Context, in envelope.VerifyInput, baseDir string) (*VerifiedInstantiation, error) {
	vm, err := envelope.Verify(in)
	if err != nil {
		return nil, err
	}

	// #392 Stage 3 — validate every [[depends]] entry against the
	// runtime HandleRegistry. Fails closed before any forge work runs:
	// a manifest referencing an unregistered handle never sees a
	// handle.Instance and the guest cannot start. Skipped when no
	// registry is wired (test paths) or the manifest declared no
	// dependencies (the common case in 0.8.0 until provider bundles
	// land).
	if i.handleRegistry != nil && len(vm.Manifest.Depends) > 0 {
		if err := i.resolveDepends(vm.Manifest); err != nil {
			return nil, err
		}
	}

	// Map envelope.Manifest → capability.Manifest so downstream forge
	// code keeps the runtime-facing contract it has today. This shim
	// goes away when the Registry / Capability interface absorbs
	// envelope.Manifest directly (follow-up post-#391).
	capManifest := capability.Manifest{
		ID:          capability.ID(vm.Manifest.ID),
		Kind:        mapEnvelopeKind(vm.Manifest.Kind),
		Name:        vm.Manifest.Name,
		Version:     vm.Manifest.Version,
		Description: vm.Manifest.Description,
		Permissions: permissionsFromEnvelope(vm.Manifest),
	}

	signed := SignedManifest{
		Manifest: capManifest,
		BaseDir:  baseDir,
	}
	grants := i.forgeGrants(signed)

	// FS handle forging from the typed envelope: forgeGrants produced a
	// reads-only FSHandle via the legacy PermissionSet shim (which has
	// no read/write distinction). Override here so [[fs.writes]] paths
	// reach the handle's write scope. Audit doc D1 — see
	// technical/AUDIT-3-TIERS-2026-04-26.md.
	if len(vm.Manifest.FS.Reads) > 0 || len(vm.Manifest.FS.Writes) > 0 {
		grants.FS = handle.NewFSHandle(capManifest.ID, baseDir, handle.FSScope{
			Reads:  envFSPaths(vm.Manifest.FS.Reads),
			Writes: envFSPaths(vm.Manifest.FS.Writes),
		})
	}

	// Events block forging (#399). Only runs when bus + cross-flow
	// registry are wired; tests and legacy paths skip this entirely.
	// EventPub is forged unconditionally for any capability that
	// declared events.exports — the topics are baked into the handle's
	// scope so a publish on an undeclared topic returns ErrTopicNotExported.
	// EventSub is forged only when the cross-flow registry confirms
	// the cited publisher is installed AND exports the topic.
	if i.bus != nil && i.subscribe != nil && i.crossFlow != nil {
		if len(vm.Manifest.Events.Exports) > 0 {
			topics := make([]string, 0, len(vm.Manifest.Events.Exports))
			for _, e := range vm.Manifest.Events.Exports {
				topics = append(topics, e.Topic)
			}
			grants.EventPub = handle.NewEventPub(capManifest.ID, topics, i.bus)
		}
		for _, s := range vm.Manifest.Events.Subscribes {
			fromID := capability.ID(s.From)
			if !i.crossFlow.HasExport(fromID, s.Topic) {
				// §3.3 private-by-default: no handle, no method, no leak.
				// The loader logs this as an unresolved cross-flow.
				continue
			}
			q, cleanup, err := i.subscribe.Subscribe(capManifest.ID, fromID, s.Topic)
			if err != nil {
				continue
			}
			grants.EventSubs = append(grants.EventSubs, handle.NewEventSub(
				capManifest.ID, fromID, s.Topic, q, cleanup,
			))
		}
	}

	// Tools block forging (#389). Same shape as the events branch:
	// only runs when an invoker is wired so the legacy Instantiate
	// path and tests that don't exercise the tool surface stay
	// untouched. Manifests that declare zero tools yield a nil
	// ToolHandle — the cap has no inter-capability invocation surface.
	if i.invoker != nil && len(vm.Manifest.Tools.Declares) > 0 {
		allowed := make([]capability.ID, 0, len(vm.Manifest.Tools.Declares))
		for _, d := range vm.Manifest.Tools.Declares {
			allowed = append(allowed, capability.ID(d.ID))
		}
		grants.Tool = handle.NewToolHandle(capManifest.ID, handle.ToolScope{Allowed: allowed}, i.invoker)
	}

	inst, err := handle.ForgeInstance(i.token, ctx, capManifest.ID, grants)
	if err != nil {
		return nil, err
	}

	// Track in the live registry so RevokeByKey can find this Instance
	// later (#396 deliverable 3). The watcher goroutine inside
	// trackLive prunes the entry when Close() fires the lifecycle ctx,
	// regardless of whether Close came from the user, RevokeByKey, or
	// the eventual provider cascade (#392 follow-up).
	i.trackLive(inst, vm.SignerID, vm.SignedAt)

	// #392 Stage 3 — capability-provider bundles register their
	// [[provider.exports]] under the publisher's fingerprint short.
	// Runs AFTER successful instantiation so a forge failure can't
	// pollute the registry; runs BEFORE returning so a downstream
	// consumer loaded immediately after sees the exports.
	//
	// A duplicate registration (same provider re-loaded across a
	// SIGHUP, or a re-load by a hot-reload path) is a wiring bug —
	// surface it as an instantiation failure so the operator notices.
	// Stage 5 will add a proper uninstall path that removes registry
	// entries before re-registration.
	if i.handleRegistry != nil &&
		vm.Manifest.Kind == envelope.KindCapabilityProvider &&
		len(vm.Manifest.Provider.Exports) > 0 {
		if err := i.RegisterProviderExports(i.handleRegistry, vm.SignerID, vm.Manifest.Provider.Exports); err != nil {
			return nil, fmt.Errorf("capability-provider %q: %w", vm.Manifest.ID, err)
		}
	}

	return &VerifiedInstantiation{
		Instance: inst,
		Manifest: vm.Manifest,
		SignerID: vm.SignerID,
		SignedAt: vm.SignedAt,
	}, nil
}

// resolveDepends checks every [[depends]] entry in m against the
// HandleRegistry: the handle must be registered AND the consumer's
// scope table must conform to the registered ScopeFields schema.
// Returns the wrapped sentinel error on the first miss; the manifest
// must declare every dependency by reference, the registry must
// agree the handle exists, the scope must validate, and any
// divergence aborts before the forge runs.
//
// Pre-condition: every DependsEntry came from envelope.Validate so
// SplitHandle returns two well-formed parts. Scope is opaque
// `map[string]any` at the envelope side; resolveDepends drives the
// type checks here using the registered schema.
func (i *Instantiator) resolveDepends(m *envelope.Manifest) error {
	for idx, d := range m.Depends {
		ns, id := d.SplitHandle()
		k, ok := i.handleRegistry.Lookup(ns, id)
		if !ok {
			return fmt.Errorf("%w: depends[%d].handle=%q", ErrDependsHandleNotRegistered, idx, d.Handle)
		}
		if err := validateScopeAgainstSchema(d.Scope, k.ScopeFields); err != nil {
			return fmt.Errorf("depends[%d].handle=%q: %w", idx, d.Handle, err)
		}
	}
	return nil
}

// validateScopeAgainstSchema enforces the scope schema invariants
// declared by the provider at sign time:
//   - every required field is present in scope
//   - every scope key matches a declared field name
//   - every scope value's type matches the declared field type
//
// A nil schema with a non-empty scope is rejected (the consumer is
// passing data the provider's interface does not accept) — the spec
// equivalent of "no parameters" at the function-signature level.
//
// This is the M8 audit finding's runtime hook: the provider
// implementation never sees scope until validation passes, so a
// buggy provider that "forgets" to validate cannot accept broader
// input than the manifest declared.
func validateScopeAgainstSchema(scope map[string]any, schema []handle.ScopeField) error {
	if len(scope) == 0 && len(schema) == 0 {
		return nil
	}
	if len(schema) == 0 && len(scope) > 0 {
		return ErrDependsScopeNonEmptyButNoSchema
	}

	// Build a name → field index for O(1) lookup; also use it to
	// detect "unknown scope keys" by tracking which schema entries
	// were matched.
	byName := make(map[string]handle.ScopeField, len(schema))
	for _, f := range schema {
		byName[f.Name] = f
	}

	// Required fields present.
	for _, f := range schema {
		if !f.Required {
			continue
		}
		if _, ok := scope[f.Name]; !ok {
			return fmt.Errorf("%w: field=%q type=%q", ErrDependsScopeRequiredFieldMissing, f.Name, f.Type)
		}
	}

	// Every scope key has a matching field with matching type.
	for k, v := range scope {
		f, ok := byName[k]
		if !ok {
			return fmt.Errorf("%w: field=%q", ErrDependsScopeUnknownField, k)
		}
		if err := checkScopeValueType(v, f.Type); err != nil {
			return fmt.Errorf("%w: field=%q expected_type=%q: %v", ErrDependsScopeFieldTypeMismatch, k, f.Type, err)
		}
	}
	return nil
}

// checkScopeValueType returns nil iff value matches the declared type.
// TOML's pelletier/go-toml/v2 maps int → int64, float → float64, etc.
// Lists are decoded as []any; we descend per-element for *-list types.
func checkScopeValueType(value any, t handle.ScopeFieldType) error {
	switch t {
	case handle.ScopeFieldTypeString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("got %T", value)
		}
	case handle.ScopeFieldTypeInt:
		// TOML decoder uses int64 for whole numbers.
		switch value.(type) {
		case int64, int, int32:
			return nil
		default:
			return fmt.Errorf("got %T", value)
		}
	case handle.ScopeFieldTypeBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("got %T", value)
		}
	case handle.ScopeFieldTypeStringList:
		list, ok := value.([]any)
		if !ok {
			return fmt.Errorf("got %T (want list)", value)
		}
		for i, e := range list {
			if _, ok := e.(string); !ok {
				return fmt.Errorf("item %d: got %T (want string)", i, e)
			}
		}
	case handle.ScopeFieldTypeIntList:
		list, ok := value.([]any)
		if !ok {
			return fmt.Errorf("got %T (want list)", value)
		}
		for i, e := range list {
			switch e.(type) {
			case int64, int, int32:
				continue
			default:
				return fmt.Errorf("item %d: got %T (want int)", i, e)
			}
		}
	default:
		return fmt.Errorf("unknown scope-field type %q (handle.ScopeFieldType enum drift?)", t)
	}
	return nil
}

// mapEnvelopeKind bridges the envelope.Manifest kind enum (string-typed
// per MANIFEST-SCHEMA §3.3) to the legacy capability.Kind enum (int
// iota from the v0.7.10 rework). Unknown values default to
// capability.KindTool — the envelope schema validator has already
// rejected unknowns, so this path is only reached for the 6 known kinds.
//
// Both provider sub-kinds (LLM + capability) map to KindTool because the
// legacy capability.Kind enum has no provider concept; #392 follow-ups
// (forge integration in Stage 3) consume envelope.Manifest directly and
// don't go through this shim.
func mapEnvelopeKind(k envelope.ManifestKind) capability.Kind {
	switch k {
	case envelope.KindWASMTool, envelope.KindSkill, envelope.KindLLMProvider, envelope.KindCapabilityProvider:
		return capability.KindTool
	case envelope.KindWASMApp:
		return capability.KindApp
	case envelope.KindMarketplaceApp:
		return capability.KindApp
	default:
		return capability.KindTool
	}
}

// permissionsFromEnvelope builds the legacy capability.PermissionSet
// from the envelope.Manifest's typed blocks. The FS branch is empty
// here — FS handle forging happens in InstantiateVerified using the
// typed envelope directly (see audit D1 fix), because the legacy
// PermissionSet has no read/write distinction. Other blocks
// (http/exec/secrets) are parse-time errors in the schema today, so
// they're always empty.
func permissionsFromEnvelope(_ *envelope.Manifest) capability.PermissionSet {
	return capability.PermissionSet{}
}

// envFSPaths flattens an envelope.FSPath slice to plain strings. Used
// by InstantiateVerified to feed handle.FSScope without going through
// the legacy PermissionSet shim.
func envFSPaths(in []envelope.FSPath) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, p := range in {
		out = append(out, p.Path)
	}
	return out
}

