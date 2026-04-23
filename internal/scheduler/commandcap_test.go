package scheduler

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
)

// TestCommandCapability_Manifest pins the registry contract: ID, Kind,
// description — changing these silently would break every daemon that
// registers the Capability under a different name.
func TestCommandCapability_Manifest(t *testing.T) {
	c := NewCommandCapability("", "")
	m := c.Manifest()
	if m.ID != CommandCapabilityID {
		t.Fatalf("ID: got %q want %q", m.ID, CommandCapabilityID)
	}
	if m.Kind != capability.KindTool {
		t.Fatalf("Kind: got %v want KindTool", m.Kind)
	}
	if m.Description == "" {
		t.Fatal("Description must not be empty")
	}
}

// TestCommandCapability_Success covers the happy path: bash -c echoes,
// Execute returns the trimmed stdout as Output.Data and no error.
func TestCommandCapability_Success(t *testing.T) {
	c := NewCommandCapability("", "")
	out, err := c.Execute(context.Background(), capability.Input{"command": "echo hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Error != "" {
		t.Fatalf("unexpected Output.Error: %q", out.Error)
	}
	got, ok := out.Data.(string)
	if !ok {
		t.Fatalf("Output.Data type: got %T want string", out.Data)
	}
	if got != "hello" {
		t.Fatalf("output: got %q want %q", got, "hello")
	}
}

// TestCommandCapability_MissingCommand rejects an empty/whitespace command
// with both an error and a populated Output.Error — mirrors the shape the
// legacy runCommand caller has relied on.
func TestCommandCapability_MissingCommand(t *testing.T) {
	c := NewCommandCapability("", "")
	cases := []capability.Input{
		{},
		{"command": ""},
		{"command": "   "},
	}
	for i, in := range cases {
		out, err := c.Execute(context.Background(), in)
		if err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
		if out.Error == "" {
			t.Fatalf("case %d: expected populated Output.Error", i)
		}
	}
}

// TestCommandCapability_NonZeroExit turns a failing bash expression into the
// familiar "command failed" error, with stdout/stderr appended when present.
func TestCommandCapability_NonZeroExit(t *testing.T) {
	c := NewCommandCapability("", "")
	out, err := c.Execute(context.Background(), capability.Input{
		"command": "echo oops >&2; exit 3",
	})
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}
	if out.Error == "" || !strings.Contains(out.Error, "command failed") {
		t.Fatalf("Output.Error should contain 'command failed', got %q", out.Error)
	}
	if !strings.Contains(out.Error, "oops") {
		t.Fatalf("Output.Error should include captured stderr, got %q", out.Error)
	}
}

// TestCommandCapability_Timeout enforces the per-call deadline: a command
// that sleeps past the timeout must surface the "timed out" error.
func TestCommandCapability_Timeout(t *testing.T) {
	c := NewCommandCapability("", "")
	start := time.Now()
	out, err := c.Execute(context.Background(), capability.Input{
		"command": "sleep 5",
		"timeout": 50 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(out.Error, "timed out") {
		t.Fatalf("Output.Error should mention timed out, got %q", out.Error)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Execute did not honour the 50ms timeout (elapsed %v)", elapsed)
	}
}

// TestCommandCapability_TimeoutParseable verifies the duration-as-string form
// works too (scheduler passes time.Duration, but external callers may not).
func TestCommandCapability_TimeoutParseable(t *testing.T) {
	c := NewCommandCapability("", "")
	_, err := c.Execute(context.Background(), capability.Input{
		"command": "sleep 5",
		"timeout": "30ms",
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// TestCommandCapability_OutputTruncation caps captured output at 4000 bytes
// plus a truncation marker. A 5000-char payload must be cut to the marker.
func TestCommandCapability_OutputTruncation(t *testing.T) {
	c := NewCommandCapability("", "")
	// Emit 5000 'x' chars on stdout.
	out, err := c.Execute(context.Background(), capability.Input{
		"command": "printf 'x%.0s' {1..5000}",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, _ := out.Data.(string)
	if !strings.Contains(s, "... (truncated)") {
		t.Fatalf("expected truncation marker in output, got len=%d", len(s))
	}
	if len(s) > 4200 {
		t.Fatalf("output not truncated: len=%d", len(s))
	}
}

// TestCommandCapability_StripsSecrets verifies _TOKEN / CLAUDE_* env vars are
// not leaked into the child process — this is the same guard that
// Engine.commandEnv enforces today.
func TestCommandCapability_StripsSecrets(t *testing.T) {
	t.Setenv("SOMETHING_TOKEN", "super-secret")
	t.Setenv("CLAUDE_API_KEY", "also-secret")
	t.Setenv("ALF_VISIBLE", "keep-me")

	c := NewCommandCapability("", "")
	// Probe each var individually so we're immune to env-size truncation
	// on a heavy test host.
	probe := func(name string) string {
		out, err := c.Execute(context.Background(), capability.Input{
			"command": "printenv " + name + " || true",
		})
		if err != nil {
			t.Fatalf("probe %s: %v", name, err)
		}
		s, _ := out.Data.(string)
		return s
	}
	if probe("SOMETHING_TOKEN") != "" {
		t.Fatal("secret _TOKEN leaked into child env")
	}
	if probe("CLAUDE_API_KEY") != "" {
		t.Fatal("CLAUDE_ secret leaked into child env")
	}
	if probe("ALF_VISIBLE") != "keep-me" {
		t.Fatal("non-secret env should be forwarded")
	}
}

// TestCommandCapability_SignalSock injects ALF_SIGNAL_SOCK when configured.
func TestCommandCapability_SignalSock(t *testing.T) {
	c := NewCommandCapability("", "/tmp/alf.sock")
	out, err := c.Execute(context.Background(), capability.Input{
		"command": "printenv ALF_SIGNAL_SOCK",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, _ := out.Data.(string)
	if s != "/tmp/alf.sock" {
		t.Fatalf("ALF_SIGNAL_SOCK: got %q want %q", s, "/tmp/alf.sock")
	}
}

// TestCommandCapability_PathEnrichment adds dataDir/tools[.d] to PATH when
// configured. The child prints $PATH; we assert the suffix landed in.
func TestCommandCapability_PathEnrichment(t *testing.T) {
	dataDir, err := os.MkdirTemp("", "cmdcap-")
	if err != nil {
		t.Fatalf("tmp dir: %v", err)
	}
	defer os.RemoveAll(dataDir)

	c := NewCommandCapability(dataDir, "")
	out, err := c.Execute(context.Background(), capability.Input{
		"command": "printenv PATH",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, _ := out.Data.(string)
	if !strings.Contains(s, dataDir+"/tools.d") || !strings.Contains(s, dataDir+"/tools") {
		t.Fatalf("PATH missing enriched suffixes, got %q", s)
	}
}
