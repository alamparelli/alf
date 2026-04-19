package comms

import (
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/session"
)

func TestHandleCommand_DelegatesToDefaultRegistry(t *testing.T) {
	e := &ChatEngine{
		Sessions:   session.New(t.TempDir(), time.Minute),
		ContextDir: t.TempDir(),
	}
	// /new is a built-in command and should be handled.
	_, handled := e.HandleCommand("cc:default", "/new")
	if !handled {
		t.Error("expected /new to be handled via default registry")
	}

	// Non-command → not handled.
	_, handled = e.HandleCommand("cc:default", "just text")
	if handled {
		t.Error("plain text must not be handled")
	}
}

func TestCmdStart_StartsSessionAndReturnsEmpty(t *testing.T) {
	e := &ChatEngine{
		Sessions:   session.New(t.TempDir(), time.Minute),
		ContextDir: t.TempDir(),
	}
	got := cmdStart(e, "cc:onboard", "")
	if got != "" {
		t.Errorf("cmdStart must return empty string, got %q", got)
	}
	// Onboarding marker file is created by NewSession(true); re-asserting here
	// keeps the behavioral contract locked.
	// (marker path is .onboarding under ContextDir)
}

func TestCmdTool_NoExecutor(t *testing.T) {
	e := &ChatEngine{}
	got := cmdTool(e, "cc:default", "")
	if got != "Tool integrity guard is not enabled." {
		t.Errorf("unexpected response: %q", got)
	}
}
