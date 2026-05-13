package tooling

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/sandbox/integrity"
)

// capStub implements capability.Capability for the wasm-tool dispatch tests
// without spinning up a real wasm.Adapter. Captures the last Execute input
// so the test can assert the args round-tripped intact.
type capStub struct {
	id          capability.ID
	out         capability.Output
	err         error
	gotInput    capability.Input
	gotInputCnt int
}

func (c *capStub) Manifest() capability.Manifest {
	return capability.Manifest{ID: c.id, Kind: capability.KindTool, Name: string(c.id)}
}
func (c *capStub) Permissions() capability.PermissionSet { return capability.PermissionSet{} }
func (c *capStub) Execute(_ context.Context, in capability.Input) (capability.Output, error) {
	c.gotInput = in
	c.gotInputCnt++
	return c.out, c.err
}

func TestExecutor_ToolNotFound(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tools.d"), 0o755)

	e := &Executor{
		DataDir: dir,
		HomeDir: dir,
		Timeout: 5 * time.Second,
	}

	result := e.Execute(context.Background(), CallRequest{
		ID:        "call_1",
		Name:      "nonexistent",
		Arguments: "{}",
	})

	if !result.IsError {
		t.Error("expected error for missing tool")
	}
	if result.ID != "call_1" {
		t.Errorf("expected ID 'call_1', got %q", result.ID)
	}
}

func TestExecutor_RunsTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	toolsD := filepath.Join(dir, "tools.d")
	os.MkdirAll(toolsD, 0o755)

	// Create a simple echo tool that reads stdin and outputs it.
	script := "#!/bin/sh\ncat\n"
	toolPath := filepath.Join(toolsD, "echo-tool")
	os.WriteFile(toolPath, []byte(script), 0o755)

	e := &Executor{
		DataDir: dir,
		HomeDir: dir,
		Timeout: 5 * time.Second,
	}

	result := e.Execute(context.Background(), CallRequest{
		ID:        "call_2",
		Name:      "echo-tool",
		Arguments: `{"query": "test"}`,
	})

	if result.IsError {
		t.Errorf("unexpected error: %s", result.Output)
	}
	if result.Output != `{"query": "test"}` {
		t.Errorf("expected stdin piped to stdout, got %q", result.Output)
	}
}

func TestExecutor_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	toolsD := filepath.Join(dir, "tools.d")
	os.MkdirAll(toolsD, 0o755)

	script := "#!/bin/sh\nsleep 30\n"
	toolPath := filepath.Join(toolsD, "slow-tool")
	os.WriteFile(toolPath, []byte(script), 0o755)

	e := &Executor{
		DataDir: dir,
		HomeDir: dir,
		Timeout: 100 * time.Millisecond,
	}

	result := e.Execute(context.Background(), CallRequest{
		ID:        "call_3",
		Name:      "slow-tool",
		Arguments: "{}",
	})

	if !result.IsError {
		t.Error("expected timeout error")
	}
}

func TestExecutor_NonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	toolsD := filepath.Join(dir, "tools.d")
	os.MkdirAll(toolsD, 0o755)

	script := "#!/bin/sh\necho 'something went wrong' >&2\nexit 1\n"
	toolPath := filepath.Join(toolsD, "fail-tool")
	os.WriteFile(toolPath, []byte(script), 0o755)

	e := &Executor{
		DataDir: dir,
		HomeDir: dir,
		Timeout: 5 * time.Second,
	}

	result := e.Execute(context.Background(), CallRequest{
		ID:        "call_4",
		Name:      "fail-tool",
		Arguments: "{}",
	})

	if !result.IsError {
		t.Error("expected error for non-zero exit")
	}
}

// --- Environment regression tests (#122) ---

