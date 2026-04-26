package runtime

import (
	"context"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
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
	return &VerifiedInstantiation{
		Instance: inst,
		Manifest: vm.Manifest,
	}, nil
}

// mapEnvelopeKind bridges the envelope.Manifest kind enum (string-typed
// per MANIFEST-SCHEMA §3.3) to the legacy capability.Kind enum (int
// iota from the v0.7.10 rework). Unknown values default to
// capability.KindTool — the envelope schema validator has already
// rejected unknowns, so this path is only reached for the 5 known kinds.
func mapEnvelopeKind(k envelope.ManifestKind) capability.Kind {
	switch k {
	case envelope.KindWASMTool, envelope.KindSkill, envelope.KindProvider:
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

