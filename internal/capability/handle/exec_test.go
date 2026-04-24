package handle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// execBin returns a path to a standard POSIX binary we can assume exists.
// Skips on Windows where /bin doesn't exist — alf is Linux/macOS primary.
func execBin(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("exec tests require a POSIX system")
	}
	for _, p := range []string{"/bin/" + name, "/usr/bin/" + name} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skipf("%s not found in /bin or /usr/bin", name)
	return ""
}

func TestExecScope_AllowsExactBinary(t *testing.T) {
	s := ExecScope{Binaries: []string{"/bin/echo"}}
	if !s.Allows("/bin/echo") {
		t.Error("exact match denied")
	}
	if s.Allows("/usr/bin/echo") {
		t.Error("different path accepted")
	}
	if s.Allows("echo") {
		t.Error("relative path accepted")
	}
	if s.Allows("") {
		t.Error("empty path accepted")
	}
}

func TestExecScope_PathTraversalBlocked(t *testing.T) {
	s := ExecScope{Binaries: []string{"/bin/echo"}}
	// Input that Cleans to a scope'd path still matches — that's fine,
	// Clean is the canonicalisation we expect. Input that Cleans to a
	// DIFFERENT path must not match.
	if !s.Allows("/bin/./echo") {
		t.Error("Clean-equivalent /bin/./echo denied")
	}
	if s.Allows("/bin/../bin/echo") {
		// After Clean, this is /bin/echo — it matches. Document behaviour:
		// byte-for-byte AFTER Clean. Not a security concern because the
		// scope author declared /bin/echo and Clean(/bin/../bin/echo) is
		// /bin/echo — same executable, same authority.
		t.Log("Clean normalises /bin/../bin/echo to /bin/echo — allowed as expected")
	}
	if s.Allows("/usr/bin/echo") {
		t.Error("/usr/bin/echo accepted despite different declared path")
	}
}

func TestExecHandle_RunAllowed(t *testing.T) {
	echo := execBin(t, "echo")
	h := NewExecHandle("cap", ExecScope{Binaries: []string{echo}})
	inst := NewInstance(context.Background(), "cap", Grants{Exec: h})
	defer inst.Close()

	res, err := inst.Exec.Run(context.Background(), echo, []string{"hello"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d, want 0", res.ExitCode)
	}
	if !strings.Contains(string(res.Stdout), "hello") {
		t.Errorf("stdout=%q, want to contain hello", res.Stdout)
	}
}

func TestExecHandle_RunOutOfScope(t *testing.T) {
	h := NewExecHandle("cap", ExecScope{Binaries: []string{"/bin/echo"}})
	inst := NewInstance(context.Background(), "cap", Grants{Exec: h})
	defer inst.Close()

	_, err := inst.Exec.Run(context.Background(), "/bin/ls", nil, nil)
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("want ErrOutOfScope, got %v", err)
	}
}

func TestExecHandle_RunRelativePathDenied(t *testing.T) {
	h := NewExecHandle("cap", ExecScope{Binaries: []string{"/bin/echo"}})
	inst := NewInstance(context.Background(), "cap", Grants{Exec: h})
	defer inst.Close()

	_, err := inst.Exec.Run(context.Background(), "echo", nil, nil)
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("relative path must be denied, got %v", err)
	}
}

func TestExecHandle_NonZeroExitReported(t *testing.T) {
	// /usr/bin/false exits 1 on POSIX.
	f := execBin(t, "false")
	h := NewExecHandle("cap", ExecScope{Binaries: []string{f}})
	inst := NewInstance(context.Background(), "cap", Grants{Exec: h})
	defer inst.Close()

	res, err := inst.Exec.Run(context.Background(), f, nil, nil)
	if err != nil {
		t.Fatalf("Run returned err=%v — non-zero exit should be in ExitCode, not err", err)
	}
	if res.ExitCode == 0 {
		t.Errorf("false exited 0, want non-zero")
	}
}

func TestExecHandle_Revocation(t *testing.T) {
	echo := execBin(t, "echo")
	h := NewExecHandle("cap", ExecScope{Binaries: []string{echo}})
	inst := NewInstance(context.Background(), "cap", Grants{Exec: h})

	start := time.Now()
	inst.Close()

	_, err := inst.Exec.Run(context.Background(), echo, []string{"hi"}, nil)
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("revocation took %v, want <100ms", elapsed)
	}
}

func TestExecHandle_LifecycleCancelsInFlight(t *testing.T) {
	sleep := execBin(t, "sleep")
	h := NewExecHandle("cap", ExecScope{Binaries: []string{sleep}})
	inst := NewInstance(context.Background(), "cap", Grants{Exec: h})

	var runErr atomic.Value
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := inst.Exec.Run(context.Background(), sleep, []string{"30"}, nil)
		if err != nil {
			runErr.Store(err)
		}
	}()

	// Give the subprocess a moment to actually start.
	time.Sleep(50 * time.Millisecond)
	inst.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight exec was not cancelled within 3s")
	}
}

func TestExecHandle_NonSerializable(t *testing.T) {
	h := NewExecHandle("cap", ExecScope{})
	if _, err := json.Marshal(h); err == nil {
		t.Fatal("ExecHandle must not be JSON-serializable")
	}
}

func TestExecHandle_Owner(t *testing.T) {
	h := NewExecHandle("cap-xyz", ExecScope{})
	if got := h.Owner(); string(got) != "cap-xyz" {
		t.Errorf("Owner()=%q, want cap-xyz", got)
	}
}

func TestExecHandle_StdinPassthrough(t *testing.T) {
	// /bin/cat mirrors stdin to stdout — use it to verify stdin passes.
	cat := execBin(t, "cat")
	h := NewExecHandle("cap", ExecScope{Binaries: []string{cat}})
	inst := NewInstance(context.Background(), "cap", Grants{Exec: h})
	defer inst.Close()

	res, err := inst.Exec.Run(context.Background(), cat, nil, []byte("piped-input"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(res.Stdout) != "piped-input" {
		t.Errorf("stdout=%q, want piped-input", res.Stdout)
	}
}
