package gittrack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func skipIfNoGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
}

func TestInit_CreatesRepo(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()

	tr := New(dir)
	if err := tr.Init(); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Error(".git directory should exist after Init")
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Error(".gitignore should exist after Init")
	}
}

func TestInit_Idempotent(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()

	tr := New(dir)
	if err := tr.Init(); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if err := tr.Init(); err != nil {
		t.Fatalf("second Init should be no-op: %v", err)
	}
}

func TestCommit_AfterFileWrite(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()

	tr := New(dir)
	if err := tr.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Write a tracked file inside context/.
	os.MkdirAll(filepath.Join(dir, "context"), 0o755)
	os.WriteFile(filepath.Join(dir, "context", "soul.md"), []byte(`# Soul`), 0o644)

	if err := tr.Commit("update config"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Verify commit in log.
	out, _ := exec.Command("git", "-C", dir, "log", "--oneline").CombinedOutput()
	if !strings.Contains(string(out), "update config") {
		t.Errorf("git log should contain commit message, got: %s", out)
	}
}

func TestCommit_NoopWhenClean(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()

	tr := New(dir)
	if err := tr.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Commit with no changes should succeed (no-op).
	if err := tr.Commit("should be noop"); err != nil {
		t.Fatalf("Commit on clean repo: %v", err)
	}

	// Verify the noop commit message is not in the log.
	out, _ := exec.Command("git", "-C", dir, "log", "--oneline").CombinedOutput()
	if strings.Contains(string(out), "should be noop") {
		t.Error("no-op commit should not create a log entry")
	}
}

func TestGitignore_Content(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()

	tr := New(dir)
	if err := tr.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}

	content := string(data)

	// The gitignore tracks everything by default (no blanket "*" deny),
	// so only transient/heavy paths need exclusion rules.
	for _, expected := range []string{
		".cache/",
		".local/",
		"sessions/",
		"logs/daemon.log",
		"*.db-shm",
		"*.db-wal",
		"*.sock",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf(".gitignore should contain %q", expected)
		}
	}
}
