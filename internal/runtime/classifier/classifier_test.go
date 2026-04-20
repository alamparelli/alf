package classifier

import (
	"bytes"
	"log"
	"strings"
	"testing"

	cc "github.com/alamparelli/alf/internal/controlcenter"
)

// TestInterpretRaw_LogsRawOnParseFailure verifies that when the classifier
// returns unparseable output, the raw content is logged (truncated) so
// parse failures can be diagnosed. See #194.
func TestInterpretRaw_LogsRawOnParseFailure(t *testing.T) {
	var buf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(origOut)

	tiers := defaultTiers()
	// Garbage that neither parses as JSON nor contains a tier name.
	raw := "zzzz garbage output that should not match any tier zzzz"
	_ = InterpretRaw(raw, tiers, "hello")

	logged := buf.String()
	if !strings.Contains(logged, "parse failed") {
		t.Errorf("expected 'parse failed' in log, got: %s", logged)
	}
	if !strings.Contains(logged, "raw=") {
		t.Errorf("expected raw= in log, got: %s", logged)
	}
	if !strings.Contains(logged, "zzzz garbage") {
		t.Errorf("expected raw sample in log, got: %s", logged)
	}
}

// TestInterpretRaw_TruncatesLongRaw verifies the raw log preview is
// truncated to ~200 chars to avoid flooding the log.
func TestInterpretRaw_TruncatesLongRaw(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(log.Writer())

	tiers := defaultTiers()
	raw := strings.Repeat("x", 500)
	_ = InterpretRaw(raw, tiers, "hello")

	logged := buf.String()
	// Ensure we don't dump all 500 chars.
	if strings.Count(logged, "x") >= 500 {
		t.Errorf("raw preview not truncated: logged %d chars", strings.Count(logged, "x"))
	}
}

func defaultTiers() *cc.TiersConfig {
	return cc.DefaultTiersConfig()
}

func TestParseResponse_ValidJSON(t *testing.T) {
	valid := map[string]bool{"haiku": true, "sonnet": true, "opus": true}
	raw := `{"tier": "sonnet", "reason": "needs explanation"}`

	r := parseResponse(raw, valid)
	if r.Tier != "sonnet" {
		t.Errorf("expected tier=sonnet, got %q", r.Tier)
	}
	if r.Reason != "needs explanation" {
		t.Errorf("expected reason='needs explanation', got %q", r.Reason)
	}
}

func TestParseResponse_MarkdownFences(t *testing.T) {
	valid := map[string]bool{"sonnet": true, "haiku": true}
	raw := "```json\n{\"tier\": \"sonnet\", \"reason\": \"file changes\"}\n```"

	r := parseResponse(raw, valid)
	if r.Tier != "sonnet" {
		t.Errorf("expected tier=sonnet, got %q", r.Tier)
	}
}

func TestParseResponse_RawTextFallback(t *testing.T) {
	valid := map[string]bool{"haiku": true, "sonnet": true, "opus": true}
	raw := "I think this should be classified as sonnet because it needs reasoning."

	r := parseResponse(raw, valid)
	if r.Tier != "sonnet" {
		t.Errorf("expected tier=sonnet from text scan, got %q", r.Tier)
	}
	if r.Reason != "text-scan fallback" {
		t.Errorf("expected reason='text-scan fallback', got %q", r.Reason)
	}
}

func TestParseResponse_Garbage(t *testing.T) {
	valid := map[string]bool{"haiku": true, "analyze": true}
	raw := "lorem ipsum dolor sit amet"

	r := parseResponse(raw, valid)
	if r.Tier != "" {
		t.Errorf("expected empty tier for garbage input, got %q", r.Tier)
	}
}

func TestParseResponse_InvalidTierInJSON(t *testing.T) {
	valid := map[string]bool{"haiku": true, "sonnet": true}
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

	for _, name := range []string{"codex-fast", "codex-dev", "codex-arch"} {
		if !strings.Contains(prompt, name) {
			t.Errorf("prompt should contain tier %q", name)
		}
	}
}

