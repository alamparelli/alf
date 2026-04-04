package comms

import (
	"testing"
)

// mockTierStore implements TierStoreReader for testing.
type mockTierStore struct {
	snap TiersSnapshot
}

func (m *mockTierStore) Snapshot() TiersSnapshot { return m.snap }

func TestResolveTierParams(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Model: "haiku", Priority: 1, Enabled: true},
			{Name: "smart", Model: "sonnet", Priority: 2, Enabled: true, Effort: "high"},
		},
	}

	tp, found := ResolveTierParams("fast", tiers, "/data", nil, nil, nil)
	if !found {
		t.Fatal("expected to find tier 'fast'")
	}
	if tp.Model == "" {
		t.Error("expected non-empty model")
	}

	tp, found = ResolveTierParams("smart", tiers, "/data", nil, nil, nil)
	if !found {
		t.Fatal("expected to find tier 'smart'")
	}
	if tp.Effort != "high" {
		t.Errorf("expected effort='high', got %q", tp.Effort)
	}
}

func TestResolveTierParams_Unknown(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Model: "haiku", Priority: 1, Enabled: true},
		},
	}

	tp, found := ResolveTierParams("nonexistent", tiers, "/data", nil, nil, nil)
	if found {
		t.Error("expected found=false for unknown tier")
	}
	if tp.Model != "claude-haiku-4-5" {
		t.Errorf("expected fallback model, got %q", tp.Model)
	}
}

func TestFirstFallbackTier(t *testing.T) {
	store := &mockTierStore{snap: TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "disabled", Priority: 1, Enabled: false},
			{Name: "fast", Priority: 2, Enabled: true},
			{Name: "smart", Priority: 3, Enabled: true},
		},
	}}

	if got := FirstFallbackTier(store); got != "fast" {
		t.Errorf("FirstFallbackTier() = %q, want %q", got, "fast")
	}
}

func TestFirstFallbackTier_DefaultFallback(t *testing.T) {
	store := &mockTierStore{snap: TiersSnapshot{
		DefaultFallback: "smart",
		Tiers: []TierInfo{
			{Name: "fast", Priority: 1, Enabled: true},
			{Name: "smart", Priority: 2, Enabled: true},
		},
	}}

	if got := FirstFallbackTier(store); got != "smart" {
		t.Errorf("FirstFallbackTier() = %q, want %q", got, "smart")
	}
}

func TestFirstFallbackTier_NoneEnabled(t *testing.T) {
	store := &mockTierStore{snap: TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Priority: 1, Enabled: false},
		},
	}}

	if got := FirstFallbackTier(store); got != "fast" {
		t.Errorf("FirstFallbackTier() = %q, want %q (first tier overall)", got, "fast")
	}
}

func TestFirstFallbackTier_Empty(t *testing.T) {
	store := &mockTierStore{snap: TiersSnapshot{}}

	if got := FirstFallbackTier(store); got != "" {
		t.Errorf("FirstFallbackTier() = %q, want empty", got)
	}
}

func TestOnboardingTier(t *testing.T) {
	store := &mockTierStore{snap: TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Priority: 1, Enabled: true},
			{Name: "smart", Priority: 2, Enabled: true},
			{Name: "opus", Priority: 3, Enabled: true},
		},
	}}

	if got := OnboardingTier(store); got != "smart" {
		t.Errorf("OnboardingTier() = %q, want %q", got, "smart")
	}
}

func TestOnboardingTier_SkipsAgent(t *testing.T) {
	store := &mockTierStore{snap: TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Priority: 1, Enabled: true},
			{Name: "agent", Priority: 2, Enabled: true},
			{Name: "smart", Priority: 3, Enabled: true},
		},
	}}

	if got := OnboardingTier(store); got != "smart" {
		t.Errorf("OnboardingTier() = %q, want %q (agent should be skipped)", got, "smart")
	}
}

func TestOnboardingTier_SingleTier(t *testing.T) {
	store := &mockTierStore{snap: TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Priority: 1, Enabled: true},
		},
	}}

	if got := OnboardingTier(store); got != "fast" {
		t.Errorf("OnboardingTier() = %q, want %q", got, "fast")
	}
}

func TestLowestMediaTier(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Priority: 1, Enabled: true, Tools: []string{"Bash"}},
			{Name: "smart", Priority: 2, Enabled: true, Tools: []string{"Read", "Bash"}},
			{Name: "opus", Priority: 3, Enabled: true, WriteCapable: true},
		},
	}

	if got := LowestMediaTier(tiers); got != "smart" {
		t.Errorf("LowestMediaTier() = %q, want %q", got, "smart")
	}
}

func TestLowestMediaTier_WriteCapable(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Priority: 1, Enabled: true, WriteCapable: true},
			{Name: "smart", Priority: 2, Enabled: true, Tools: []string{"Read"}},
		},
	}

	if got := LowestMediaTier(tiers); got != "fast" {
		t.Errorf("LowestMediaTier() = %q, want %q", got, "fast")
	}
}

func TestLowestMediaTier_NoReadTier(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Priority: 1, Enabled: true, Tools: []string{"Bash"}},
		},
	}

	if got := LowestMediaTier(tiers); got != "fast" {
		t.Errorf("LowestMediaTier() = %q, want %q", got, "fast")
	}
}

