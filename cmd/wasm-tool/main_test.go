package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDaemon spins up an http server on a unix socket and records the
// last request it received. Used by the binary tests to assert the
// wire format without requiring a real daemon or capability runtime.
type fakeDaemon struct {
	listener   net.Listener
	server     *httptest.Server
	sockPath   string
	lastReq    *request
	respond    func(*request) response
	statusCode int
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	// Unix socket paths cap at 104 chars on darwin / 108 on linux, so
	// t.TempDir() — which embeds the test name — can blow the budget
	// for longer-named tests. os.MkdirTemp under the system /tmp keeps
	// the path short and we clean it up via t.Cleanup.
	tmp, err := os.MkdirTemp("", "wt")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	sockPath := filepath.Join(tmp, "s")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	fd := &fakeDaemon{
		listener:   ln,
		sockPath:   sockPath,
		statusCode: http.StatusOK,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tools/invoke", func(w http.ResponseWriter, r *http.Request) {
		var req request
		_ = json.NewDecoder(r.Body).Decode(&req)
		fd.lastReq = &req
		var resp response
		if fd.respond != nil {
			resp = fd.respond(&req)
		}
		w.WriteHeader(fd.statusCode)
		_ = json.NewEncoder(w).Encode(resp)
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	fd.server = &httptest.Server{Listener: ln, Config: srv}
	t.Cleanup(func() {
		ln.Close()
	})
	return fd
}

func withSocket(t *testing.T, path string, fn func()) {
	t.Helper()
	prev := os.Getenv("ALF_TOOLS_SOCK")
	os.Setenv("ALF_TOOLS_SOCK", path)
	defer os.Setenv("ALF_TOOLS_SOCK", prev)
	fn()
}

// TestRun_HappyPath pins the basic invocation: pass id + JSON args,
// daemon returns success output, binary prints it to stdout and
// exits 0. This is the contract Claude Code subprocesses rely on.
func TestRun_HappyPath(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.respond = func(req *request) response {
		return response{Output: `{"status":200,"body":"ok"}`}
	}

	var stdout, stderr bytes.Buffer
	var code int
	withSocket(t, fd.sockPath, func() {
		code = run([]string{"http-hello", `{"url":"https://httpbin.org/get"}`}, strings.NewReader(""), &stdout, &stderr)
	})

	if code != exitOK {
		t.Errorf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status":200`) {
		t.Errorf("stdout=%q does not carry output", stdout.String())
	}
	if fd.lastReq == nil {
		t.Fatal("daemon was never called")
	}
	if fd.lastReq.Name != "http-hello" {
		t.Errorf("name forwarded as %q, want %q", fd.lastReq.Name, "http-hello")
	}
	if fd.lastReq.Arguments != `{"url":"https://httpbin.org/get"}` {
		t.Errorf("arguments forwarded as %q", fd.lastReq.Arguments)
	}
}

// TestRun_StdinArgs pins the stdin invocation path used by the
// toolbox.md `echo '...' | wasm-tool <id>` pattern. Important for
// tools whose arg payload is too long for the bash quoting model.
func TestRun_StdinArgs(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.respond = func(req *request) response { return response{Output: "ok"} }

	var stdout, stderr bytes.Buffer
	var code int
	withSocket(t, fd.sockPath, func() {
		code = run([]string{"http-hello"}, strings.NewReader(`{"url":"https://x.test"}`), &stdout, &stderr)
	})

	if code != exitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if fd.lastReq.Arguments != `{"url":"https://x.test"}` {
		t.Errorf("arguments lost from stdin: %q", fd.lastReq.Arguments)
	}
}

// TestRun_NoArgsTool pins that calling `wasm-tool foo` with empty
// stdin sends an empty Arguments — the daemon-side handler then
// substitutes "{}" so the dispatch lands. The binary itself stays
// agnostic to the schema.
func TestRun_NoArgsTool(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.respond = func(req *request) response { return response{Output: "ok"} }

	var stdout, stderr bytes.Buffer
	withSocket(t, fd.sockPath, func() {
		run([]string{"foo"}, strings.NewReader(""), &stdout, &stderr)
	})

	if fd.lastReq.Arguments != "" {
		t.Errorf("arguments should be empty on empty stdin; got %q", fd.lastReq.Arguments)
	}
}

// TestRun_ToolErrorExitCode pins the is_error → exit 2 wiring. Claude
// Code Bash inspects exit codes; mapping daemon-reported tool errors
// to a distinct code (vs transport errors) lets agents distinguish
// "tool ran and failed" from "couldn't reach tool".
func TestRun_ToolErrorExitCode(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.respond = func(req *request) response {
		return response{Output: "stack trace here", IsError: true, ErrorMessage: "boom"}
	}

	var stdout, stderr bytes.Buffer
	var code int
	withSocket(t, fd.sockPath, func() {
		code = run([]string{"failing-tool", "{}"}, strings.NewReader(""), &stdout, &stderr)
	})

	if code != exitToolError {
		t.Errorf("exit=%d, want %d (tool error)", code, exitToolError)
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Errorf("stderr should mention error message; got %q", stderr.String())
	}
	// The payload is still on stdout so the LLM sees the diagnostic.
	if !strings.Contains(stdout.String(), "stack trace here") {
		t.Errorf("stdout should still carry the tool's output; got %q", stdout.String())
	}
}

// TestRun_TransportErrorExitCode pins that an unreachable socket
// returns exit 3 (vs 2 for tool failure). Lets a wrapping bash script
// retry on transport but escalate on tool errors.
func TestRun_TransportErrorExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var code int
	withSocket(t, "/nonexistent/path-that-does-not-exist.sock", func() {
		code = run([]string{"http-hello", "{}"}, strings.NewReader(""), &stdout, &stderr)
	})

	if code != exitTransport {
		t.Errorf("exit=%d, want %d (transport)", code, exitTransport)
	}
	if !strings.Contains(stderr.String(), "wasm-tool:") {
		t.Errorf("stderr should carry diagnostic; got %q", stderr.String())
	}
}

