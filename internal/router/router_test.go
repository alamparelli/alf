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
	valid := map[string]bool{"haiku_r": true, "sonnet_r": true, "sonnet_rw": true}
	raw := `{"tier": "sonnet_r", "reason": "needs explanation"}`

	r := parseResponse(raw, valid)
	if r.Tier != "sonnet_r" {
		t.Errorf("expected tier=sonnet_r, got %q", r.Tier)
	}
	if r.Reason != "needs explanation" {
		t.Errorf("expected reason='needs explanation', got %q", r.Reason)
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
	valid := map[string]bool{"haiku_r": true, "sonnet_r": true, "sonnet_rw": true}
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
	valid := map[string]bool{"haiku_r": true, "analyze": true}
	raw := "lorem ipsum dolor sit amet"

	r := parseResponse(raw, valid)
	if r.Tier != "" {
		t.Errorf("expected empty tier for garbage input, got %q", r.Tier)
	}
}

func TestParseResponse_InvalidTierInJSON(t *testing.T) {
	valid := map[string]bool{"haiku_r": true, "sonnet_r": true}
	raw := `{"tier": "nonexistent", "reason": "test"}`

	r := parseResponse(raw, valid)
	if r.Tier == "nonexistent" {
		t.Error("should not return invalid tier name")
	}
}

func TestBuildPrompt_IncludesRoutableTiers(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)
	prompt := buildPrompt(ClassifyInput{Message: "hello", Tiers: tiers, DataDir: t.TempDir(), ConfigDir: t.TempDir()}, valid)

	for _, name := range []string{"haiku_r", "sonnet_r", "sonnet_rw", "opus_r", "opus_rw"} {
		if !strings.Contains(prompt, name) {
			t.Errorf("prompt should contain tier %q", name)
		}
	}
}

func TestBuildPrompt_ExcludesDisabledTiers(t *testing.T) {
	tiers := defaultTiers()
	// Find and disable opus_rw
	for i := range tiers.Tiers {
		if tiers.Tiers[i].Name == "opus_rw" {
			tiers.Tiers[i].Enabled = false
			break
		}
	}
	valid := validTierSet(tiers)
	prompt := buildPrompt(ClassifyInput{Message: "hello", Tiers: tiers, DataDir: t.TempDir(), ConfigDir: t.TempDir()}, valid)

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

func TestBuildPrompt_AlwaysRoutes(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)
	prompt := buildPrompt(ClassifyInput{Message: "hello", Tiers: tiers, DataDir: t.TempDir(), ConfigDir: t.TempDir()}, valid)

	if strings.Contains(prompt, "respond directly") {
		t.Error("prompt should not allow direct responses")
	}
	if !strings.Contains(prompt, "ALWAYS route") {
		t.Error("prompt should instruct to always route")
	}
}

func TestValidTierSet_Default(t *testing.T) {
	tiers := defaultTiers()
	valid := validTierSet(tiers)

	expected := []string{"haiku_r", "sonnet_r", "sonnet_rw", "opus_r", "opus_rw"}
	for _, name := range expected {
		if !valid[name] {
			t.Errorf("expected %q in valid set", name)
		}
	}
}

func TestValidTierSet_ExcludesNonRoutable(t *testing.T) {
	tiers := defaultTiers()
	for i := range tiers.Tiers {
		if tiers.Tiers[i].Name == "haiku_r" {
			tiers.Tiers[i].Routable = false
			break
		}
	}
	valid := validTierSet(tiers)

	if valid["haiku_r"] {
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

func TestHasWriteIntent(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"you can fix all and polish", true},
		{"fix the bug", true},
		{"apply the changes", true},
		{"create a new file", true},
		{"delete that entry", true},
		{"what time is it", false},
		{"explain how this works", false},
		{"show me the logs", false},
		{"prefix has no match", false},   // "fix" not at word boundary
		{"the suffix is fine", false},
		{"corrige le fichier", false},     // non-English not matched
		{"improve the performance", true},
		{"refactor the code", true},
		{"deploy to production", true},
		{"can you generate a report", true},
	}
	for _, tt := range tests {
		got := HasWriteIntent(tt.msg)
		if got != tt.want {
			t.Errorf("HasWriteIntent(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestInterpretRaw_UpgradesReadOnlyOnWriteIntent(t *testing.T) {
	tiers := defaultTiers()
	// Router picks haiku_r but message has write intent → should upgrade
	raw := `{"tier": "haiku_r", "reason": "follow-up"}`
	r := InterpretRaw(raw, tiers, "you can fix all and polish")
	if r.Tier == "haiku_r" {
		t.Error("should have upgraded from haiku_r to a write-capable tier")
	}
	if !strings.Contains(r.Reason, "write intent") {
		t.Errorf("reason should mention write intent upgrade, got %q", r.Reason)
	}
	// Verify the upgrade target is write-capable
	access := TierAccess(r.Tier, tiers)
	if access != "read-write" {
		t.Errorf("upgraded tier %q should be read-write, got %s", r.Tier, access)
	}
}

func TestInterpretRaw_NoUpgradeForReadOnly(t *testing.T) {
	tiers := defaultTiers()
	// Router picks haiku_r for a read-only message → no upgrade
	raw := `{"tier": "haiku_r", "reason": "simple question"}`
	r := InterpretRaw(raw, tiers, "what time is it")
	if r.Tier != "haiku_r" {
		t.Errorf("should NOT upgrade for read-only message, got %q", r.Tier)
	}
}

func TestInterpretRaw_NoUpgradeForWriteTier(t *testing.T) {
	tiers := defaultTiers()
	// Router already picks a write tier → no upgrade needed
	raw := `{"tier": "sonnet_rw", "reason": "file changes"}`
	r := InterpretRaw(raw, tiers, "fix the bug")
	if r.Tier != "sonnet_rw" {
		t.Errorf("should keep sonnet_rw for write-capable tier, got %q", r.Tier)
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
	// fallbackResult picks lowest-priority enabled tier → haiku_r (priority 1)
	r := fallbackResult(tiers)
	if r.Tier != "haiku_r" {
		t.Errorf("expected fallback tier=haiku_r, got %q", r.Tier)
	}

	// Disable haiku_r → next lowest is haiku_rw (priority 2)
	for i := range tiers.Tiers {
		if tiers.Tiers[i].Name == "haiku_r" {
			tiers.Tiers[i].Enabled = false
			break
		}
	}
	r = fallbackResult(tiers)
	if r.Tier != "haiku_rw" {
		t.Errorf("expected fallback tier=haiku_rw, got %q", r.Tier)
	}
}

func TestInterpretRaw_DirectResponseFallsBack(t *testing.T) {
	tiers := defaultTiers()
	// Simulate router returning a direct response — should fallback to default tier
	raw := `{"response": "Hello!", "reason": "greeting"}`
	r := InterpretRaw(raw, tiers, "hi")
	if r.Tier == "" {
		t.Error("direct response should fallback to a tier, not return empty")
	}
	if r.Response != "" {
		t.Error("direct response should be cleared in favor of tier routing")
	}
}
