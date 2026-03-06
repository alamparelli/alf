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
	valid := map[string]bool{"instant": true, "sonnet_r": true, "sonnet_rw": true}
	raw := `{"tier": "sonnet_r", "reason": "needs explanation"}`

	r := parseResponse(raw, valid)
	if r.Tier != "sonnet_r" {
		t.Errorf("expected tier=sonnet_r, got %q", r.Tier)
	}
	if r.Reason != "needs explanation" {
		t.Errorf("expected reason='needs explanation', got %q", r.Reason)
	}
}

func TestParseResponse_InstantWithResponse(t *testing.T) {
	valid := map[string]bool{"instant": true, "sonnet_r": true}
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
	valid := map[string]bool{"sonnet_rw": true, "sonnet_r": true}
	raw := "```json\n{\"tier\": \"sonnet_rw\", \"reason\": \"file changes\"}\n```"

	r := parseResponse(raw, valid)
	if r.Tier != "sonnet_rw" {
		t.Errorf("expected tier=sonnet_rw, got %q", r.Tier)
	}
}

func TestParseResponse_RawTextFallback(t *testing.T) {
	valid := map[string]bool{"instant": true, "sonnet_r": true, "sonnet_rw": true}
	raw := "I think this should be classified as sonnet_r because it needs reasoning."

	r := parseResponse(raw, valid)
	if r.Tier != "sonnet_r" {
		t.Errorf("expected tier=sonnet_r from text scan, got %q", r.Tier)
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
	valid := map[string]bool{"instant": true, "sonnet_r": true}
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
	prompt := buildPrompt(ClassifyInput{Message: "hello", Tiers: tiers, DataDir: t.TempDir(), ConfigDir: t.TempDir()}, valid)

	for _, name := range []string{"instant", "haiku_r", "sonnet_r", "sonnet_rw", "opus_r", "opus_rw"} {
		if !strings.Contains(prompt, name) {
			t.Errorf("prompt should contain tier %q", name)
		}
	}
}

func TestBuildPrompt_ExcludesDisabledTiers(t *testing.T) {
	tiers := defaultTiers()
	tiers.Tiers[6].Enabled = false // disable "opus_rw"
	valid := validTierSet(tiers)
	prompt := buildPrompt(ClassifyInput{Message: "hello", Tiers: tiers, DataDir: t.TempDir(), ConfigDir: t.TempDir()}, valid)

	// "opus_rw" should not appear as a listed tier (though it might appear in distinctions text)
	lines := strings.Split(prompt, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "- opus_rw") {
			t.Error("disabled tier 'opus_rw' should not be listed")
		}
	}
}

func TestBuildPrompt_WriteGuard(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)
	prompt := buildPrompt(ClassifyInput{Message: "hello", Tiers: tiers, DataDir: t.TempDir(), ConfigDir: t.TempDir()}, valid)

	if !strings.Contains(prompt, "write-capable") {
		t.Error("prompt should contain write guard warning")
	}
}

func TestBuildPrompt_TruncatesLongMessage(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)
	longMsg := strings.Repeat("a", 600)
	prompt := buildPrompt(ClassifyInput{Message: longMsg, Tiers: tiers, DataDir: t.TempDir(), ConfigDir: t.TempDir()}, valid)

	if strings.Contains(prompt, strings.Repeat("a", 600)) {
		t.Error("prompt should truncate long messages")
	}
	if !strings.Contains(prompt, "...") {
		t.Error("truncated message should end with ...")
	}
}

func TestBuildPrompt_IncludesDistinctions(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)
	prompt := buildPrompt(ClassifyInput{Message: "hello", Tiers: tiers, DataDir: t.TempDir(), ConfigDir: t.TempDir()}, valid)

	if !strings.Contains(prompt, tiers.RouterDistinctions) {
		t.Error("prompt should include router distinctions")
	}
}

func TestValidTierSet_Default(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)

	expected := []string{"instant", "haiku_r", "sonnet_r", "sonnet_rw", "opus_r", "opus_rw"}
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
		{`{"tier": "sonnet_r"}`, `{"tier": "sonnet_r"}`},
		{"```json\n{\"tier\": \"sonnet_r\"}\n```", `{"tier": "sonnet_r"}`},
		{"```\n{\"tier\": \"sonnet_r\"}\n```", `{"tier": "sonnet_r"}`},
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
	// fallbackResult picks lowest-priority enabled non-instant tier → haiku_r (priority 1)
	r := fallbackResult(tiers)
	if r.Tier != "haiku_r" {
		t.Errorf("expected fallback tier=haiku_r, got %q", r.Tier)
	}

	// Disable haiku_r → next lowest is haiku_rw (priority 2)
	tiers.Tiers[1].Enabled = false
	r = fallbackResult(tiers)
	if r.Tier != "haiku_rw" {
		t.Errorf("expected fallback tier=haiku_rw, got %q", r.Tier)
	}
}
