package comms

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/platform/eventlog"
	"github.com/alamparelli/alf/internal/platform/session"
)

func TestCommandRegistry_Register(t *testing.T) {
	r := NewCommandRegistry()

	// Built-in commands should exist.
	if _, ok := r.Get("new"); !ok {
		t.Error("expected 'new' command to be registered")
	}
	if _, ok := r.Get("clear"); !ok {
		t.Error("expected 'clear' command to be registered")
	}
	if _, ok := r.Get("skills"); !ok {
		t.Error("expected 'skills' command to be registered")
	}
}

func TestCommandRegistry_Unknown(t *testing.T) {
	r := NewCommandRegistry()

	_, handled := r.Dispatch(nil, "cc:default", "nonexistent", "")
	if handled {
		t.Error("expected unknown command to not be handled")
	}
}

func TestCommand_New(t *testing.T) {
	dir := t.TempDir()
	sessions := session.New(dir, 30*time.Minute)
	sessions.Set(-1, "old-session")
	sessions.AddSkills(-1, []string{"test-skill"})

	e := &ChatEngine{
		Sessions:   sessions,
		ContextDir: t.TempDir(),
	}

	result := cmdNew(e, "cc:default", "")
	if result == "" {
		t.Error("expected non-empty response")
	}

	// Session should be archived.
	if sid := sessions.Get(-1); sid != "" {
		t.Errorf("expected session to be archived, got %q", sid)
	}

	// Skills should be cleared.
	if sk := sessions.GetSkills(-1); len(sk) > 0 {
		t.Errorf("expected skills to be cleared, got %v", sk)
	}
}

func TestCommand_Skills(t *testing.T) {
	dir := t.TempDir()
	sessions := session.New(dir, 30*time.Minute)

	e := &ChatEngine{Sessions: sessions}

	// No skills active.
	result := cmdSkills(e, "cc:default", "")
	if result != "No skills active in this session.\n\nUse /skills clear to reset." {
		t.Errorf("unexpected result: %q", result)
	}

	// Add skills and check.
	sessions.AddSkills(-1, []string{"code-review", "security"})
	result = cmdSkills(e, "cc:default", "")
	if result == "" {
		t.Error("expected non-empty result with active skills")
	}
}

func TestCommand_SkillsClear(t *testing.T) {
	dir := t.TempDir()
	sessions := session.New(dir, 30*time.Minute)
	sessions.AddSkills(-1, []string{"test-skill"})

	e := &ChatEngine{Sessions: sessions}

	result := cmdSkills(e, "cc:default", "clear")
	if result != "Active skills cleared from session." {
		t.Errorf("unexpected result: %q", result)
	}

	if sk := sessions.GetSkills(-1); len(sk) > 0 {
		t.Errorf("expected skills to be cleared, got %v", sk)
	}
}

func TestNewSession_FiresOnSessionEnd(t *testing.T) {
	dir := t.TempDir()
	sessions := session.New(dir, 30*time.Minute)
	chID := ChannelID("cc:test-conv")
	key := chID.SessionKey()
	sessions.Set(key, "session-abc")
	sessions.AddSkills(key, []string{"test-skill"})

	var firedWith string
	e := &ChatEngine{
		Sessions:     sessions,
		ContextDir:   t.TempDir(),
		OnSessionEnd: func(sid string) { firedWith = sid },
	}

	old := e.NewSession(chID, false)

	if old != "session-abc" {
		t.Errorf("expected old session 'session-abc', got %q", old)
	}
	if firedWith != "session-abc" {
		t.Errorf("expected OnSessionEnd fired with 'session-abc', got %q", firedWith)
	}
	// Skills should be cleared.
	if sk := sessions.GetSkills(key); len(sk) > 0 {
		t.Errorf("expected skills cleared, got %v", sk)
	}
}

func TestNewSession_NoFireWhenNoOldSession(t *testing.T) {
	dir := t.TempDir()
	sessions := session.New(dir, 30*time.Minute)

	fired := false
	e := &ChatEngine{
		Sessions:     sessions,
		ContextDir:   t.TempDir(),
		OnSessionEnd: func(sid string) { fired = true },
	}

	old := e.NewSession("cc:-1", false)

	if old != "" {
		t.Errorf("expected empty old session, got %q", old)
	}
	if fired {
		t.Error("OnSessionEnd should not fire when there's no old session")
	}
}

func TestNewSession_CmdNewDelegatesToEngine(t *testing.T) {
	dir := t.TempDir()
	sessions := session.New(dir, 30*time.Minute)
	chID := ChannelID("cc:test-conv2")
	key := chID.SessionKey()
	sessions.Set(key, "old-sess")

	var firedWith string
	e := &ChatEngine{
		Sessions:     sessions,
		ContextDir:   t.TempDir(),
		OnSessionEnd: func(sid string) { firedWith = sid },
	}

	result := cmdNew(e, chID, "")

	if result != "Previous session archived. New session started." {
		t.Errorf("unexpected result: %q", result)
	}
	if firedWith != "old-sess" {
		t.Errorf("expected OnSessionEnd via cmdNew, got %q", firedWith)
	}
}