// TestExecutor_EnvPropagated verifies that Env entries on the Executor are
// passed through to tool subprocesses. Regression for #122 where
// ALF_SIGNAL_SOCK was never reaching the notify tool.
func TestExecutor_EnvPropagated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	toolsD := filepath.Join(dir, "tools.d")
	os.MkdirAll(toolsD, 0o755)

	// Tool that prints ALF_SIGNAL_SOCK to stdout.
	script := "#!/bin/sh\necho \"$ALF_SIGNAL_SOCK\"\n"
	os.WriteFile(filepath.Join(toolsD, "env-check"), []byte(script), 0o755)

	e := &Executor{
		DataDir: dir,
		HomeDir: dir,
		Timeout: 5 * time.Second,
		Env:     []string{"ALF_SIGNAL_SOCK=/tmp/test-signal.sock"},
	}

	result := e.Execute(context.Background(), CallRequest{
		ID:        "env_1",
		Name:      "env-check",
		Arguments: "{}",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	want := "/tmp/test-signal.sock"
	got := strings.TrimSpace(result.Output)
	if got != want {
		t.Errorf("ALF_SIGNAL_SOCK = %q, want %q", got, want)
	}
}

// TestExecutor_EnvNil_NoSignalSock verifies that when Env is nil,
// ALF_SIGNAL_SOCK is absent (old broken behavior).
func TestExecutor_EnvNil_NoSignalSock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	toolsD := filepath.Join(dir, "tools.d")
	os.MkdirAll(toolsD, 0o755)

	script := "#!/bin/sh\necho \"${ALF_SIGNAL_SOCK:-UNSET}\"\n"
	os.WriteFile(filepath.Join(toolsD, "env-check2"), []byte(script), 0o755)

	e := &Executor{
		DataDir: dir,
		HomeDir: dir,
		Timeout: 5 * time.Second,
		Env:     nil, // no signal sock
	}

	result := e.Execute(context.Background(), CallRequest{
		ID:   "env_2",
		Name: "env-check2",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	if got := strings.TrimSpace(result.Output); got != "UNSET" {
		t.Errorf("expected UNSET, got %q", got)
	}
}

// --- Security regression tests ---

// TestExecutor_DropToAlfUser_Linux verifies that on Linux, user tool subprocesses
// get SysProcAttr with uid 1000 credentials (not the daemon's uid 1001).
// Regression test for privilege escalation: user tools must never run as daemon.
func TestExecutor_DropToAlfUser_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("credential drop only active on Linux")
	}

	cmd := exec.Command("/bin/true")
	dropToAlfUser(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr should be set on Linux")
	}
	if cmd.SysProcAttr.Credential == nil {
		t.Fatal("Credential should be set on Linux")
	}
	if cmd.SysProcAttr.Credential.Uid != 1000 {
		t.Errorf("Uid = %d, want 1000", cmd.SysProcAttr.Credential.Uid)
	}
	if cmd.SysProcAttr.Credential.Gid != 1000 {
		t.Errorf("Gid = %d, want 1000", cmd.SysProcAttr.Credential.Gid)
	}
}

// TestExecutor_DropToAlfUser_Noop_NonLinux verifies that dropToAlfUser is a
// no-op on non-Linux platforms (macOS dev/test).
func TestExecutor_DropToAlfUser_Noop_NonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("this test checks non-Linux behavior")
	}

	cmd := exec.Command("/bin/true")
	dropToAlfUser(cmd)

	if cmd.SysProcAttr != nil {
		t.Error("SysProcAttr should not be set on non-Linux")
	}
}

// TestExecutor_PathTraversal_Regression verifies that tool names with path
// traversal sequences are rejected.
func TestExecutor_PathTraversal_Regression(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tools.d"), 0o755)

	e := &Executor{DataDir: dir, HomeDir: dir}

	for _, name := range []string{"../etc/passwd", "foo/bar", "..\\windows", "tools/../secret"} {
		result := e.Execute(context.Background(), CallRequest{
			ID:   "trav",
			Name: name,
		})
		if !result.IsError {
			t.Errorf("expected path traversal %q to be rejected", name)
		}
	}
}

