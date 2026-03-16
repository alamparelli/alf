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
