package main

import (
	"context"
	"fmt"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
	"github.com/alamparelli/alf/internal/runtime"
	"github.com/alamparelli/alf/internal/skills"
)

// skillsRuntime bundles the daemon-owned verified-skill lifecycle.
// It exposes the slice of currently-loaded VerifiedSkills so the
// shutdown path can revoke their handle Instances, and so a hot-reload
// can swap them in-place via Replace().
//
// The legacy YAML-only skillStore + skills.MirrorInto path coexists
// during the transition window (#389 Stage 1): a skill without a
// manifest.toml continues to load via the legacy path, just without
// a forged Instance. Once every shipped + user skill has migrated,
// MirrorInto + skillCapability go away in a follow-up.
type skillsRuntime struct {
	verified []*skills.VerifiedSkill
}

// Close revokes every currently-loaded verified skill's Instance.
// Safe on nil receiver so main() can defer unconditionally.
func (s *skillsRuntime) Close() {
	if s == nil {
		return
	}
	skills.CloseAll(s.verified)
	s.verified = nil
}

// Replace closes the previous batch's Instances and adopts a new one.
// Used by the Control Center reload path so a manifest edit takes
// effect immediately and old handles cannot leak.
func (s *skillsRuntime) Replace(next []*skills.VerifiedSkill) {
	if s == nil {
		return
	}
	skills.CloseAll(s.verified)
	s.verified = next
}

// DeclaresLookup matches the §3.1 active-skill boundary contract
// (#389 Stage 2). Returns the [[tools.declares]] id list for a
// skill identified by its SKILL.md / manifest name, or nil if no
// loaded skill matches the name (e.g. a YAML-only legacy skill, or
// a typo in the session's active-skills set).
//
// The lookup walks s.verified linearly. The list is small (5 shipped
// + a handful of operator skills); a map index would not pay back
// the bookkeeping cost for the load + Replace paths.
//
// Safe on nil receiver — returns nil. Used by ChatEngine via
// SetSkillDeclaresLookup; the engine treats nil result as "no
// declares from this skill" (legacy-permissive semantics).
func (s *skillsRuntime) DeclaresLookup(name string) []string {
	if s == nil {
		return nil
	}
	for _, vs := range s.verified {
		if vs == nil || vs.Skill == nil {
			continue
		}
		if vs.Skill.Name == name {
			return skills.DeclaresFromVerified(vs)
		}
	}
	return nil
}

// setupSkillsLoader walks every directory in skillDirs, prepares each
// signed bundle (manifest.toml + SKILL.md + optional manifest.sig),
// drives it through the shared Instantiator, and returns the list of
// verified skills. It reuses the daemon's already-minted runtime token
// (§4.3 forge gate) by routing through wasmRt.Inst — minting a second
// token would panic. It reuses the daemon key + trust store from
// wasmRt for the same reason: §7.3 Tier 2 is process-wide.
//
// Per-skill failures are accumulated and logged — they never abort
// the boot sequence, mirroring the WASM loader's behaviour. Skills
// without a manifest.toml are silently skipped (legacy path picks
// them up via skills.MirrorInto / parseSkill).
func setupSkillsLoader(
	ctx context.Context,
	skillDirs []string,
	wasmRt *wasmRuntime,
	logf func(string, ...any),
) (*skillsRuntime, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if wasmRt == nil || wasmRt.Inst == nil {
		return nil, fmt.Errorf("skills-loader: wasmRt must be initialised first (shared Instantiator + daemon key)")
	}
	if len(skillDirs) == 0 {
		return &skillsRuntime{}, nil
	}

	instantiate := newSkillInstantiateFn(wasmRt.Inst)
	autoSign := skills.NewDaemonAutoSigner(wasmRt.DaemonPriv, nil)

	verified, errs := skills.LoadDir(ctx, skills.LoadOptions{
		Dirs:        skillDirs,
		TrustStore:  wasmRt.TrustStore,
		AutoSign:    autoSign,
		Instantiate: instantiate,
		Logger:      logf,
	})

	logf("[skills-loader] scanned %d dirs: %d skills loaded, %d errors", len(skillDirs), len(verified), len(errs))
	for _, e := range errs {
		logf("[skills-loader] error: %v", e)
	}

	return &skillsRuntime{verified: verified}, nil
}

// newSkillInstantiateFn adapts runtime.Instantiator to the skills
// package's narrow callback interface — that package cannot import
// runtime/ (the dependency is the wrong direction; runtime imports
// skills via SkillStore).
func newSkillInstantiateFn(inst *runtime.Instantiator) skills.InstantiateFn {
	return func(ctx context.Context, in envelope.VerifyInput, baseDir string) (*handle.Instance, *envelope.Manifest, error) {
		vi, err := inst.InstantiateVerified(ctx, in, baseDir)
		if err != nil {
			return nil, nil, err
		}
		return vi.Instance, vi.Manifest, nil
	}
}
