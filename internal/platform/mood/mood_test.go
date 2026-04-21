package mood

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- reactions.go / reaction_score.go pure helpers ---

func TestIsAllowedReaction(t *testing.T) {
	if !IsAllowedReaction("👍") {
		t.Error("👍 should be allowed")
	}
	if IsAllowedReaction("🫨") {
		t.Error("🫨 should not be allowed")
	}
	if IsAllowedReaction("") {
		t.Error("empty string must not be allowed")
	}
}

func TestValidateOrFallback_EmptyStaysEmpty(t *testing.T) {
	if got := ValidateOrFallback(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestValidateOrFallback_AllowedPassthrough(t *testing.T) {
	if got := ValidateOrFallback("🔥"); got != "🔥" {
		t.Errorf("allowed emoji should pass through, got %q", got)
	}
}

func TestValidateOrFallback_UnknownPositive(t *testing.T) {
	// 🤩 is in AllowedReactionEmoji, so use something unknown-to-Allowed but
	// present in EmojiWeights with positive weight — IsAllowed check fails →
	// fallback kicks in using EmojiWeights sentiment.
	// Pick an unknown emoji that's not in AllowedReactionEmoji.
	got := ValidateOrFallback("🫠")
	if got == "" {
		t.Error("expected non-empty fallback")
	}
	if !IsAllowedReaction(got) {
		t.Errorf("fallback must be an allowed emoji, got %q", got)
	}
}

func TestAllowedReactionList(t *testing.T) {
	list := AllowedReactionList()
	if !strings.Contains(list, "👍") || !strings.Contains(list, "💯") {
		t.Errorf("list missing expected emoji: %s", list)
	}
	tokens := strings.Fields(list)
	if len(tokens) != len(AllowedReactionEmoji) {
		t.Errorf("expected %d tokens, got %d", len(AllowedReactionEmoji), len(tokens))
	}
}

func TestIsNegative(t *testing.T) {
	tests := map[string]bool{
		"👎": true, "💩": true, "😡": true,
		"👍": false, "🔥": false, "🤔": false, "unknown": false,
	}
	for e, want := range tests {
		if got := IsNegative(e); got != want {
			t.Errorf("IsNegative(%q) = %v, want %v", e, got, want)
		}
	}
}

func TestIsStrongNegative(t *testing.T) {
	if !IsStrongNegative("💩") {
		t.Error("💩 should be strong negative")
	}
	if IsStrongNegative("👎") {
		t.Error("👎 is mild negative, not strong")
	}
	if IsStrongNegative("👍") {
		t.Error("👍 is positive")
	}
	if IsStrongNegative("unknown") {
		t.Error("unknown emoji must not be strong negative")
	}
}

func TestScoreToState(t *testing.T) {
	tests := []struct {
		score int
		want  string
	}{
		{20, "on_fire"},
		{6, "on_fire"},
		{5, "flowing"},
		{3, "flowing"},
		{2, "neutral"},
		{0, "neutral"},
		{-2, "neutral"},
		{-3, "careful"},
		{-6, "careful"},
		{-7, "off_track"},
		{-100, "off_track"},
	}
	for _, tt := range tests {
		if got := scoreToState(tt.score); got != tt.want {
			t.Errorf("scoreToState(%d) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestStateInstruction(t *testing.T) {
	if StateInstruction("on_fire") == "" {
		t.Error("on_fire should have an instruction")
	}
	if StateInstruction("neutral") != "" {
		t.Error("neutral should have empty instruction")
	}
	if StateInstruction("off_track") == "" {
		t.Error("off_track should have its catch-all instruction")
	}
	if StateInstruction("bogus") != "" {
		t.Error("unknown state should return empty")
	}
}

func TestShouldReact_Deterministic(t *testing.T) {
	// Probability=1.0 → always true.
	trueCount := 0
	for i := 0; i < 50; i++ {
		if ShouldReact("on_fire") {
			trueCount++
		}
	}
	if trueCount != 50 {
		t.Errorf("on_fire should always react, got %d/50", trueCount)
	}
	// Probability=0.0 → always false.
	falseCount := 0
	for i := 0; i < 50; i++ {
		if !ShouldReact("careful") {
			falseCount++
		}
	}
	if falseCount != 50 {
		t.Errorf("careful should never react, got %d/50", 50-falseCount)
	}
}

func TestShouldReact_UnknownStateHas50Percent(t *testing.T) {
	// Unknown state falls to 0.50 default. We don't assert exact stats but
	// can confirm it's neither always-true nor always-false.
	trues := 0
	for i := 0; i < 200; i++ {
		if ShouldReact("???") {
			trues++
		}
	}
	if trues == 0 || trues == 200 {
		t.Errorf("unknown state expected mixed results near 50%%, got %d/200", trues)
	}
}

func TestChooseMirror_KnownEmojiKnownState(t *testing.T) {
	for i := 0; i < 20; i++ {
		m := ChooseMirror("👍", "flowing")
		if m == "" {
			t.Fatal("expected non-empty mirror")
		}
		if !IsAllowedReaction(m) {
			t.Errorf("mirror must be an allowed emoji, got %q", m)
		}
	}
}

func TestChooseMirror_UnknownEmojiFallsBackToDefault(t *testing.T) {
	m := ChooseMirror("🫠", "flowing")
	if m == "" {
		t.Fatal("unknown user emoji should still return a mirror")
	}
	if !IsAllowedReaction(m) {
		t.Errorf("fallback mirror must be allowed, got %q", m)
	}
}

func TestChooseMirror_UnknownStateFallsBackToNeutral(t *testing.T) {
	m := ChooseMirror("👍", "bogus-state")
	if m == "" {
		t.Fatal("unknown state should still return a mirror")
	}
}

func TestChooseSpontaneous_ReturnsEmoji(t *testing.T) {
	for _, state := range []string{"on_fire", "flowing", "neutral", "careful", "off_track", "???"} {
		e := ChooseSpontaneous(state)
		if e == "" {
			t.Errorf("state %q produced empty spontaneous", state)
		}
	}
}

// --- mood.go ---

func TestGenerateDaily_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	GenerateDaily(dir)

	data, err := os.ReadFile(filepath.Join(dir, "mood.md"))
	if err != nil {
		t.Fatalf("mood.md not created: %v", err)
	}
	if !strings.Contains(string(data), "Current mood:") {
		t.Errorf("mood.md missing mood line: %s", data)
	}
	if !strings.Contains(string(data), "Generated: "+time.Now().Format("2006-01-02")) {
		t.Errorf("mood.md missing today's date: %s", data)
	}
}

func TestGenerateDaily_Idempotent(t *testing.T) {
	dir := t.TempDir()
	GenerateDaily(dir)
	before, _ := os.ReadFile(filepath.Join(dir, "mood.md"))

	// Second call should not re-generate (same date).
	GenerateDaily(dir)
	after, _ := os.ReadFile(filepath.Join(dir, "mood.md"))

	if string(before) != string(after) {
		t.Errorf("GenerateDaily should be idempotent for same date")
	}
}

func TestGetCurrentState_Defaults(t *testing.T) {
	// No mood.md → neutral.
	if got := GetCurrentState(t.TempDir()); got != "neutral" {
		t.Errorf("expected neutral default, got %q", got)
	}
}

func TestGetCurrentState_ParsesLiveFeedback(t *testing.T) {
	dir := t.TempDir()
	content := `# Mood

Current mood: sharp
Tone: direct

## Live Feedback
Score: 4
State: flowing
`
	os.WriteFile(filepath.Join(dir, "mood.md"), []byte(content), 0o644)

	if got := GetCurrentState(dir); got != "flowing" {
		t.Errorf("expected flowing, got %q", got)
	}
}

func TestGetCurrentState_NoLiveFeedbackSection(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mood.md"), []byte("# Mood\n\nCurrent mood: zen\n"), 0o644)
	if got := GetCurrentState(dir); got != "neutral" {
		t.Errorf("missing section must default to neutral, got %q", got)
	}
}

// --- reaction_score.go (LogReaction, GetTodayScore) ---

func TestLogReaction_AppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	LogReaction(dir, "👍", 42)
	LogReaction(dir, "💯", 43)

	data, err := os.ReadFile(filepath.Join(dir, "logs", "reactions.jsonl"))
	if err != nil {
		t.Fatalf("reactions.jsonl not written: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	var e reactionEntry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("line 0 not JSON: %v", err)
	}
	if e.Emoji != "👍" || e.Weight != 1 {
		t.Errorf("unexpected entry: %+v", e)
	}
}

func TestGetTodayScore_NoLog(t *testing.T) {
	score, state := GetTodayScore(t.TempDir())
	if score != 0 || state != "neutral" {
		t.Errorf("empty log should yield (0, neutral), got (%d, %s)", score, state)
	}
}

func TestGetTodayScore_WeightsAndRecency(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	os.MkdirAll(logDir, 0o755)
	path := filepath.Join(logDir, "reactions.jsonl")

	now := time.Now()
	// Non-recent positive (weight 1), non-recent strong positive (weight 3),
	// recent positive (weight 1 → boosted to 2).
	entries := []reactionEntry{
		{Timestamp: now.Add(-2 * time.Hour), Emoji: "👍", Weight: 1},
		{Timestamp: now.Add(-90 * time.Minute), Emoji: "🔥", Weight: 3},
		{Timestamp: now.Add(-5 * time.Minute), Emoji: "👍", Weight: 1},
	}
	var sb strings.Builder
	for _, e := range entries {
		data, _ := json.Marshal(e)
		sb.Write(data)
		sb.WriteString("\n")
	}
	os.WriteFile(path, []byte(sb.String()), 0o644)

	score, state := GetTodayScore(dir)
	if score != 1+3+2 {
		t.Errorf("expected score=6, got %d", score)
	}
	if state != "on_fire" {
		t.Errorf("score 6 should be on_fire, got %s", state)
	}
}

func TestGetTodayScore_IgnoresYesterday(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	os.MkdirAll(logDir, 0o755)
	path := filepath.Join(logDir, "reactions.jsonl")

	now := time.Now()
	yesterday := now.Add(-26 * time.Hour)
	entries := []reactionEntry{
		{Timestamp: yesterday, Emoji: "🔥", Weight: 3},
	}
	var sb strings.Builder
	for _, e := range entries {
		data, _ := json.Marshal(e)
		sb.Write(data)
		sb.WriteString("\n")
	}
	os.WriteFile(path, []byte(sb.String()), 0o644)

	score, _ := GetTodayScore(dir)
	if score != 0 {
		t.Errorf("yesterday's reactions must be excluded, got score=%d", score)
	}
}

// --- mood_updater.go ---

func TestUpdateLiveFeedback_AppendsSection(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mood.md"), []byte("# Mood\n\nCurrent mood: sharp\n"), 0o644)

	dataDir := t.TempDir()
	UpdateLiveFeedback(dir, dataDir)

	data, _ := os.ReadFile(filepath.Join(dir, "mood.md"))
	content := string(data)
	if !strings.Contains(content, "## Live Feedback") {
		t.Errorf("Live Feedback section missing: %s", content)
	}
	if !strings.Contains(content, "State: neutral") {
		t.Errorf("expected neutral state when no reactions: %s", content)
	}
}

func TestUpdateLiveFeedback_ReplacesExistingSection(t *testing.T) {
	dir := t.TempDir()
	original := `# Mood

Current mood: zen

## Live Feedback
Score: 99
State: stale
Updated: 00:00
`
	os.WriteFile(filepath.Join(dir, "mood.md"), []byte(original), 0o644)

	UpdateLiveFeedback(dir, t.TempDir())

	data, _ := os.ReadFile(filepath.Join(dir, "mood.md"))
	content := string(data)
	if strings.Contains(content, "State: stale") {
		t.Errorf("stale Live Feedback should have been replaced: %s", content)
	}
	// Original mood should be preserved.
	if !strings.Contains(content, "Current mood: zen") {
		t.Errorf("original mood lost: %s", content)
	}
	// There must be exactly one Live Feedback section.
	if n := strings.Count(content, "## Live Feedback"); n != 1 {
		t.Errorf("expected exactly 1 Live Feedback section, got %d", n)
	}
}

func TestUpdateLiveFeedback_NoFile(t *testing.T) {
	// Should be a no-op when mood.md doesn't exist.
	UpdateLiveFeedback(t.TempDir(), t.TempDir())
}
