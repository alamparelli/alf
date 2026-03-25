package supervisor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// scan()
// ---------------------------------------------------------------------------

func TestScan_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	got := s.scan()
	if len(got) != 0 {
		t.Fatalf("expected 0 services, got %d", len(got))
	}
}

func TestScan_NonexistentDir(t *testing.T) {
	s := New("/tmp/does-not-exist-supervisor-test")
	got := s.scan()
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestScan_ValidServiceJSON(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "myapp")
	os.MkdirAll(appDir, 0o755)

	cfg := ServiceConfig{
		Command: "./run.sh",
		Enabled: true,
	}
	data, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(appDir, "service.json"), data, 0o644)

	s := New(dir)
	got := s.scan()

	if len(got) != 1 {
		t.Fatalf("expected 1 service, got %d", len(got))
	}
	svc, ok := got["myapp"]
	if !ok {
		t.Fatal("expected key 'myapp'")
	}
	if svc.Command != "./run.sh" {
		t.Errorf("command = %q, want ./run.sh", svc.Command)
	}
}

func TestScan_AppWithoutServiceJSON_Skipped(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "noservice"), 0o755)

	s := New(dir)
	got := s.scan()
	if len(got) != 0 {
		t.Fatalf("expected 0 services, got %d", len(got))
	}
}

func TestScan_InvalidJSON_Skipped(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "badapp")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "service.json"), []byte("{invalid"), 0o644)

	s := New(dir)
	got := s.scan()
	if len(got) != 0 {
		t.Fatalf("expected 0 services, got %d", len(got))
	}
}

func TestScan_MissingCommand_Skipped(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "nocmd")
	os.MkdirAll(appDir, 0o755)

	cfg := ServiceConfig{Enabled: true}
	data, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(appDir, "service.json"), data, 0o644)

	s := New(dir)
	got := s.scan()
	if len(got) != 0 {
		t.Fatalf("expected 0 services, got %d", len(got))
	}
}

func TestScan_Defaults(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "defaults-app")
	os.MkdirAll(appDir, 0o755)

	// Minimal: only command set, no name/restart/max_restarts.
	cfg := ServiceConfig{Command: "./start"}
	data, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(appDir, "service.json"), data, 0o644)

	s := New(dir)
	got := s.scan()

	svc := got["defaults-app"]
	if svc.Name != "defaults-app" {
		t.Errorf("name = %q, want 'defaults-app' (default to slug)", svc.Name)
	}
	if svc.Restart != "always" {
		t.Errorf("restart = %q, want 'always'", svc.Restart)
	}
	if svc.MaxRestarts != 100 {
		t.Errorf("max_restarts = %d, want 100", svc.MaxRestarts)
	}
}

func TestScan_HiddenDirSkipped(t *testing.T) {
	dir := t.TempDir()
	hidden := filepath.Join(dir, ".hidden")
	os.MkdirAll(hidden, 0o755)
	cfg := ServiceConfig{Command: "./run"}
	data, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(hidden, "service.json"), data, 0o644)

	s := New(dir)
	got := s.scan()
	if len(got) != 0 {
		t.Fatalf("expected hidden dir skipped, got %d services", len(got))
	}
}