func TestIsTierValid(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "fast", Enabled: true, Routable: true},
			{Name: "disabled", Enabled: false, Routable: true},
			{Name: "force", Enabled: true, Routable: false, ForceCommand: true},
		},
	}

	if !IsTierValid("fast", tiers) {
		t.Error("expected 'fast' to be valid")
	}
	if IsTierValid("disabled", tiers) {
		t.Error("expected 'disabled' to be invalid")
	}
	if !IsTierValid("force", tiers) {
		t.Error("expected 'force' to be valid (ForceCommand)")
	}
	if IsTierValid("nonexistent", tiers) {
		t.Error("expected 'nonexistent' to be invalid")
	}
}

func TestResolveFallbackChain_Basic(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "haiku", Enabled: true, Fallback: "sonnet"},
			{Name: "sonnet", Enabled: true, Fallback: "opus"},
			{Name: "opus", Enabled: true},
		},
	}

	chain := ResolveFallbackChain("haiku", tiers)
	if len(chain) != 2 || chain[0] != "sonnet" || chain[1] != "opus" {
		t.Errorf("expected [sonnet opus], got %v", chain)
	}
}

func TestResolveFallbackChain_CycleDetection(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "a", Enabled: true, Fallback: "b"},
			{Name: "b", Enabled: true, Fallback: "a"},
		},
	}

	chain := ResolveFallbackChain("a", tiers)
	if len(chain) != 1 || chain[0] != "b" {
		t.Errorf("expected [b] (cycle stopped), got %v", chain)
	}
}

func TestResolveFallbackChain_NoFallback(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "solo", Enabled: true},
		},
	}

	chain := ResolveFallbackChain("solo", tiers)
	if len(chain) != 0 {
		t.Errorf("expected empty chain, got %v", chain)
	}
}

func TestResolveFallbackChain_MissingTarget(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "haiku", Enabled: true, Fallback: "nonexistent"},
		},
	}

	// "nonexistent" is not in tiers, so after haiku we can't find it → chain stops
	chain := ResolveFallbackChain("haiku", tiers)
	// The function adds "nonexistent" to chain since it's a valid fallback name,
	// but then can't resolve its next fallback.
	if len(chain) != 1 || chain[0] != "nonexistent" {
		t.Errorf("expected [nonexistent], got %v", chain)
	}
}

func TestResolveFallbackChain_SelfReference(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "self", Enabled: true, Fallback: "self"},
		},
	}

	chain := ResolveFallbackChain("self", tiers)
	if len(chain) != 0 {
		t.Errorf("expected empty chain (self-cycle), got %v", chain)
	}
}

func TestResolveFallbackChain_LongChain(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "t1", Fallback: "t2"},
			{Name: "t2", Fallback: "t3"},
			{Name: "t3", Fallback: "t4"},
			{Name: "t4", Fallback: "t5"},
			{Name: "t5"},
		},
	}

	chain := ResolveFallbackChain("t1", tiers)
	expected := []string{"t2", "t3", "t4", "t5"}
	if len(chain) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, chain)
	}
	for i, name := range expected {
		if chain[i] != name {
			t.Errorf("chain[%d] = %q, want %q", i, chain[i], name)
		}
	}
}

func TestResolveFallbackChain_ThreeWayCycle(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "a", Fallback: "b"},
			{Name: "b", Fallback: "c"},
			{Name: "c", Fallback: "a"},
		},
	}

	chain := ResolveFallbackChain("a", tiers)
	if len(chain) != 2 || chain[0] != "b" || chain[1] != "c" {
		t.Errorf("expected [b c] (cycle stopped before revisiting a), got %v", chain)
	}
}

func TestResolveTierParams_SpecificTools(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "limited", Model: "haiku", Enabled: true, Tools: []string{"bash", "grep"}},
		},
	}
	tp, found := ResolveTierParams("limited", tiers, "/data", nil, nil, nil)
	if !found {
		t.Fatal("expected to find tier")
	}
	if len(tp.Tools) != 2 || tp.Tools[0] != "bash" || tp.Tools[1] != "grep" {
		t.Errorf("expected [bash grep], got %v", tp.Tools)
	}
}

func TestResolveTierParams_WildcardWithoutRegistry(t *testing.T) {
	// Without a tool registry, wildcard should resolve to empty (no tools discoverable).
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "all", Model: "haiku", Enabled: true, Tools: []string{"*"}},
		},
	}
	tp, found := ResolveTierParams("all", tiers, "/tmp/nonexistent", nil, nil, nil)
	if !found {
		t.Fatal("expected to find tier")
	}
	// With no registry and no tools.d/ dir, wildcard resolves to nil/empty.
	if len(tp.Tools) != 0 {
		t.Errorf("expected empty tools (no registry), got %v", tp.Tools)
	}
}

func TestResolveTierParams_NativeWildcardWithoutRegistry(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "native", Model: "haiku", Enabled: true, Tools: []string{"*native"}},
		},
	}
	tp, found := ResolveTierParams("native", tiers, "/data", nil, nil, nil)
	if !found {
		t.Fatal("expected to find tier")
	}
	// With nil registry, native wildcard resolves to nil.
	if tp.Tools != nil {
		t.Errorf("expected nil tools (no registry for native wildcard), got %v", tp.Tools)
	}
}

func TestTierHasRead(t *testing.T) {
	tests := []struct {
		name string
		tier TierInfo
		want bool
	}{
		{"write capable", TierInfo{WriteCapable: true}, true},
		{"has Read tool", TierInfo{Tools: []string{"Bash", "Read"}}, true},
		{"no Read tool", TierInfo{Tools: []string{"Bash"}}, false},
		{"empty tools", TierInfo{}, false},
	}
	for _, tt := range tests {
		if got := TierHasRead(tt.tier); got != tt.want {
			t.Errorf("TierHasRead(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