func TestBuildPrompt_ExcludesDisabledTiers(t *testing.T) {
	tiers := defaultTiers()
	// Disable the last routable tier (codex-arch or opus depending on profile)
	disabledName := tiers.Tiers[2].Name
	tiers.Tiers[2].Enabled = false
	valid := validTierSet(tiers)
	prompt := buildPrompt(ClassifyInput{Message: "hello", Tiers: tiers, DataDir: t.TempDir(), ConfigDir: t.TempDir()}, valid)

	lines := strings.Split(prompt, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "- "+disabledName) {
			t.Errorf("disabled tier %q should not be listed", disabledName)
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

	expected := []string{"codex-fast", "codex-dev", "codex-arch"}
	for _, name := range expected {
		if !valid[name] {
			t.Errorf("expected %q in valid set", name)
		}
	}
}

func TestValidTierSet_ExcludesNonRoutable(t *testing.T) {
	tiers := defaultTiers()
	firstName := tiers.Tiers[0].Name
	tiers.Tiers[0].Routable = false
	valid := validTierSet(tiers)

	if valid[firstName] {
		t.Errorf("non-routable tier %q should not be in valid set", firstName)
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

func TestInterpretRaw_UpgradesNonWriteTierOnWriteIntent(t *testing.T) {
	tiers := defaultTiers()
	// Make codex-dev write-capable so there's an upgrade target
	for i := range tiers.Tiers {
		if tiers.Tiers[i].Name == "codex-dev" {
			tiers.Tiers[i].WriteCapable = true
		}
	}
	// Router picks agent (non-write-capable) but message has write intent → should upgrade
	raw := `{"tier": "agent", "reason": "follow-up"}`
	r := InterpretRaw(raw, tiers, "you can fix all and polish")
	if r.Tier == "agent" {
		t.Error("should have upgraded from agent to a write-capable tier")
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
	// Router picks codex-fast for a read-only message → no upgrade
	raw := `{"tier": "codex-fast", "reason": "simple question"}`
	r := InterpretRaw(raw, tiers, "what time is it")
	if r.Tier != "codex-fast" {
		t.Errorf("should NOT upgrade for read-only message, got %q", r.Tier)
	}
}

func TestInterpretRaw_NoUpgradeForWriteTier(t *testing.T) {
	tiers := defaultTiers()
	// Make codex-dev write-capable
	for i := range tiers.Tiers {
		if tiers.Tiers[i].Name == "codex-dev" {
			tiers.Tiers[i].WriteCapable = true
		}
	}
	// Router already picks a write tier → no upgrade needed
	raw := `{"tier": "codex-dev", "reason": "file changes"}`
	r := InterpretRaw(raw, tiers, "fix the bug")
	if r.Tier != "codex-dev" {
		t.Errorf("should keep codex-dev for write-capable tier, got %q", r.Tier)
	}
}

func TestStripMarkdownFences(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"tier": "sonnet"}`, `{"tier": "sonnet"}`},
		{"```json\n{\"tier\": \"sonnet\"}\n```", `{"tier": "sonnet"}`},
		{"```\n{\"tier\": \"sonnet\"}\n```", `{"tier": "sonnet"}`},
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
	// fallbackResult picks lowest-priority enabled tier (priority 1)
	r := fallbackResult(tiers)
	if r.Tier != tiers.Tiers[0].Name {
		t.Errorf("expected fallback tier=%q, got %q", tiers.Tiers[0].Name, r.Tier)
	}

	// Disable first tier → next lowest is priority 2
	first := tiers.Tiers[0].Name
	second := tiers.Tiers[1].Name
	for i := range tiers.Tiers {
		if tiers.Tiers[i].Name == first {
			tiers.Tiers[i].Enabled = false
			break
		}
	}
	r = fallbackResult(tiers)
	if r.Tier != second {
		t.Errorf("expected fallback tier=%q, got %q", second, r.Tier)
	}
}

func TestInterpretRaw_DirectResponseFallsBack(t *testing.T) {
	tiers := defaultTiers()
	// Simulate router returning a direct response - should fallback to default tier
	raw := `{"response": "Hello!", "reason": "greeting"}`
	r := InterpretRaw(raw, tiers, "hi")
	if r.Tier == "" {
		t.Error("direct response should fallback to a tier, not return empty")
	}
	if r.Response != "" {
		t.Error("direct response should be cleared in favor of tier routing")
	}
}

func TestBuildPrompt_IncludesRecentContext(t *testing.T) {
	tiers := defaultTiers()
	input := ClassifyInput{
		Message:       "oui c'est le titre",
		Tiers:         tiers,
		DataDir:       t.TempDir(),
		ConfigDir:     t.TempDir(),
		LastTier:      "sonnet",
		MessageCount:  3,
		RecentContext: "[user]: a regarder: https://youtube.com/watch?v=abc\n[sonnet]: Ajouté, mais je n'ai pas pu récupérer le titre.\n",
	}
	valid := map[string]bool{"haiku": true, "sonnet": true}
	prompt := buildPrompt(input, valid)

	if !strings.Contains(prompt, "Recent conversation") {
		t.Error("expected prompt to include 'Recent conversation' section")
	}
	if !strings.Contains(prompt, "youtube.com") {
		t.Error("expected prompt to include recent context content")
	}
	if !strings.Contains(prompt, "[sonnet]") {
		t.Error("expected prompt to include tier labels from context")
	}
	if !strings.Contains(prompt, "continuation of the conversation") {
		t.Error("expected updated continuity instruction")
	}
}

func TestBuildPrompt_NoRecentContextWhenEmpty(t *testing.T) {
	tiers := defaultTiers()
	input := ClassifyInput{
		Message:   "bonjour",
		Tiers:     tiers,
		DataDir:   t.TempDir(),
		ConfigDir: t.TempDir(),
	}
	valid := map[string]bool{"haiku": true, "sonnet": true}
	prompt := buildPrompt(input, valid)

	if strings.Contains(prompt, "Recent conversation") {
		t.Error("should not include recent conversation section when no context")
	}
}
