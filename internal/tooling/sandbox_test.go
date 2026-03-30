package tooling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePath_Relative(t *testing.T) {
	got := ResolvePath("/data", "file.txt")
	if got != "/data/file.txt" {
		t.Errorf("expected /data/file.txt, got %s", got)
	}
}

func TestResolvePath_Absolute(t *testing.T) {
	got := ResolvePath("/data", "/other/file.txt")
	if got != "/other/file.txt" {
		t.Errorf("expected /other/file.txt, got %s", got)
	}
}

func TestResolvePath_EmptyDataDir(t *testing.T) {
	got := ResolvePath("", "file.txt")
	if got != "file.txt" {
		t.Errorf("expected file.txt, got %s", got)
	}
}

func TestCheckBoundary_EmptyDataDir(t *testing.T) {
	path, err := CheckBoundary("", "/anywhere/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/anywhere/file.txt" {
		t.Errorf("expected passthrough, got %s", path)
	}
}

func TestCheckBoundary_InsideBoundary(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	os.MkdirAll(sub, 0o755)
	file := filepath.Join(sub, "test.txt")
	os.WriteFile(file, []byte("ok"), 0o644)

	path, err := CheckBoundary(dir, file)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if path == "" {
		t.Error("expected non-empty resolved path")
	}
}

func TestCheckBoundary_OutsideBoundary(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "..", "escape.txt")

	_, err := CheckBoundary(dir, outside)
	if err == nil {
		t.Error("expected error for path escaping boundary")
	}
}

func TestCheckBoundary_DotDotTraversal(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)

	_, err := CheckBoundary(dir, filepath.Join(dir, "sub", "..", "..", "etc", "passwd"))
	if err == nil {
		t.Error("expected error for .. traversal escaping boundary")
	}
}

func TestCheckBoundary_NewFileInsideBoundary(t *testing.T) {
	dir := t.TempDir()
	newFile := filepath.Join(dir, "new.txt")

	path, err := CheckBoundary(dir, newFile)
	if err != nil {
		t.Fatalf("expected success for new file, got %v", err)
	}
	if path == "" {
		t.Error("expected non-empty resolved path")
	}
}

func TestCheckBoundary_SymlinkInside(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	os.WriteFile(target, []byte("data"), 0o644)
	link := filepath.Join(dir, "link.txt")
	os.Symlink(target, link)

	path, err := CheckBoundary(dir, link)
	if err != nil {
		t.Fatalf("expected success for symlink inside boundary, got %v", err)
	}
	if path == "" {
		t.Error("expected non-empty resolved path")
	}
}

func TestCheckBoundary_SymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	os.WriteFile(target, []byte("secret"), 0o644)

	link := filepath.Join(dir, "escape-link")
	os.Symlink(target, link)

	_, err := CheckBoundary(dir, link)
	if err == nil {
		t.Error("expected error for symlink escaping boundary")
	}
}

// ---------------------------------------------------------------------------
// shellQuote tests
// ---------------------------------------------------------------------------

func TestShellQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"normal string", "hello", "'hello'"},
		{"empty string", "", "''"},
		{"with spaces", "hello world", "'hello world'"},
		{"single quote", "it's", `'it'\''s'`},
		{"multiple single quotes", "a'b'c", `'a'\''b'\''c'`},
		{"null bytes stripped", "ab\x00cd", "'abcd'"},
		{"only null byte", "\x00", "''"},
		{"special shell chars", "a;b|c&d", "'a;b|c&d'"},
		{"dollar sign", "$HOME", "'$HOME'"},
		{"backticks", "`whoami`", "'`whoami`'"},
		{"command substitution", "$(id)", "'$(id)'"},
		{"double quotes", `"hello"`, `'"hello"'`},
		{"newlines", "line1\nline2", "'line1\nline2'"},
		{"tabs", "a\tb", "'a\tb'"},
		{"backslash", `a\b`, `'a\b'`},
		{"glob chars", "*.txt", "'*.txt'"},
		{"semicolon injection", "ls; rm -rf /", "'ls; rm -rf /'"},
		{"pipe injection", "echo | cat /etc/passwd", "'echo | cat /etc/passwd'"},
		{"null before quote", "\x00'", "''\\'''"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SandboxSafeEnv tests
// ---------------------------------------------------------------------------

func TestSandboxSafeEnv_ContainsExpectedVars(t *testing.T) {
	env := SandboxSafeEnv("/data/apps/myapp")

	expected := map[string]string{
		"PATH":             "/usr/local/bin:/usr/bin:/bin",
		"HOME":             "/home/alf",
		"USER":             "alf",
		"LOGNAME":          "alf",
		"SHELL":            "/bin/bash",
		"TERM":             "xterm-256color",
		"LANG":             "en_US.UTF-8",
		"ALF_APP_DATA_DIR": "/data/apps/myapp",
		"TMPDIR":           "/tmp",
	}

	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	for k, v := range expected {
		got, ok := envMap[k]
		if !ok {
			t.Errorf("missing env var %s", k)
		} else if got != v {
			t.Errorf("env %s = %q, want %q", k, got, v)
		}
	}

	if len(env) != len(expected) {
		t.Errorf("expected %d env vars, got %d", len(expected), len(env))
	}
}

func TestSandboxSafeEnv_NoSensitiveVars(t *testing.T) {
	env := SandboxSafeEnv("/data/apps/test")

	sensitivePatterns := []string{
		"VAULT_TOKEN",
		"VAULT_ADDR",
		"SECRET",
		"PASSWORD",
		"TOKEN",
		"API_KEY",
		"AWS_SECRET",
		"PRIVATE_KEY",
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"http_proxy",
		"https_proxy",
		"NO_PROXY",
		"DOCKER_HOST",
	}

	for _, e := range env {
		key := strings.SplitN(e, "=", 2)[0]
		for _, sensitive := range sensitivePatterns {
			// ALF_APP_DATA_DIR contains no sensitive keyword, skip exact matches
			if strings.EqualFold(key, sensitive) {
				t.Errorf("env contains sensitive var %s", key)
			}
		}
	}
}

func TestSandboxSafeEnv_AppDataDirPropagated(t *testing.T) {
	dir := "/custom/app/path"
	env := SandboxSafeEnv(dir)
	found := false
	for _, e := range env {
		if e == "ALF_APP_DATA_DIR="+dir {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ALF_APP_DATA_DIR not set to %q", dir)
	}
}

// ---------------------------------------------------------------------------
// dnsSnippet tests
// ---------------------------------------------------------------------------

func TestDnsSnippet_NetworkTrue(t *testing.T) {
	got := dnsSnippet(true)
	if !strings.Contains(got, "resolv.conf") {
		t.Errorf("network=true should copy resolv.conf, got: %s", got)
	}
	if strings.HasPrefix(got, "#") {
		t.Errorf("network=true should not be a comment, got: %s", got)
	}
}

// ---------------------------------------------------------------------------
// ServerSafeEnv tests
// ---------------------------------------------------------------------------

func TestServerSafeEnv_ContainsExpectedVars(t *testing.T) {
	env := ServerSafeEnv("/data/apps/myapp")

	expected := map[string]string{
		"PATH":             "/usr/local/bin:/usr/bin:/bin",
		"HOME":             "/home/alf",
		"USER":             "alf",
		"LANG":             "en_US.UTF-8",
		"ALF_APP_DATA_DIR": "/data/apps/myapp/data",
		"TMPDIR":           "/tmp",
	}

	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	for k, v := range expected {
		got, ok := envMap[k]
		if !ok {
			t.Errorf("missing env var %s", k)
		} else if got != v {
			t.Errorf("env %s = %q, want %q", k, got, v)
		}
	}
}

func TestServerSafeEnv_NoSensitiveVars(t *testing.T) {
	env := ServerSafeEnv("/data/apps/test")

	sensitivePatterns := []string{
		"VAULT_TOKEN",
		"VAULT_ADDR",
		"SECRET",
		"PASSWORD",
	}

	for _, e := range env {
		key := strings.SplitN(e, "=", 2)[0]
		upper := strings.ToUpper(key)
		for _, sensitive := range sensitivePatterns {
			if strings.Contains(upper, sensitive) {
				t.Errorf("env contains sensitive var %s (matches %s)", key, sensitive)
			}
		}
		// Also check for ANTHROPIC_ and CLAUDE_ prefixes.
		if strings.HasPrefix(upper, "ANTHROPIC_") {
			t.Errorf("env contains ANTHROPIC_ prefixed var: %s", key)
		}
		if strings.HasPrefix(upper, "CLAUDE_") {
			t.Errorf("env contains CLAUDE_ prefixed var: %s", key)
		}
	}
}

func TestServerSafeEnv_AppDataDirCombinesWithData(t *testing.T) {
	appDir := "/opt/apps/weather"
	env := ServerSafeEnv(appDir)

	want := filepath.Join(appDir, "data")
	found := false
	for _, e := range env {
		if e == "ALF_APP_DATA_DIR="+want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ALF_APP_DATA_DIR should be %q (appDir + /data)", want)
	}
}

func TestDnsSnippet_NetworkFalse(t *testing.T) {
	got := dnsSnippet(false)
	if strings.Contains(got, "resolv.conf") {
		t.Errorf("network=false should not reference resolv.conf, got: %s", got)
	}
	if !strings.HasPrefix(got, "#") {
		t.Errorf("network=false should be a comment, got: %s", got)
	}
}