// TestRun_MissingSocketEnv pins behaviour when the binary is invoked
// outside a daemon-launched subprocess (no ALF_TOOLS_SOCK). A clear
// error beats a confusing dial failure.
func TestRun_MissingSocketEnv(t *testing.T) {
	var stdout, stderr bytes.Buffer
	prev := os.Getenv("ALF_TOOLS_SOCK")
	os.Unsetenv("ALF_TOOLS_SOCK")
	defer os.Setenv("ALF_TOOLS_SOCK", prev)

	code := run([]string{"http-hello", "{}"}, strings.NewReader(""), &stdout, &stderr)
	if code != exitTransport {
		t.Errorf("exit=%d, want %d", code, exitTransport)
	}
	if !strings.Contains(stderr.String(), "ALF_TOOLS_SOCK") {
		t.Errorf("stderr should mention missing env; got %q", stderr.String())
	}
}

// TestRun_NoArgsUsage pins the usage banner: `wasm-tool` with no args
// is a misuse, exits 64 (sysexits.h EX_USAGE). Distinct from tool/
// transport errors so a shell wrapper can detect "I called the binary
// wrong".
func TestRun_NoArgsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, strings.NewReader(""), &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("exit=%d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "usage") {
		t.Errorf("stderr should carry usage hint; got %q", stderr.String())
	}
}

// TestRun_DaemonHTTPErrorSurfaces pins that a non-200 from the daemon
// (e.g. /api/tools/invoke returns 503 when the executor is missing)
// bubbles up as transport-class failure. Avoids the wasm-tool binary
// silently exiting 0 on a server-side outage.
func TestRun_DaemonHTTPErrorSurfaces(t *testing.T) {
	fd := newFakeDaemon(t)
	fd.statusCode = http.StatusServiceUnavailable
	fd.respond = func(req *request) response { return response{} }

	var stdout, stderr bytes.Buffer
	var code int
	withSocket(t, fd.sockPath, func() {
		code = run([]string{"http-hello", "{}"}, strings.NewReader(""), &stdout, &stderr)
	})

	if code != exitTransport {
		t.Errorf("exit=%d, want %d (transport)", code, exitTransport)
	}
	if !strings.Contains(stderr.String(), "HTTP 503") {
		t.Errorf("stderr should mention HTTP 503; got %q", stderr.String())
	}
}

// Stop the unused-import linter from complaining about httptest in
// the fake-daemon scaffolding when -tags fts5 is set on some builds.
var _ = io.Discard
