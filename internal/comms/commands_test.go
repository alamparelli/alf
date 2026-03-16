package comms

import (
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/session"
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
