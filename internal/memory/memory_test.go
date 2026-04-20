package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	provider "github.com/alamparelli/alf/internal/ai/provider"
)

// Regression lock for step 1 (memory consolidation) of milestone
// 0.7.9. Tests the public entry points: prompt collection variants,
// onboarding flag lifecycle, preferences consolidation, workspace
// summary, tool reminder. See TEST-BASELINE.md.

// ----- Stub provider ---------------------------------------------------

type stubProvider struct {
	reply string
	err   error
	seen  string
}

func (s *stubProvider) Invoke(_ context.Context, prompt string, _ provider.Params, _ provider.OnProgress) (*provider.Result, error) {
	s.seen = prompt
	if s.err != nil {
		return nil, s.err
	}
	return &provider.Result{Text: s.reply}, nil
}

// ----- CollectPrompts --------------------------------------------------

func TestCollectPrompts_IncludesCoreAndDate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "soul.md"), []byte("You are Alf."), 0o644)
	os.WriteFile(filepath.Join(dir, "mood.md"), []byte("sharp"), 0o644)
	os.WriteFile(filepath.Join(dir, "index.md"), []byte("# Memory"), 0o644)

	prompts := CollectPrompts(dir, PromptConfig{Backend: "cli", Channel: "cc"})
	if len(prompts) < 2 {
		t.Fatalf("expected core + date + files, got %d", len(prompts))
	}
	if !strings.Contains(prompts[1], "Current date:") {
		t.Errorf("date not injected: %q", prompts[1])
	}
	// soul / mood / index content should appear with block markers.
	joined := strings.Join(prompts, "\n")
	for _, want := range []string{"soul.md", "mood.md", "index.md", "You are Alf.", "sharp"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in prompts, joined=%q", want, joined)
		}
	}
}

func TestCollectPrompts_LightWeightSkipsToolbox(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "toolbox.md"), []byte("- `bash`"), 0o644)
	os.WriteFile(filepath.Join(dir, "soul.md"), []byte("x"), 0o644)

	light := CollectPrompts(dir, PromptConfig{Weight: "light"})
	full := CollectPrompts(dir, PromptConfig{Weight: "full"})

	hasToolbox := func(ps []string) bool {
		for _, p := range ps {
			if strings.Contains(p, "=== [toolbox.md] ===") {
				return true
			}
		}
		return false
	}

	if hasToolbox(light) {
		t.Error("light weight should skip toolbox.md")
	}
	if !hasToolbox(full) {
		t.Error("full weight should include toolbox.md")
	}
}

func TestCollectPrompts_SkipsEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	// Empty file — must be skipped, not included as an empty block.
	os.WriteFile(filepath.Join(dir, "soul.md"), []byte("   \n\n  "), 0o644)
	os.WriteFile(filepath.Join(dir, "mood.md"), []byte("non-empty"), 0o644)

	prompts := CollectPrompts(dir, PromptConfig{})
	joined := strings.Join(prompts, "\n")
	// soul.md block should NOT appear (whitespace-only is treated as empty).
	if strings.Contains(joined, "[soul.md]") {
		t.Errorf("whitespace-only file should not produce a block: %q", joined)
	}
	if !strings.Contains(joined, "[mood.md]") {
		t.Errorf("non-empty file should appear: %q", joined)
	}
}

// ----- CollectSchedulerPrompts / CollectAgentContext ------------------

func TestCollectSchedulerPrompts_OnlyToolboxAndIndex(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "soul.md"), []byte("personality"), 0o644)
	os.WriteFile(filepath.Join(dir, "mood.md"), []byte("mood"), 0o644)
	os.WriteFile(filepath.Join(dir, "toolbox.md"), []byte("tools"), 0o644)
	os.WriteFile(filepath.Join(dir, "index.md"), []byte("memory"), 0o644)

	prompts := CollectSchedulerPrompts(dir)
	joined := strings.Join(prompts, "\n")

	if strings.Contains(joined, "[soul.md]") || strings.Contains(joined, "[mood.md]") {
		t.Error("scheduler prompts must not include personality files")
	}
	if !strings.Contains(joined, "[toolbox.md]") || !strings.Contains(joined, "[index.md]") {
		t.Errorf("scheduler prompts missing capabilities/context: %q", joined)
	}
}

func TestCollectAgentContext_IncludesDate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "toolbox.md"), []byte("tools"), 0o644)

	prompts := CollectAgentContext(dir)
	joined := strings.Join(prompts, "\n")

	if !strings.Contains(joined, "Current date:") || !strings.Contains(joined, "Time:") {
		t.Errorf("agent context missing clock: %q", joined)
	}
	if strings.Contains(joined, "[soul.md]") {
		t.Error("agent context must not include soul.md")
	}
}

