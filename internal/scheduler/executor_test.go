package scheduler

import (
	"strings"
	"testing"
)

// --- Mock SkillStoreReader ---

type mockSkillStore struct {
	skills map[string]*SkillInfo
}

func (m *mockSkillStore) Get(name string) (*SkillInfo, bool) {
	sk, ok := m.skills[name]
	return sk, ok
}

// --- buildSkillBlock tests ---

func TestBuildSkillBlock_EmptyNames(t *testing.T) {
	store := &mockSkillStore{skills: map[string]*SkillInfo{}}
	got := buildSkillBlock(store, nil)
	if got != "" {
		t.Fatalf("expected empty string for nil names, got %q", got)
	}
	got = buildSkillBlock(store, []string{})
	if got != "" {
		t.Fatalf("expected empty string for empty names, got %q", got)
	}
}

func TestBuildSkillBlock_UnknownSkill(t *testing.T) {
	store := &mockSkillStore{skills: map[string]*SkillInfo{}}
	got := buildSkillBlock(store, []string{"nonexistent"})
	if got != "" {
		t.Fatalf("expected empty string for unknown skill, got %q", got)
	}
}

func TestBuildSkillBlock_SingleSkill(t *testing.T) {
	store := &mockSkillStore{skills: map[string]*SkillInfo{
		"deploy": {Name: "deploy", Prompt: "Run the deploy pipeline"},
	}}
	got := buildSkillBlock(store, []string{"deploy"})
	want := "--- deploy ---\nRun the deploy pipeline"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildSkillBlock_MultipleSkills(t *testing.T) {
	store := &mockSkillStore{skills: map[string]*SkillInfo{
		"deploy": {Name: "deploy", Prompt: "Run deploy"},
		"audit":  {Name: "audit", Prompt: "Run audit"},
	}}
	got := buildSkillBlock(store, []string{"deploy", "audit"})
	if !strings.Contains(got, "--- deploy ---\nRun deploy") {
		t.Fatalf("missing deploy block in:\n%s", got)
	}
	if !strings.Contains(got, "--- audit ---\nRun audit") {
		t.Fatalf("missing audit block in:\n%s", got)
	}
	// Blocks separated by double newline.
	if !strings.Contains(got, "\n\n") {
		t.Fatalf("expected double newline separator in:\n%s", got)
	}
}

func TestBuildSkillBlock_EmptyPromptSkipped(t *testing.T) {
	store := &mockSkillStore{skills: map[string]*SkillInfo{
		"empty": {Name: "empty", Prompt: ""},
	}}
	got := buildSkillBlock(store, []string{"empty"})
	if got != "" {
		t.Fatalf("expected empty string for skill with empty prompt, got %q", got)
	}
}

func TestBuildSkillBlock_MixedKnownUnknownEmpty(t *testing.T) {
	store := &mockSkillStore{skills: map[string]*SkillInfo{
		"valid": {Name: "valid", Prompt: "Do something"},
		"empty": {Name: "empty", Prompt: ""},
	}}
	got := buildSkillBlock(store, []string{"unknown", "empty", "valid"})
	want := "--- valid ---\nDo something"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// --- errorPatterns tests ---

func TestErrorPatterns_MatchesKnownPatterns(t *testing.T) {
	cases := []struct {
		input string
		match bool
	}{
		{"something error occurred", true},
		{"PANIC in goroutine", true},
		{"fatal: not a git repository", true},
		{"task failed successfully", true},
		{"connection timeout after 30s", true},
		{"process killed by OOM", true},
		{"redis ERR wrong number of args", true},
		{"CRITICAL disk usage at 95%", true},
		{"WARNING memory pressure", true},
		// Case insensitive: "error" should match "Error", "ERROR", etc.
		{"Error: file not found", true},
		{"FATAL crash", true},
		{"Failed to connect", true},
	}

	for _, tc := range cases {
		lower := strings.ToLower(tc.input)
		matched := false
		for _, p := range errorPatterns {
			if strings.Contains(lower, strings.ToLower(p)) {
				matched = true
				break
			}
		}
		if matched != tc.match {
			t.Errorf("input=%q: expected match=%v, got %v", tc.input, tc.match, matched)
		}
	}
}

func TestErrorPatterns_CleanOutputNoMatch(t *testing.T) {
	clean := []string{
		"all services healthy",
		"deployment complete",
		"3 pods running",
		"OK 200",
		"backup finished in 12s",
		"",
	}

	for _, input := range clean {
		lower := strings.ToLower(input)
		for _, p := range errorPatterns {
			if strings.Contains(lower, strings.ToLower(p)) {
				t.Errorf("clean input %q matched pattern %q", input, p)
			}
		}
	}
}

// --- dispatch tests ---

type mockTG struct {
	sent []string
}

func (m *mockTG) SendMessage(_ int64, text string) error {
	m.sent = append(m.sent, text)
	return nil
}

type mockCC struct {
	notified []string
}

func (m *mockCC) Notify(text string) {
	m.notified = append(m.notified, text)
}

func newTestEngine(tg TelegramSender, cc ChatNotifier, dataDir string) *Engine {
	return &Engine{
		cfg: Config{
			TG:      tg,
			CC:      cc,
			ChatID:  123,
			DataDir: dataDir,
		},
	}
}

func TestDispatch_EmptyText(t *testing.T) {
	tg := &mockTG{}
	cc := &mockCC{}
	e := newTestEngine(tg, cc, t.TempDir())

	e.dispatch(&Job{Output: "chat"}, "")
	if len(tg.sent) != 0 {
		t.Fatal("expected no TG message for empty text")
	}
	if len(cc.notified) != 0 {
		t.Fatal("expected no CC notification for empty text")
	}
}

func TestDispatch_ChatOutput(t *testing.T) {
	tg := &mockTG{}
	cc := &mockCC{}
	e := newTestEngine(tg, cc, t.TempDir())

	e.dispatch(&Job{Output: "chat", ID: "j1"}, "hello world")
	if len(tg.sent) != 1 || tg.sent[0] != "hello world" {
		t.Fatalf("expected TG message 'hello world', got %v", tg.sent)
	}
	if len(cc.notified) != 1 || cc.notified[0] != "hello world" {
		t.Fatalf("expected CC notification 'hello world', got %v", cc.notified)
	}
}

func TestDispatch_FileOutput(t *testing.T) {
	tg := &mockTG{}
	cc := &mockCC{}
	dir := t.TempDir()
	e := newTestEngine(tg, cc, dir)

	e.dispatch(&Job{Output: "file", ID: "j1", Name: "Test"}, "file content")
	if len(tg.sent) != 0 {
		t.Fatal("file output should not send to TG")
	}
	if len(cc.notified) != 0 {
		t.Fatal("file output should not notify CC")
	}
	// File write is best-effort; just verify no panic.
}

func TestDispatch_BothOutput(t *testing.T) {
	tg := &mockTG{}
	cc := &mockCC{}
	dir := t.TempDir()
	e := newTestEngine(tg, cc, dir)

	e.dispatch(&Job{Output: "both", ID: "j1", Name: "Test"}, "dual output")
	if len(tg.sent) != 1 {
		t.Fatalf("expected 1 TG message for both output, got %d", len(tg.sent))
	}
	if len(cc.notified) != 1 {
		t.Fatalf("expected 1 CC notification for both output, got %d", len(cc.notified))
	}
}

func TestDispatch_SilentOutput(t *testing.T) {
	tg := &mockTG{}
	cc := &mockCC{}
	e := newTestEngine(tg, cc, t.TempDir())

	e.dispatch(&Job{Output: "silent", ID: "j1"}, "should be ignored")
	if len(tg.sent) != 0 {
		t.Fatal("silent output should not send to TG")
	}
	if len(cc.notified) != 0 {
		t.Fatal("silent output should not notify CC")
	}
}

func TestDispatch_NoChannelsConfigured(t *testing.T) {
	e := &Engine{cfg: Config{DataDir: t.TempDir()}}
	// Should not panic when TG and CC are nil.
	e.dispatch(&Job{Output: "chat", ID: "j1"}, "no channels")
}
