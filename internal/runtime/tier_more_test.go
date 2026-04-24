package comms

import "testing"

func TestIsOrchestratorTier(t *testing.T) {
	snap := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Role: ""},
			{Name: "agent", Role: "orchestrator"},
		},
	}
	if !snap.IsOrchestratorTier("agent") {
		t.Error("expected 'agent' to be orchestrator")
	}
	if snap.IsOrchestratorTier("fast") {
		t.Error("expected 'fast' to not be orchestrator")
	}
	if snap.IsOrchestratorTier("missing") {
		t.Error("expected unknown tier to return false")
	}
}

func TestDefaultFallbackModel_NamedFallback(t *testing.T) {
	snap := TiersSnapshot{
		DefaultFallback: "smart",
		Tiers: []TierInfo{
			{Name: "fast", Model: "haiku", Enabled: true},
			{Name: "smart", Model: "sonnet", Enabled: true},
		},
	}
	if got := DefaultFallbackModel(snap, nil); got != "sonnet" {
		t.Errorf("expected sonnet, got %q", got)
	}
}

func TestDefaultFallbackModel_NamedFallbackDisabled(t *testing.T) {
	// Named fallback is disabled → should fall through to lowest-priority enabled+routable.
	snap := TiersSnapshot{
		DefaultFallback: "disabled",
		Tiers: []TierInfo{
			{Name: "disabled", Model: "old-model", Enabled: false},
			{Name: "fast", Model: "haiku", Priority: 1, Enabled: true, Routable: true},
			{Name: "smart", Model: "sonnet", Priority: 2, Enabled: true, Routable: true},
		},
	}
	if got := DefaultFallbackModel(snap, nil); got != "haiku" {
		t.Errorf("expected lowest-priority routable (haiku), got %q", got)
	}
}

func TestDefaultFallbackModel_LowestPriorityRoutable(t *testing.T) {
	snap := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "smart", Model: "sonnet", Priority: 5, Enabled: true, Routable: true},
			{Name: "fast", Model: "haiku", Priority: 1, Enabled: true, Routable: true},
		},
	}
	if got := DefaultFallbackModel(snap, nil); got != "haiku" {
		t.Errorf("expected haiku (lowest priority), got %q", got)
	}
}

func TestDefaultFallbackModel_NoRoutableFallsBackToEnabled(t *testing.T) {
	snap := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "only", Model: "solo", Enabled: true, Routable: false, ForceCommand: true},
		},
	}
	if got := DefaultFallbackModel(snap, nil); got != "solo" {
		t.Errorf("expected 'solo' (any enabled), got %q", got)
	}
}

func TestDefaultFallbackModel_Empty(t *testing.T) {
	snap := TiersSnapshot{
		Tiers: []TierInfo{{Name: "t", Enabled: false}},
	}
	if got := DefaultFallbackModel(snap, nil); got != "" {
		t.Errorf("expected empty when no enabled tier, got %q", got)
	}
}

func TestDefaultFallbackModel_ResolverExpandsAliases(t *testing.T) {
	snap := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Model: "haiku", Priority: 1, Enabled: true, Routable: true},
		},
	}
	resolver := func(m string) string {
		if m == "haiku" {
			return "claude-haiku-4-5-20251001"
		}
		return ""
	}
	if got := DefaultFallbackModel(snap, resolver); got != "claude-haiku-4-5-20251001" {
		t.Errorf("resolver not applied: got %q", got)
	}
}

func TestDefaultFallbackModel_ResolverEmptyFallsBack(t *testing.T) {
	// Resolver returns empty → must fall back to raw alias, not empty string.
	snap := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Model: "haiku", Priority: 1, Enabled: true, Routable: true},
		},
	}
	resolver := func(m string) string { return "" }
	if got := DefaultFallbackModel(snap, resolver); got != "haiku" {
		t.Errorf("expected raw model fallback when resolver returns empty, got %q", got)
	}
}
