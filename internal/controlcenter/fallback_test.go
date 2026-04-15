package controlcenter

import "testing"

func TestDefaultFallbackModel_NilConfig(t *testing.T) {
	if m := DefaultFallbackModel(nil); m != "" {
		t.Errorf("expected empty on nil config, got %q", m)
	}
}

func TestDefaultFallbackModel_EmptyTiers(t *testing.T) {
	if m := DefaultFallbackModel(&TiersConfig{}); m != "" {
		t.Errorf("expected empty with no tiers, got %q", m)
	}
}

func TestDefaultFallbackModel_NamedFallback(t *testing.T) {
	tc := &TiersConfig{
		DefaultFallback: "fast",
		Tiers: []Tier{
			{Name: "fast", Model: "mistral-small", Enabled: true, Routable: true, Priority: 1},
			{Name: "heavy", Model: "gpt-5", Enabled: true, Routable: true, Priority: 0},
		},
	}
	if m := DefaultFallbackModel(tc); m != "mistral-small" {
		t.Errorf("expected named fallback 'mistral-small', got %q", m)
	}
}

func TestDefaultFallbackModel_NamedFallbackDisabled(t *testing.T) {
	// Named fallback exists but is disabled → fall through to next rule.
	tc := &TiersConfig{
		DefaultFallback: "fast",
		Tiers: []Tier{
			{Name: "fast", Model: "haiku", Enabled: false, Routable: true, Priority: 0},
			{Name: "heavy", Model: "sonnet", Enabled: true, Routable: true, Priority: 1},
		},
	}
	if m := DefaultFallbackModel(tc); m != "claude-sonnet-4-6" {
		t.Errorf("expected lowest-priority enabled fallback, got %q", m)
	}
}

func TestDefaultFallbackModel_LowestPriorityEnabled(t *testing.T) {
	tc := &TiersConfig{
		Tiers: []Tier{
			{Name: "heavy", Model: "opus", Enabled: true, Routable: true, Priority: 10},
			{Name: "cheap", Model: "haiku", Enabled: true, Routable: true, Priority: 1},
			{Name: "mid", Model: "sonnet", Enabled: true, Routable: true, Priority: 5},
		},
	}
	if m := DefaultFallbackModel(tc); m != "claude-haiku-4-5" {
		t.Errorf("expected 'haiku' via alias, got %q", m)
	}
}

func TestDefaultFallbackModel_NonRoutableSkipped(t *testing.T) {
	// Only non-routable enabled tiers → falls to rule 3 (any enabled).
	tc := &TiersConfig{
		Tiers: []Tier{
			{Name: "tool", Model: "gpt-5-mini", Enabled: true, Routable: false, Priority: 0},
		},
	}
	if m := DefaultFallbackModel(tc); m != "gpt-5-mini" {
		t.Errorf("expected any-enabled fallback, got %q", m)
	}
}

func TestDefaultFallbackModel_NoBackendBaked(t *testing.T) {
	// Regression guard: the resolved model must come from the user's
	// configured tier, not a hardcoded claude-* value.
	tc := &TiersConfig{
		Tiers: []Tier{
			{Name: "only", Model: "gemini-2.0-flash", Enabled: true, Routable: true, Priority: 0},
		},
	}
	if m := DefaultFallbackModel(tc); m != "gemini-2.0-flash" {
		t.Errorf("expected user-configured model, got %q", m)
	}
}

func TestDefaultFallbackTier_ReturnsBackend(t *testing.T) {
	tc := &TiersConfig{
		DefaultFallback: "fast",
		Tiers: []Tier{
			{Name: "fast", Model: "gpt-5-mini", Backend: "codex", Enabled: true, Routable: true, Priority: 0},
		},
	}
	model, backend, ok := DefaultFallbackTier(tc)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if model != "gpt-5-mini" || backend != "codex" {
		t.Errorf("model=%q backend=%q, want gpt-5-mini/codex", model, backend)
	}
}

func TestResolveModelAlias_Aliases(t *testing.T) {
	cases := map[string]string{
		"haiku":      "claude-haiku-4-5",
		"sonnet":     "claude-sonnet-4-6",
		"opus":       "claude-opus-4-6",
		"sonnet-max": "claude-sonnet-4-6-max",
		"opus-max":   "claude-opus-4-6-max",
		"HAIKU":      "claude-haiku-4-5",
		"claude-custom-model": "claude-custom-model",
		"gpt-5":      "",
	}
	for in, want := range cases {
		if got := resolveModelAlias(in); got != want {
			t.Errorf("resolveModelAlias(%q) = %q, want %q", in, got, want)
		}
	}
}
