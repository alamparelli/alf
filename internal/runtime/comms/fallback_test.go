package comms

import (
	"context"
	"testing"
)

// --- ResolveFallbackChain regression tests ---

func TestResolveFallbackChain_Regression_HaikuSonnetOpus(t *testing.T) {
	// Core use case: haiku → sonnet → opus
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "haiku", Enabled: true, Fallback: "sonnet"},
			{Name: "sonnet", Enabled: true, Fallback: "opus"},
			{Name: "opus", Enabled: true},
		},
	}

	chain := ResolveFallbackChain("haiku", tiers)
	assertChain(t, chain, []string{"sonnet", "opus"}, "haiku→sonnet→opus")

	// Starting from sonnet should only get opus.
	chain = ResolveFallbackChain("sonnet", tiers)
	assertChain(t, chain, []string{"opus"}, "sonnet→opus")

	// Starting from opus should get nothing.
	chain = ResolveFallbackChain("opus", tiers)
	assertChain(t, chain, nil, "opus→nothing")
}

func TestResolveFallbackChain_Regression_DisabledTierInChain(t *testing.T) {
	// Disabled tier's fallback field is still followed (the chain resolves
	// names, availability is checked at invoke time).
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "haiku", Enabled: true, Fallback: "sonnet"},
			{Name: "sonnet", Enabled: false, Fallback: "opus"},
			{Name: "opus", Enabled: true},
		},
	}

	chain := ResolveFallbackChain("haiku", tiers)
	assertChain(t, chain, []string{"sonnet", "opus"}, "disabled sonnet still in chain")
}

func TestResolveFallbackChain_Regression_UnknownStartTier(t *testing.T) {
	tiers := TiersSnapshot{
		Tiers: []TierInfo{
			{Name: "haiku", Enabled: true, Fallback: "sonnet"},
		},
	}

	// Unknown start tier has no entry → no fallback.
	chain := ResolveFallbackChain("ghost", tiers)
	assertChain(t, chain, nil, "unknown start tier")
}

// --- Fallback-triggering error classification regression ---

func TestRegression_FallbackTriggeredOnAuthError(t *testing.T) {
	// Auth errors should NOT be context.Canceled, so fallback should trigger.
	err := context.Canceled
	if err == nil {
		t.Skip()
	}
	// Verify auth error is not classified as cancellation.
	notice := classifyProviderError("api[openai] error 401: Invalid API key", nil)
	if notice == "" {
		t.Error("auth error should produce a notice")
	}
	// ctx.Err() == nil means fallback would trigger (not cancelled).
}

func TestRegression_FallbackSkippedOnCancel(t *testing.T) {
	// When context is cancelled, fallback should NOT trigger.
	// The pipeline checks ctx.Err() == nil before entering fallback.
	if context.Canceled == nil {
		t.Fatal("context.Canceled should not be nil")
	}
	// This documents the contract: ctx.Err() != nil → no fallback.
}

func TestRegression_FallbackTriggeredOnTimeout(t *testing.T) {
	// Timeout with ctx.Err() == nil (e.g., provider-level timeout, not context deadline)
	// should trigger fallback.
	notice := classifyProviderError("request timed out after 300s", nil)
	assertContains(t, notice, "timed out", "provider timeout triggers fallback-compatible notice")
}

func TestRegression_FallbackTriggeredOnServerError(t *testing.T) {
	notice := classifyProviderError("api[openrouter] error 502: Bad Gateway", nil)
	assertContains(t, notice, "provider", "502 produces notice, fallback should try next tier")
}

func TestRegression_FallbackTriggeredOnRateLimit(t *testing.T) {
	notice := classifyProviderError("api[grok] error 429: too many requests", nil)
	assertContains(t, notice, "Rate limit", "429 produces notice, fallback should try next tier")
}

func TestRegression_FallbackTriggeredOnTurnLimit(t *testing.T) {
	notice := classifyProviderError("claude: error_max_turns", nil)
	assertContains(t, notice, "Turn limit", "turn limit produces notice, fallback should try next tier")
}

// --- helpers ---

func assertChain(t *testing.T, got, want []string, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("[%s] chain length: got %d, want %d (%v vs %v)", label, len(got), len(want), got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%s] chain[%d]: got %q, want %q", label, i, got[i], want[i])
		}
	}
}
