package controlcenter

import "github.com/alamparelli/alf/internal/ai"

// DefaultFallbackModel resolves the user's configured fallback model without
// hardcoding any provider-specific value. Resolution order:
//  1. the tier named by tiers.DefaultFallback (if enabled)
//  2. the lowest-priority enabled+routable tier
//  3. any enabled tier
// Returns "" if nothing is resolvable — callers MUST handle the empty case
// rather than silently substituting a baked-in model (see #194, #291).
func DefaultFallbackModel(tiers *TiersConfig) string {
	model, _, _ := DefaultFallbackTier(tiers)
	return model
}

// DefaultFallbackTier returns (model, backend, ok) for the fallback tier,
// so callers that need both pieces (e.g. to dispatch to the right backend)
// don't have to walk the tier list twice. Uses the same resolution order
// as DefaultFallbackModel.
func DefaultFallbackTier(tiers *TiersConfig) (model, backend string, ok bool) {
	if tiers == nil {
		return "", "", false
	}
	resolve := func(raw string) string {
		if m := ai.ResolveModel(raw); m != "" {
			return string(m)
		}
		return raw
	}
	// 1. Named default_fallback.
	if name := tiers.DefaultFallback; name != "" {
		for _, t := range tiers.Tiers {
			if t.Name == name && t.Enabled {
				return resolve(t.Model), t.Backend, true
			}
		}
	}
	// 2. Lowest-priority enabled+routable tier.
	bestIdx := -1
	bestPriority := int(^uint(0) >> 1)
	for i, t := range tiers.Tiers {
		if !t.Enabled || !t.Routable {
			continue
		}
		if t.Priority < bestPriority {
			bestPriority = t.Priority
			bestIdx = i
		}
	}
	if bestIdx >= 0 {
		t := tiers.Tiers[bestIdx]
		return resolve(t.Model), t.Backend, true
	}
	// 3. Any enabled tier.
	for _, t := range tiers.Tiers {
		if t.Enabled {
			return resolve(t.Model), t.Backend, true
		}
	}
	return "", "", false
}