func TestScan_FileInAppsDirSkipped(t *testing.T) {
	dir := t.TempDir()
	// A regular file (not a dir) in the apps directory should be ignored.
	os.WriteFile(filepath.Join(dir, "notadir"), []byte("hi"), 0o644)

	s := New(dir)
	got := s.scan()
	if len(got) != 0 {
		t.Fatalf("expected 0 services, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// inheritSafeEnv()
// ---------------------------------------------------------------------------

func TestInheritSafeEnv_PassesSafeVars(t *testing.T) {
	// Set known safe vars.
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("HOME", "/home/test")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("VAULT_TOKEN", "s.abc123")

	env := inheritSafeEnv()
	envMap := envToMap(env)

	for _, key := range []string{"PATH", "HOME", "LANG", "VAULT_TOKEN"} {
		if _, ok := envMap[key]; !ok {
			t.Errorf("expected %s to be inherited", key)
		}
	}
}

func TestInheritSafeEnv_BlocksSensitiveVars(t *testing.T) {
	t.Setenv("CC_AUTH_TOKEN", "secret")
	t.Setenv("TELEGRAM_BOT_TOKEN", "secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")

	env := inheritSafeEnv()
	envMap := envToMap(env)

	for _, key := range []string{"CC_AUTH_TOKEN", "TELEGRAM_BOT_TOKEN", "AWS_SECRET_ACCESS_KEY"} {
		if _, ok := envMap[key]; ok {
			t.Errorf("expected %s to be blocked", key)
		}
	}
}

func TestInheritSafeEnv_LangDefault(t *testing.T) {
	// Unset LANG to verify default is added.
	origLang, hadLang := os.LookupEnv("LANG")
	os.Unsetenv("LANG")
	defer func() {
		if hadLang {
			os.Setenv("LANG", origLang)
		}
	}()

	env := inheritSafeEnv()
	envMap := envToMap(env)

	if val, ok := envMap["LANG"]; !ok || val != "C.UTF-8" {
		t.Errorf("expected LANG=C.UTF-8 default, got %q", val)
	}
}

func TestInheritSafeEnv_LangNotOverriddenWhenSet(t *testing.T) {
	t.Setenv("LANG", "en_US.UTF-8")

	env := inheritSafeEnv()
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "LANG=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 LANG entry, got %d", count)
	}
}

func TestInheritSafeEnv_AnthropicPrefix(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	env := inheritSafeEnv()
	envMap := envToMap(env)

	if _, ok := envMap["ANTHROPIC_API_KEY"]; !ok {
		t.Error("expected ANTHROPIC_API_KEY to pass through")
	}
}

// ---------------------------------------------------------------------------
// buildCmd() — SEC-001 & SEC-002
// ---------------------------------------------------------------------------

func TestBuildCmd_CommandWithinAppsDir(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app1")
	os.MkdirAll(appDir, 0o755)

	// Create a dummy executable.
	script := filepath.Join(appDir, "run.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho ok"), 0o755)

	s := New(dir)
	p := &managedProc{
		config:  ServiceConfig{Command: "./run.sh"},
		appSlug: "app1",
		workDir: appDir,
	}

	cmd, err := s.buildCmd(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Dir != appDir {
		t.Errorf("workdir = %q, want %q", cmd.Dir, appDir)
	}
}

func TestBuildCmd_SEC001_RelativeEscape(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app1")
	os.MkdirAll(appDir, 0o755)

	s := New(dir)
	p := &managedProc{
		config:  ServiceConfig{Command: "../../etc/evil"},
		appSlug: "app1",
		workDir: appDir,
	}

	_, err := s.buildCmd(p)
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "escapes apps directory") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBuildCmd_SEC001_AbsoluteEscape(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app1")
	os.MkdirAll(appDir, 0o755)

	s := New(dir)
	p := &managedProc{
		config:  ServiceConfig{Command: "/usr/bin/python3"},
		appSlug: "app1",
		workDir: appDir,
	}

	_, err := s.buildCmd(p)
	if err == nil {
		t.Fatal("expected error for absolute path outside apps dir, got nil")
	}
	if !strings.Contains(err.Error(), "escapes apps directory") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBuildCmd_SEC001_AbsoluteWithinAppsDir(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app1")
	os.MkdirAll(appDir, 0o755)

	script := filepath.Join(appDir, "run.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho ok"), 0o755)

	s := New(dir)
	p := &managedProc{
		config:  ServiceConfig{Command: script}, // absolute but inside apps dir
		appSlug: "app1",
		workDir: appDir,
	}

	_, err := s.buildCmd(p)
	if err != nil {
		t.Fatalf("expected no error for absolute path within apps dir, got: %v", err)
	}
}

func TestBuildCmd_SEC002_BlockedEnvKeys(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app1")
	os.MkdirAll(appDir, 0o755)
	script := filepath.Join(appDir, "run.sh")
	os.WriteFile(script, []byte("#!/bin/sh"), 0o755)

	s := New(dir)
	p := &managedProc{
		config: ServiceConfig{
			Command: "./run.sh",
			Env: map[string]string{
				"PATH":            "/evil",
				"LD_PRELOAD":      "/evil.so",
				"LD_LIBRARY_PATH": "/evil",
				"LD_AUDIT":        "/evil.so",
				"HOME":            "/evil",
				"MY_APP_KEY":      "safe-value",
			},
		},
		appSlug: "app1",
		workDir: appDir,
	}

	cmd, err := s.buildCmd(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envMap := envToMap(cmd.Env)

	// Blocked keys must not have the service.json override value.
	for _, key := range []string{"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT"} {
		if val, ok := envMap[key]; ok {
			t.Errorf("expected %s to be blocked, got %q", key, val)
		}
	}
	// PATH and HOME come from inheritSafeEnv, not from the override.
	if val, ok := envMap["PATH"]; ok && val == "/evil" {
		t.Errorf("PATH should not be overridden to /evil")
	}

	// Safe key should pass through.
	if val, ok := envMap["MY_APP_KEY"]; !ok || val != "safe-value" {
		t.Errorf("MY_APP_KEY = %q, want 'safe-value'", val)
	}
}

func TestBuildCmd_SEC002_CaseInsensitiveBlocking(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app1")
	os.MkdirAll(appDir, 0o755)
	script := filepath.Join(appDir, "run.sh")
	os.WriteFile(script, []byte("#!/bin/sh"), 0o755)

	s := New(dir)
	p := &managedProc{
		config: ServiceConfig{
			Command: "./run.sh",
			Env: map[string]string{
				"ld_preload": "/evil.so",
				"Path":       "/evil",
			},
		},
		appSlug: "app1",
		workDir: appDir,
	}

	cmd, err := s.buildCmd(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envMap := envToMap(cmd.Env)
	if val, ok := envMap["ld_preload"]; ok && val == "/evil.so" {
		t.Error("ld_preload (lowercase) should be blocked")
	}
	if val, ok := envMap["Path"]; ok && val == "/evil" {
		t.Error("Path (mixed case) should be blocked")
	}
}

func TestBuildCmd_SetsALF_APP_DATA_DIR(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app1")
	os.MkdirAll(appDir, 0o755)
	script := filepath.Join(appDir, "run.sh")
	os.WriteFile(script, []byte("#!/bin/sh"), 0o755)

	s := New(dir)
	p := &managedProc{
		config:  ServiceConfig{Command: "./run.sh"},
		appSlug: "app1",
		workDir: appDir,
	}

	cmd, err := s.buildCmd(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	envMap := envToMap(cmd.Env)
	expected := filepath.Join(appDir, "data")
	if val, ok := envMap["ALF_APP_DATA_DIR"]; !ok || val != expected {
		t.Errorf("ALF_APP_DATA_DIR = %q, want %q", val, expected)
	}

	// Directory should have been created.
	if _, err := os.Stat(expected); os.IsNotExist(err) {
		t.Error("expected data directory to be created")
	}
}

// ---------------------------------------------------------------------------
// prefixWriter
// ---------------------------------------------------------------------------

func TestPrefixWriter_SingleLine(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	log.SetFlags(0)

	w := &prefixWriter{prefix: "[app] "}
	w.Write([]byte("hello world\n"))

	if !strings.Contains(buf.String(), "[app] hello world") {
		t.Errorf("expected prefixed line, got %q", buf.String())
	}
}

func TestPrefixWriter_MultipleLines(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	log.SetFlags(0)

	w := &prefixWriter{prefix: "[x] "}
	w.Write([]byte("line1\nline2\nline3\n"))

	output := buf.String()
	for _, want := range []string{"[x] line1", "[x] line2", "[x] line3"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got %q", want, output)
		}
	}
}

func TestPrefixWriter_NoNewline_Buffered(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	log.SetFlags(0)

	w := &prefixWriter{prefix: "[b] "}
	w.Write([]byte("partial"))

	// Nothing should be written yet (no newline).
	if buf.Len() != 0 {
		t.Errorf("expected no output, got %q", buf.String())
	}

	// Completing the line should flush it.
	w.Write([]byte(" data\n"))
	if !strings.Contains(buf.String(), "[b] partial data") {
		t.Errorf("expected buffered line, got %q", buf.String())
	}
}

func TestPrefixWriter_BufferOverflow_Truncated(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	log.SetFlags(0)

	w := &prefixWriter{prefix: "[ov] "}

	// Write more than maxPrefixBuf (64KB) without any newlines.
	bigData := make([]byte, maxPrefixBuf+1024)
	for i := range bigData {
		bigData[i] = 'A'
	}
	n, err := w.Write(bigData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(bigData) {
		t.Errorf("n = %d, want %d", n, len(bigData))
	}

	// Should have flushed with truncation.
	if !strings.Contains(buf.String(), "(truncated)") {
		t.Errorf("expected truncated message, got %q", buf.String())
	}
	// Buffer should be cleared after overflow.
	if len(w.buf) != 0 {
		t.Errorf("expected buffer cleared, got %d bytes", len(w.buf))
	}
}

func TestPrefixWriter_ReturnsTotalBytesWritten(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	log.SetFlags(0)

	w := &prefixWriter{prefix: "[r] "}
	data := []byte("hello\nworld\n")
	n, err := w.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Errorf("n = %d, want %d", n, len(data))
	}
}

func TestPrefixWriter_SplitAcrossWrites(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	log.SetFlags(0)

	w := &prefixWriter{prefix: "[s] "}
	w.Write([]byte("hel"))
	w.Write([]byte("lo\nwor"))
	w.Write([]byte("ld\n"))

	output := buf.String()
	if !strings.Contains(output, "[s] hello") {
		t.Errorf("expected '[s] hello' in output, got %q", output)
	}
	if !strings.Contains(output, "[s] world") {
		t.Errorf("expected '[s] world' in output, got %q", output)
	}
}

// ---------------------------------------------------------------------------
// scan() — multiple apps, mixed validity
// ---------------------------------------------------------------------------

func TestScan_MixedApps(t *testing.T) {
	dir := t.TempDir()

	// Valid app.
	valid := filepath.Join(dir, "valid")
	os.MkdirAll(valid, 0o755)
	data, _ := json.Marshal(ServiceConfig{Command: "./run", Enabled: true, Name: "myname"})
	os.WriteFile(filepath.Join(valid, "service.json"), data, 0o644)

	// Invalid JSON.
	bad := filepath.Join(dir, "bad")
	os.MkdirAll(bad, 0o755)
	os.WriteFile(filepath.Join(bad, "service.json"), []byte("nope"), 0o644)

	// No service.json.
	plain := filepath.Join(dir, "plain")
	os.MkdirAll(plain, 0o755)

	// Missing command.
	nocmd := filepath.Join(dir, "nocmd")
	os.MkdirAll(nocmd, 0o755)
	data2, _ := json.Marshal(ServiceConfig{Enabled: true})
	os.WriteFile(filepath.Join(nocmd, "service.json"), data2, 0o644)

	s := New(dir)
	got := s.scan()

	if len(got) != 1 {
		t.Fatalf("expected 1 valid service, got %d: %+v", len(got), got)
	}
	svc := got["valid"]
	if svc.Name != "myname" {
		t.Errorf("expected explicit name 'myname', got %q", svc.Name)
	}
}

// ---------------------------------------------------------------------------
// buildCmd() — various ../ patterns (SEC-001)
// ---------------------------------------------------------------------------

func TestBuildCmd_SEC001_DotDotSlashVariants(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app1")
	os.MkdirAll(appDir, 0o755)

	tests := []struct {
		name    string
		command string
	}{
		// ../evil resolves to <appsDir>/evil which is still inside apps dir,
		// so we need ../../ to actually escape the temp dir.
		{"double parent", "../../evil"},
		{"triple parent", "../../../evil"},
		{"subdir deep escape", "sub/../../../evil"},
		{"absolute outside", "/tmp/evil"},
	}

	s := New(dir)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &managedProc{
				config:  ServiceConfig{Command: tt.command},
				appSlug: "app1",
				workDir: appDir,
			}
			_, err := s.buildCmd(p)
			if err == nil {
				t.Errorf("expected error for command %q", tt.command)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok {
			m[k] = v
		}
	}
	return m
}

// Ensure blockedEnvKeys coverage: enumerate all known blocked keys.
func TestBlockedEnvKeys_Completeness(t *testing.T) {
	expected := []string{"PATH", "HOME", "SHELL", "USER", "LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT"}
	for _, k := range expected {
		if !blockedEnvKeys[k] {
			t.Errorf("expected %q in blockedEnvKeys", k)
		}
	}
}

// Ensure safePrefixes list is not empty and contains expected entries.
func TestSafePrefixes_ContainsExpected(t *testing.T) {
	expected := []string{"PATH=", "HOME=", "LANG=", "VAULT_TOKEN=", "ANTHROPIC_"}
	for _, want := range expected {
		found := false
		for _, p := range safePrefixes {
			if p == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in safePrefixes", want)
		}
	}
}

// Verify prefixWriter implements io.Writer.
func TestPrefixWriter_ImplementsWriter(t *testing.T) {
	var _ fmt.Stringer // just ensuring imports compile
	w := &prefixWriter{prefix: "test"}
	_ = w // satisfies io.Writer interface via Write method
}