// ----- CollectInline ---------------------------------------------------

func TestCollectInline_CoreAndPersonality(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "soul.md"), []byte("Alf voice"), 0o644)
	os.WriteFile(filepath.Join(dir, "mood.md"), []byte("sharp"), 0o644)

	got := CollectInline(dir)

	if !strings.Contains(got, "Alf voice") || !strings.Contains(got, "sharp") {
		t.Errorf("CollectInline missing soul/mood: %q", got)
	}
	if !strings.Contains(got, "[soul.md]") || !strings.Contains(got, "[mood.md]") {
		t.Errorf("CollectInline missing block markers: %q", got)
	}
}

// ----- ToolReminder ----------------------------------------------------

func TestToolReminder_ExtractsToolNamesFromToolbox(t *testing.T) {
	dir := t.TempDir()
	toolbox := "- `bash` — run shell\n- `grep` — search\n- `unrelated line\n"
	os.WriteFile(filepath.Join(dir, "toolbox.md"), []byte(toolbox), 0o644)

	got := ToolReminder(dir)
	if !strings.Contains(got, "bash") || !strings.Contains(got, "grep") {
		t.Errorf("ToolReminder missing names: %q", got)
	}
}

func TestToolReminder_EmptyWhenNoToolbox(t *testing.T) {
	if got := ToolReminder(t.TempDir()); got != "" {
		t.Errorf("ToolReminder on empty dir should be empty, got %q", got)
	}
}

// ----- ToolInstruction -------------------------------------------------

func TestToolInstruction_SortedDeterministic(t *testing.T) {
	a := ToolInstruction([]string{"grep", "bash", "ls"})
	b := ToolInstruction([]string{"ls", "grep", "bash"})
	if a != b {
		t.Errorf("order-dependent output breaks prompt caching:\nA=%q\nB=%q", a, b)
	}
	if !strings.Contains(a, "bash, grep, ls") {
		t.Errorf("names not sorted+joined: %q", a)
	}
}

// ----- Onboarding flag lifecycle --------------------------------------

func TestOnboarding_SetClear(t *testing.T) {
	dir := t.TempDir()

	if got := OnboardingPrompt(dir); got != "" {
		t.Errorf("no flag should return empty, got len=%d", len(got))
	}

	SetOnboarding(dir)
	if got := OnboardingPrompt(dir); got == "" {
		t.Error("SetOnboarding didn't activate the flag")
	}

	ClearOnboarding(dir)
	if got := OnboardingPrompt(dir); got != "" {
		t.Error("ClearOnboarding didn't remove the flag")
	}
}

// ----- Bootstrap -------------------------------------------------------

func TestBootstrap_CreatesDefaultFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "context") // ensure Bootstrap creates the dir
	Bootstrap(dir)

	for _, f := range []string{"soul.md", "mood.md", "index.md"} {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Errorf("Bootstrap did not create %s: %v", f, err)
			continue
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			t.Errorf("Bootstrap wrote empty %s", f)
		}
	}
}

func TestBootstrap_SetsOnboardingFlagOnFreshInstall(t *testing.T) {
	dir := t.TempDir()
	Bootstrap(dir)

	if OnboardingPrompt(dir) == "" {
		t.Error("fresh install should activate onboarding flag (index.md has placeholder)")
	}
}

func TestBootstrap_PreservesNonEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	// User-edited file — Bootstrap must not overwrite.
	os.WriteFile(filepath.Join(dir, "soul.md"), []byte("custom soul"), 0o644)
	Bootstrap(dir)

	data, _ := os.ReadFile(filepath.Join(dir, "soul.md"))
	if string(data) != "custom soul" {
		t.Errorf("Bootstrap overwrote user file: got %q", string(data))
	}
}

func TestBootstrap_RemovesLegacyMemorySystemFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "memory-system.md"), []byte("legacy"), 0o644)
	Bootstrap(dir)

	if _, err := os.Stat(filepath.Join(dir, "memory-system.md")); !os.IsNotExist(err) {
		t.Error("Bootstrap did not remove legacy memory-system.md")
	}
}

// ----- WorkspaceSummary ------------------------------------------------

func TestWorkspaceSummary_ListsTopLevelDirs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "apps"), 0o755)
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755)
	os.MkdirAll(filepath.Join(dir, "config.d"), 0o755) // hiddenDirs
	os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o644)

	got := WorkspaceSummary(dir)

	for _, want := range []string{"apps/", "logs/", "note.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("WorkspaceSummary missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, ".hidden") {
		t.Errorf("WorkspaceSummary exposed hidden dir:\n%s", got)
	}
	if strings.Contains(got, "config.d") {
		t.Errorf("WorkspaceSummary exposed config.d (hidden by design):\n%s", got)
	}
}