func TestNewSession_EventLog(t *testing.T) {
	dir := t.TempDir()
	sessions := session.New(dir, 30*time.Minute)
	chID := ChannelID("cc:test-conv3")
	key := chID.SessionKey()
	sessions.Set(key, "session-log-test")

	dataDir := t.TempDir()
	el := eventlog.New(dataDir)
	defer el.Close()

	e := &ChatEngine{
		Sessions:   sessions,
		ContextDir: t.TempDir(),
		EventLog:   el,
	}

	old := e.NewSession(chID, false)
	if old != "session-log-test" {
		t.Fatalf("expected old session 'session-log-test', got %q", old)
	}

	el.Close() // flush

	// Read the event log file and verify the session_archived event.
	entries, err := filepath.Glob(filepath.Join(dataDir, "logs", "events", "*.jsonl"))
	if err != nil || len(entries) == 0 {
		t.Fatal("expected event log file to exist")
	}

	data, err := os.ReadFile(entries[0])
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec["event"] == "session_archived" && rec["old_session_id"] == "session-log-test" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected session_archived event in log, not found")
	}
}

func TestNewSession_NoEventLogWhenNoOldSession(t *testing.T) {
	dir := t.TempDir()
	sessions := session.New(dir, 30*time.Minute)
	// No session set -- old will be empty.

	dataDir := t.TempDir()
	el := eventlog.New(dataDir)
	defer el.Close()

	e := &ChatEngine{
		Sessions:   sessions,
		ContextDir: t.TempDir(),
		EventLog:   el,
	}

	old := e.NewSession("cc:-1", false)
	if old != "" {
		t.Fatalf("expected empty old session, got %q", old)
	}

	el.Close()

	entries, _ := filepath.Glob(filepath.Join(dataDir, "logs", "events", "*.jsonl"))
	if len(entries) > 0 {
		data, _ := os.ReadFile(entries[0])
		if strings.Contains(string(data), "session_archived") {
			t.Error("session_archived should not be logged when no old session exists")
		}
	}
}

func TestNewSession_OnboardSetsOnboarding(t *testing.T) {
	dir := t.TempDir()
	sessions := session.New(dir, 30*time.Minute)
	contextDir := t.TempDir()

	e := &ChatEngine{
		Sessions:   sessions,
		ContextDir: contextDir,
	}

	e.NewSession("cc:-1", true)

	// Verify onboarding file exists (memory.SetOnboarding creates a marker file).
	onboardPath := filepath.Join(contextDir, ".onboarding")
	if _, err := os.Stat(onboardPath); os.IsNotExist(err) {
		t.Error("expected onboarding marker file to be created")
	}
}

func TestCheckForceCommand(t *testing.T) {
	tiers := []TierInfo{
		{Name: "fast", ForceCommand: false},
		{Name: "smart", ForceCommand: true},
		{Name: "opus", ForceCommand: true},
	}

	tests := []struct {
		text     string
		wantTier string
		wantMsg  string
	}{
		{"/smart hello world", "smart", "hello world"},
		{"/smart", "smart", ""},
		{"/opus do something", "opus", "do something"},
		{"/fast hello", "", ""},  // not a force command
		{"hello", "", ""},        // not a command
		{"/unknown hi", "", ""},  // unknown tier
	}

	for _, tt := range tests {
		tier, msg := CheckForceCommand(tt.text, tiers)
		if tier != tt.wantTier || msg != tt.wantMsg {
			t.Errorf("CheckForceCommand(%q) = (%q, %q), want (%q, %q)", tt.text, tier, msg, tt.wantTier, tt.wantMsg)
		}
	}
}

func TestProcessCommand(t *testing.T) {
	dir := t.TempDir()
	sessions := session.New(dir, 30*time.Minute)

	e := &ChatEngine{
		Sessions:   sessions,
		ContextDir: t.TempDir(),
	}
	registry := NewCommandRegistry()

	// Regular text - not handled.
	_, handled := e.ProcessCommand("cc:default", "hello", registry)
	if handled {
		t.Error("expected regular text to not be handled")
	}

	// /new command - handled.
	_, handled = e.ProcessCommand("cc:default", "/new", registry)
	if !handled {
		t.Error("expected /new to be handled")
	}

	// /unknown - not handled.
	_, handled = e.ProcessCommand("cc:default", "/unknown", registry)
	if handled {
		t.Error("expected /unknown to not be handled")
	}
}

func TestHandleResume(t *testing.T) {
	dir := t.TempDir()
	sessions := session.New(dir, 30*time.Minute)

	e := &ChatEngine{Sessions: sessions}

	// /resume with no active session → error.
	_, isResume, errMsg := e.HandleResume("cc:default", "/resume")
	if isResume {
		t.Error("expected isResume=false when no session")
	}
	if errMsg == "" {
		t.Error("expected error message when no session")
	}

	// Set an active session.
	sessions.Set(-1, "session-abc")

	// /resume with no args → default prompt.
	text, isResume, errMsg := e.HandleResume("cc:default", "/resume")
	if !isResume {
		t.Error("expected isResume=true")
	}
	if errMsg != "" {
		t.Errorf("unexpected error: %s", errMsg)
	}
	if text != ResumePrompt {
		t.Errorf("expected default resume prompt, got %q", text)
	}

	// /resume with custom args.
	text, isResume, errMsg = e.HandleResume("cc:default", "/resume fix the bug")
	if !isResume {
		t.Error("expected isResume=true")
	}
	if errMsg != "" {
		t.Errorf("unexpected error: %s", errMsg)
	}
	if text != "fix the bug" {
		t.Errorf("expected 'fix the bug', got %q", text)
	}

	// Non-resume message → passthrough.
	text, isResume, errMsg = e.HandleResume("cc:default", "hello")
	if isResume {
		t.Error("expected isResume=false for regular text")
	}
	if text != "hello" {
		t.Errorf("expected passthrough text, got %q", text)
	}
}
