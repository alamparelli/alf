package router

import (
	"strings"
	"testing"

	cc "github.com/alamparelli/alf/internal/controlcenter"
)

func defaultTiers() *cc.TiersConfig {
	return cc.DefaultTiersConfig()
}

func TestParseResponse_ValidJSON(t *testing.T) {
	valid := map[string]bool{"instant": true, "analyze": true, "heavy": true}
	raw := `{"tier": "analyze", "reason": "needs explanation"}`

	r := parseResponse(raw, valid)
	if r.Tier != "analyze" {
		t.Errorf("expected tier=analyze, got %q", r.Tier)
	}
	if r.Reason != "needs explanation" {
		t.Errorf("expected reason='needs explanation', got %q", r.Reason)
	}
}

func TestParseResponse_InstantWithResponse(t *testing.T) {
	valid := map[string]bool{"instant": true, "analyze": true}
	raw := `{"tier": "instant", "reason": "greeting", "response": "Hey there!"}`

	r := parseResponse(raw, valid)
	if r.Tier != "instant" {
		t.Errorf("expected tier=instant, got %q", r.Tier)
	}
	if r.Response != "Hey there!" {
		t.Errorf("expected response='Hey there!', got %q", r.Response)
	}
}

func TestParseResponse_MarkdownFences(t *testing.T) {
	valid := map[string]bool{"heavy": true, "analyze": true}
	raw := "```json\n{\"tier\": \"heavy\", \"reason\": \"file changes\"}\n```"

	r := parseResponse(raw, valid)
	if r.Tier != "heavy" {
		t.Errorf("expected tier=heavy, got %q", r.Tier)
	}
}

func TestParseResponse_RawTextFallback(t *testing.T) {
	valid := map[string]bool{"instant": true, "analyze": true, "heavy": true}
	raw := "I think this should be classified as analyze because it needs reasoning."

	r := parseResponse(raw, valid)
	if r.Tier != "analyze" {
		t.Errorf("expected tier=analyze from text scan, got %q", r.Tier)
	}
	if r.Reason != "text-scan fallback" {
		t.Errorf("expected reason='text-scan fallback', got %q", r.Reason)
	}
}

func TestParseResponse_Garbage(t *testing.T) {
	valid := map[string]bool{"instant": true, "analyze": true}
	raw := "lorem ipsum dolor sit amet"

	r := parseResponse(raw, valid)
	if r.Tier != "" {
		t.Errorf("expected empty tier for garbage input, got %q", r.Tier)
	}
}

func TestParseResponse_InvalidTierInJSON(t *testing.T) {
	valid := map[string]bool{"instant": true, "analyze": true}
	raw := `{"tier": "nonexistent", "reason": "test"}`

	r := parseResponse(raw, valid)
	// JSON tier not in valid set → falls through to text scan → "instant" or "analyze" might match in the raw text
	// but "nonexistent" isn't in valid, so let's check it doesn't return "nonexistent"
	if r.Tier == "nonexistent" {
		t.Error("should not return invalid tier name")
	}
}

func TestBuildPrompt_IncludesRoutableTiers(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)
	prompt := buildPrompt("hello", tiers, valid, t.TempDir())

	for _, name := range []string{"instant", "analyze", "heavy", "deep"} {
		if !strings.Contains(prompt, name) {
			t.Errorf("prompt should contain tier %q", name)
		}
	}
}

func TestBuildPrompt_ExcludesDisabledTiers(t *testing.T) {
	tiers := defaultTiers()
	tiers.Tiers[3].Enabled = false // disable "deep"
	valid := validTierSet(tiers)
	prompt := buildPrompt("hello", tiers, valid, t.TempDir())

	// "deep" should not appear as a listed tier (though it might appear in distinctions text)
	lines := strings.Split(prompt, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "- deep") {
			t.Error("disabled tier 'deep' should not be listed")
		}
	}
}

func TestBuildPrompt_WriteGuard(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)
	prompt := buildPrompt("hello", tiers, valid, t.TempDir())

	if !strings.Contains(prompt, "write-capable") {
		t.Error("prompt should contain write guard warning")
	}
}

func TestBuildPrompt_TruncatesLongMessage(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)
	longMsg := strings.Repeat("a", 500)
	prompt := buildPrompt(longMsg, tiers, valid, t.TempDir())

	if strings.Contains(prompt, strings.Repeat("a", 500)) {
		t.Error("prompt should truncate long messages")
	}
	if !strings.Contains(prompt, "...") {
		t.Error("truncated message should end with ...")
	}
}

func TestBuildPrompt_IncludesDistinctions(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)
	prompt := buildPrompt("hello", tiers, valid, t.TempDir())

	if !strings.Contains(prompt, tiers.RouterDistinctions) {
		t.Error("prompt should include router distinctions")
	}
}

func TestValidTierSet_Default(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)

	expected := []string{"instant", "analyze", "heavy", "deep"}
	for _, name := range expected {
		if !valid[name] {
			t.Errorf("expected %q in valid set", name)
		}
	}
}

func TestValidTierSet_ExcludesNonRoutable(t *testing.T) {
	tiers := defaultTiers()
	tiers.Tiers[0].Routable = false // make instant non-routable
	valid := validTierSet(tiers)

	if valid["instant"] {
		t.Error("non-routable tier should not be in valid set")
	}
}

func TestResolveModel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"haiku", "claude-haiku-4-5"},
		{"sonnet", "claude-sonnet-4-6"},
		{"opus", "claude-opus-4-6"},
		{"Haiku", "claude-haiku-4-5"},
		{"claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		got := ResolveModel(tt.input)
		if got != tt.want {
			t.Errorf("ResolveModel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStripMarkdownFences(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"tier": "analyze"}`, `{"tier": "analyze"}`},
		{"```json\n{\"tier\": \"analyze\"}\n```", `{"tier": "analyze"}`},
		{"```\n{\"tier\": \"analyze\"}\n```", `{"tier": "analyze"}`},
	}

	for _, tt := range tests {
		got := stripMarkdownFences(tt.input)
		if got != tt.want {
			t.Errorf("stripMarkdownFences(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFallbackResult(t *testing.T) {
	tiers := defaultTiers()
	r := fallbackResult(tiers)
	if r.Tier != "analyze" {
		t.Errorf("expected fallback tier=analyze, got %q", r.Tier)
	}

	tiers.DefaultFallback = "heavy"
	r = fallbackResult(tiers)
	if r.Tier != "heavy" {
		t.Errorf("expected fallback tier=heavy, got %q", r.Tier)
	}
}