func TestWorkspaceSummary_ExpandsContextSubdir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "context"), 0o755)
	os.WriteFile(filepath.Join(dir, "context", "soul.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "context", "mood.md"), []byte("x"), 0o644)

	got := WorkspaceSummary(dir)
	if !strings.Contains(got, "soul.md") || !strings.Contains(got, "mood.md") {
		t.Errorf("context/ subdir not expanded:\n%s", got)
	}
}

func TestWorkspaceSummary_UnreadableDirDegrades(t *testing.T) {
	// Pointing at a non-existent path should produce the fallback message,
	// not crash.
	got := WorkspaceSummary("/nonexistent-at-test-time/xyz")
	if !strings.Contains(got, "Workspace") {
		t.Errorf("missing header on unreadable dir: %q", got)
	}
}

// ----- ConsolidatePreferences ------------------------------------------

func TestConsolidatePreferences_BelowThresholdNoOp(t *testing.T) {
	dir := t.TempDir()

	// 3 entries < consolidateThreshold (20).
	for i := 0; i < 3; i++ {
		AppendPreference(dir, "p", "positive", "👍")
	}

	original, _ := os.ReadFile(filepath.Join(dir, preferencesFile))
	stub := &stubProvider{reply: "should-never-run"}

	ConsolidatePreferences(dir, stub, "test-model")

	if stub.seen != "" {
		t.Error("provider invoked despite being below threshold")
	}

	after, _ := os.ReadFile(filepath.Join(dir, preferencesFile))
	if string(after) != string(original) {
		t.Errorf("file mutated despite no-op:\nbefore=%q\nafter=%q", string(original), string(after))
	}
}

func TestConsolidatePreferences_AboveThresholdReplacesFile(t *testing.T) {
	dir := t.TempDir()

	// Push above threshold.
	for i := 0; i < consolidateThreshold+1; i++ {
		AppendPreference(dir, "pref", "positive", "👍")
	}

	consolidated := "# User Preferences\n\n## Communication\n- [+] concise answers\n"
	stub := &stubProvider{reply: consolidated}

	ConsolidatePreferences(dir, stub, "test-model")

	if stub.seen == "" {
		t.Fatal("provider was not invoked despite being above threshold")
	}

	got, _ := os.ReadFile(filepath.Join(dir, preferencesFile))
	if !strings.Contains(string(got), "## Communication") {
		t.Errorf("file not replaced with consolidated content:\n%s", string(got))
	}
}

func TestConsolidatePreferences_StripsMarkdownFences(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < consolidateThreshold+1; i++ {
		AppendPreference(dir, "pref", "positive", "👍")
	}

	fenced := "```markdown\n# User Preferences\n\n## Tone\n- [+] direct\n```"
	stub := &stubProvider{reply: fenced}

	ConsolidatePreferences(dir, stub, "test-model")

	got, _ := os.ReadFile(filepath.Join(dir, preferencesFile))
	if strings.Contains(string(got), "```") {
		t.Errorf("markdown fences leaked into file:\n%s", string(got))
	}
	if !strings.Contains(string(got), "## Tone") {
		t.Errorf("content dropped:\n%s", string(got))
	}
}

func TestConsolidatePreferences_RejectsUnexpectedFormat(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < consolidateThreshold+1; i++ {
		AppendPreference(dir, "pref", "positive", "👍")
	}
	original, _ := os.ReadFile(filepath.Join(dir, preferencesFile))

	stub := &stubProvider{reply: "lorem ipsum without the expected header"}
	ConsolidatePreferences(dir, stub, "test-model")

	after, _ := os.ReadFile(filepath.Join(dir, preferencesFile))
	if string(after) != string(original) {
		t.Errorf("file replaced with invalid format:\n%s", string(after))
	}
}

func TestConsolidatePreferences_ProviderErrorLeavesFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < consolidateThreshold+1; i++ {
		AppendPreference(dir, "pref", "positive", "👍")
	}
	original, _ := os.ReadFile(filepath.Join(dir, preferencesFile))

	stub := &stubProvider{err: errors.New("llm unreachable")}
	ConsolidatePreferences(dir, stub, "test-model")

	after, _ := os.ReadFile(filepath.Join(dir, preferencesFile))
	if string(after) != string(original) {
		t.Errorf("provider error should preserve file, got:\n%s", string(after))
	}
}