// TestExecutor_QuarantinedToolBlocked verifies that quarantined tools cannot be executed.
func TestExecutor_QuarantinedToolBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	os.MkdirAll(toolsDir, 0o755)

	// Create a tool.
	script := "#!/bin/sh\necho pwned\n"
	os.WriteFile(filepath.Join(toolsDir, "evil-tool"), []byte(script), 0o755)

	// Create integrity guard with tool already quarantined.
	ig := integrity.NewTestGuardWithQuarantine(map[string]integrity.QuarantinedTool{"evil-tool": {}})

	e := &Executor{
		DataDir:   dir,
		HomeDir:   dir,
		Integrity: ig,
		Timeout:   5 * time.Second,
	}

	result := e.Execute(context.Background(), CallRequest{
		ID:   "q1",
		Name: "evil-tool",
	})

	if !result.IsError {
		t.Error("quarantined tool should be blocked")
	}
	if !containsSubstring(result.Output, "quarantined") {
		t.Errorf("error should mention quarantine, got: %s", result.Output)
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && findSubstring(s, sub)
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestExecutor_CapabilityDispatch_DesanitizesName pins the wasm-tool
// LLM-to-daemon name flip. The LLM sees "http_hello" (sanitised per
// Anthropic's ^[a-zA-Z0-9_]{1,64}$ rule), but the capability registry
// holds "http-hello" (the bundle's original id). Without the
// desanitisation step in lookupCapability, the ToolLoop dispatches
// http_hello, the executor finds nothing, and returns "not found" —
// which is exactly what bit #425 smoke.
func TestExecutor_CapabilityDispatch_DesanitizesName(t *testing.T) {
	capReg := capability.NewRegistry()
	stub := &capStub{id: "http-hello", out: capability.Output{Data: map[string]any{"status": 200, "body": "ok"}}}
	if err := capReg.Register(stub); err != nil {
		t.Fatalf("register: %v", err)
	}

	reg := NewRegistry(t.TempDir())
	reg.SetCapabilityRegistry(capReg)
	reg.RegisterWasmTool(ToolSchema{Name: "http-hello", Description: "x"}, stub)

	exec := &Executor{
		DataDir:  t.TempDir(),
		HomeDir:  t.TempDir(),
		Registry: reg,
		Timeout:  5 * time.Second,
	}

	result := exec.Execute(context.Background(), CallRequest{
		ID:        "call_1",
		Name:      "http_hello", // sanitised, as the LLM emits it
		Arguments: `{"url":"https://httpbin.org/get"}`,
	})

	if result.IsError {
		t.Fatalf("dispatch failed: output=%q errMsg=%q", result.Output, result.ErrorMessage)
	}
	if stub.gotInputCnt != 1 {
		t.Errorf("capability.Execute called %d times, want 1", stub.gotInputCnt)
	}
	if got := stub.gotInput["url"]; got != "https://httpbin.org/get" {
		t.Errorf("arguments lost in dispatch; got url=%v", got)
	}
	// Output.Data was a map → serialised as JSON.
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.Output), &decoded); err != nil {
		t.Errorf("Output not valid JSON: %v (output=%q)", err, result.Output)
	}
	if v := decoded["status"]; v != float64(200) {
		t.Errorf("Output.status=%v, want 200", v)
	}
}

// TestExecutor_CapabilityDispatch_StringOutputPassesThrough pins the
// string-output shortcut: when a capability returns Output.Data as a
// string, executor must NOT json-quote it again — the ToolLoop already
// owns the wrapping. Skills that emit plain reasoning text or pre-
// formatted markdown would otherwise reach the LLM as `"…"` and break
// rendering / cache-key alignment.
func TestExecutor_CapabilityDispatch_StringOutputPassesThrough(t *testing.T) {
	capReg := capability.NewRegistry()
	stub := &capStub{id: "echo-tool", out: capability.Output{Data: "hello world"}}
	if err := capReg.Register(stub); err != nil {
		t.Fatalf("register: %v", err)
	}

	reg := NewRegistry(t.TempDir())
	reg.SetCapabilityRegistry(capReg)
	reg.RegisterWasmTool(ToolSchema{Name: "echo-tool", Description: "x"}, stub)

	exec := &Executor{
		DataDir:  t.TempDir(),
		HomeDir:  t.TempDir(),
		Registry: reg,
		Timeout:  5 * time.Second,
	}

	result := exec.Execute(context.Background(), CallRequest{
		ID:        "call_2",
		Name:      "echo_tool",
		Arguments: "{}",
	})

	if result.IsError {
		t.Fatalf("dispatch failed: %v", result.ErrorMessage)
	}
	if result.Output != "hello world" {
		t.Errorf("Output=%q, want %q (string re-quoted by accident?)", result.Output, "hello world")
	}
}

// TestExecutor_CapabilityDispatch_ErrorPath pins the error surface:
// capability.Execute returning a non-nil error or a non-empty
// Output.Error must bubble up as IsError so the LLM sees a tool failure
// (not a silent success that hides bugs in the bundle).
func TestExecutor_CapabilityDispatch_ErrorPath(t *testing.T) {
	capReg := capability.NewRegistry()
	stub := &capStub{id: "fail-tool", err: errors.New("boom")}
	if err := capReg.Register(stub); err != nil {
		t.Fatalf("register: %v", err)
	}

	reg := NewRegistry(t.TempDir())
	reg.SetCapabilityRegistry(capReg)
	reg.RegisterWasmTool(ToolSchema{Name: "fail-tool", Description: "x"}, stub)

	exec := &Executor{
		DataDir:  t.TempDir(),
		HomeDir:  t.TempDir(),
		Registry: reg,
		Timeout:  5 * time.Second,
	}

	result := exec.Execute(context.Background(), CallRequest{
		ID:        "call_3",
		Name:      "fail_tool",
		Arguments: "{}",
	})

	if !result.IsError {
		t.Fatal("error from capability.Execute should surface as IsError")
	}
	if !strings.Contains(result.ErrorMessage, "boom") {
		t.Errorf("ErrorMessage should mention the underlying error; got %q", result.ErrorMessage)
	}
}
