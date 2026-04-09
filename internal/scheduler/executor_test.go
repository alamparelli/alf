package scheduler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// --- runCommand env tests ---

func TestRunCommand_SignalSockPath_PassedToSubprocess(t *testing.T) {
	sockPath := "/tmp/test-signal.sock"
	e := &Engine{
		cfg: Config{
			DataDir:        t.TempDir(),
			SignalSockPath: sockPath,
		},
	}
	j := &Job{
		ID:      "test-env",
		Command: "printenv ALF_SIGNAL_SOCK",
		Timeout: 5 * time.Second,
	}

	out, err := e.runCommand(j)
	if err != nil {
		t.Fatalf("runCommand failed: %v", err)
	}
	if out != sockPath {
		t.Fatalf("expected ALF_SIGNAL_SOCK=%q, got %q", sockPath, out)
	}
}

func TestRunCommand_ToolsInPATH(t *testing.T) {
	dataDir := t.TempDir()
	e := &Engine{cfg: Config{DataDir: dataDir}}

	j := &Job{
		ID:      "test-path",
		Command: "echo $PATH",
		Timeout: 5 * time.Second,
	}

	out, err := e.runCommand(j)
	if err != nil {
		t.Fatalf("runCommand failed: %v", err)
	}

	toolsDir := filepath.Join(dataDir, "tools")
	toolsDDir := filepath.Join(dataDir, "tools.d")
	if !strings.Contains(out, toolsDir) {
		t.Errorf("PATH missing %s, got: %s", toolsDir, out)
	}
	if !strings.Contains(out, toolsDDir) {
		t.Errorf("PATH missing %s, got: %s", toolsDDir, out)
	}
	// tools.d should come before tools
	if strings.Index(out, toolsDDir) > strings.Index(out, toolsDir) {
		t.Errorf("tools.d should precede tools in PATH, got: %s", out)
	}
}

func TestRunCommand_ToolsExecutable(t *testing.T) {
	dataDir := t.TempDir()
	toolsDir := filepath.Join(dataDir, "tools")
	os.MkdirAll(toolsDir, 0o755)

	// Create a fake tool script
	script := filepath.Join(toolsDir, "my-tool")
	os.WriteFile(script, []byte("#!/bin/sh\necho tool-ok"), 0o755)

	e := &Engine{cfg: Config{DataDir: dataDir}}
	j := &Job{
		ID:      "test-tool-exec",
		Command: "my-tool",
		Timeout: 5 * time.Second,
	}

	out, err := e.runCommand(j)
	if err != nil {
		t.Fatalf("runCommand failed: %v", err)
	}
	if out != "tool-ok" {
		t.Fatalf("expected 'tool-ok', got %q", out)
	}
}

func TestRunCommand_SignalSockPath_EmptyNotSet(t *testing.T) {
	e := &Engine{
		cfg: Config{
			DataDir:        t.TempDir(),
			SignalSockPath: "",
		},
	}
	j := &Job{
		ID:      "test-env-empty",
		Command: "printenv ALF_SIGNAL_SOCK || echo NOTSET",
		Timeout: 5 * time.Second,
	}

	out, err := e.runCommand(j)
	if err != nil {
		t.Fatalf("runCommand failed: %v", err)
	}
	if out != "NOTSET" {
		t.Fatalf("expected ALF_SIGNAL_SOCK to be unset, got %q", out)
	}
}

func TestCommandEnv_ExcludesSecrets(t *testing.T) {
	// Set secrets matching various suffix/prefix patterns.
	t.Setenv("TELEGRAM_BOT_TOKEN", "secret-token")
	t.Setenv("CC_AUTH_TOKEN", "secret-auth")
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("VAULT_MASTER_PASSWORD", "vault-pw")
	t.Setenv("EMBED_SHARED_SECRET", "embed-sec")
	t.Setenv("CLAUDE_CODE_CONFIG", "claude-cfg")
	// Non-secret vars should pass through.
	t.Setenv("HOME", "/home/alf")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("REDDIT_CLIENT_ID", "reddit-id") // no secret suffix

	e := &Engine{cfg: Config{DataDir: t.TempDir(), SignalSockPath: "/tmp/sig.sock"}}
	env := e.commandEnv()

	envMap := make(map[string]string)
	for _, v := range env {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Safe vars should be present.
	if envMap["HOME"] != "/home/alf" {
		t.Errorf("expected HOME=/home/alf, got %q", envMap["HOME"])
	}
	if envMap["LANG"] != "en_US.UTF-8" {
		t.Errorf("expected LANG=en_US.UTF-8, got %q", envMap["LANG"])
	}
	if envMap["ALF_SIGNAL_SOCK"] != "/tmp/sig.sock" {
		t.Errorf("expected ALF_SIGNAL_SOCK=/tmp/sig.sock, got %q", envMap["ALF_SIGNAL_SOCK"])
	}
	if envMap["REDDIT_CLIENT_ID"] != "reddit-id" {
		t.Errorf("expected REDDIT_CLIENT_ID to pass through, got %q", envMap["REDDIT_CLIENT_ID"])
	}

	// Secrets must NOT be present.
	for _, forbidden := range []string{
		"TELEGRAM_BOT_TOKEN", "CC_AUTH_TOKEN", "ANTHROPIC_API_KEY",
		"VAULT_MASTER_PASSWORD", "EMBED_SHARED_SECRET", "CLAUDE_CODE_CONFIG",
	} {
		if _, ok := envMap[forbidden]; ok {
			t.Errorf("secret %s leaked into commandEnv", forbidden)
		}
	}
}

func TestIsSecretEnv(t *testing.T) {
	cases := []struct {
		kv     string
		secret bool
	}{
		{"TELEGRAM_BOT_TOKEN=abc", true},
		{"CC_AUTH_TOKEN=xyz", true},
		{"ANTHROPIC_API_KEY=sk-123", true},
		{"VAULT_MASTER_PASSWORD=pw", true},
		{"WHISPER_SHARED_SECRET=sec", true},
		{"CLAUDE_CODE_CONFIG=/path", true},
		{"CLAUDE_OAUTH=tok", true},
		{"HOME=/home/alf", false},
		{"PATH=/usr/bin", false},
		{"REDDIT_CLIENT_ID=abc", false},
		{"LANG=en_US.UTF-8", false},
		{"MY_CUSTOM_VAR=hello", false},
	}
	for _, tc := range cases {
		if got := isSecretEnv(tc.kv); got != tc.secret {
			t.Errorf("isSecretEnv(%q) = %v, want %v", tc.kv, got, tc.secret)
		}
	}
}

func TestCommandEnv_PATHIncludesToolDirs(t *testing.T) {
	dataDir := t.TempDir()
	e := &Engine{cfg: Config{DataDir: dataDir}}
	env := e.commandEnv()

	var pathVal string
	for _, v := range env {
		if strings.HasPrefix(v, "PATH=") {
			pathVal = strings.TrimPrefix(v, "PATH=")
			break
		}
	}

	toolsD := filepath.Join(dataDir, "tools.d")
	tools := filepath.Join(dataDir, "tools")
	if !strings.Contains(pathVal, toolsD) {
		t.Errorf("PATH missing tools.d dir, got: %s", pathVal)
	}
	if !strings.Contains(pathVal, tools) {
		t.Errorf("PATH missing tools dir, got: %s", pathVal)
	}
}

func TestDispatch_NoChannelsConfigured(t *testing.T) {
	e := &Engine{cfg: Config{DataDir: t.TempDir()}}
	// Should not panic when TG and CC are nil.
	e.dispatch(&Job{Output: "chat", ID: "j1"}, "no channels")
}
