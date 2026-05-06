package skills

// NarrowToolsByDeclares is the §3.1 "tools outside declares are
// invisible to the LLM" producer (#389 Stage 2). It takes the
// tier-configured tool list and the session's active skills, and
// returns the intersection of:
//
//   - tierTools (what the tier configuration enables)
//   - the union of [[tools.declares]] ids across the active skills
//     that ship a manifest.toml (what the active skills authorise)
//
// Sibling to runtime.BuildScopedToolSpecs which does the same job for
// the runtime.Chat path; this helper is the pipeline.ChatEngine path's
// equivalent. They could converge once the legacy MirrorInto +
// skillCapability dies (Stage-2 follow-up).
//
// Lookup contract:
//
//   - lookup == nil  → legacy behaviour (return tierTools unchanged).
//     Wired this way so the helper is safe to call before
//     ChatEngine.SkillDeclaresLookup is set.
//
//   - lookup(name) == nil or empty → that skill is YAML-only (no
//     manifest.toml shipped yet). It does NOT narrow the surface;
//     tools coming from the tier config remain visible. The strictest
//     interpretation is to disallow tools that no active skill
//     declares, but that breaks every YAML-only skill currently
//     loaded — this gentler rule is the §389 Stage 2 transition
//     compromise.
//
//   - lookup(name) returns a non-empty slice → the active skills
//     have at least one manifest-declared declares block; the LLM-
//     visible tool surface is narrowed to the intersection.
//
// Returned slice preserves tierTools' order so any provider that
// cares about deterministic ordering keeps that property.
func NarrowToolsByDeclares(lookup func(skillName string) []string, activeSkills, tierTools []string) []string {
	if lookup == nil || len(activeSkills) == 0 || len(tierTools) == 0 {
		return tierTools
	}
	declared := make(map[string]struct{})
	anyDeclares := false
	for _, name := range activeSkills {
		ids := lookup(name)
		if len(ids) == 0 {
			continue
		}
		anyDeclares = true
		for _, id := range ids {
			declared[id] = struct{}{}
		}
	}
	if !anyDeclares {
		// Every active skill is YAML-only. Don't narrow — the cap
		// surface tracks what the tier config offered.
		return tierTools
	}
	out := make([]string, 0, len(tierTools))
	for _, t := range tierTools {
		if _, ok := declared[t]; ok {
			out = append(out, t)
		}
	}
	return out
}

// DeclaresFromVerified flattens a VerifiedSkill's manifest tools.declares
// into a []string of ids. Convenience for daemon wiring that wants to
// build a name → []string lookup table from the loader's output.
//
// Returns nil for a VerifiedSkill without a Manifest (defensive — the
// loader always populates Manifest on success, but a nil-receiver check
// keeps wiring code from panicking on an exotic future code path).
func DeclaresFromVerified(vs *VerifiedSkill) []string {
	if vs == nil || vs.Manifest == nil {
		return nil
	}
	if len(vs.Manifest.Tools.Declares) == 0 {
		return nil
	}
	out := make([]string, 0, len(vs.Manifest.Tools.Declares))
	for _, d := range vs.Manifest.Tools.Declares {
		out = append(out, d.ID)
	}
	return out
}
