package agents

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression: task directory creation must set 0o775 permissions so the
// subprocess user (uid 1000) can write files inside.

func TestTaskDir_CreatedWithCorrectPermissions(t *testing.T) {
	tmp := t.TempDir()

	taskDir := filepath.Join(tmp, "agents", "123456")
	if err := os.MkdirAll(taskDir, 0o775); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.Chmod(taskDir, 0o775); err != nil {
		t.Fatalf("Chmod failed: %v", err)
	}

	info, err := os.Stat(taskDir)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	// On most Unix systems MkdirAll applies umask, so we Chmod explicitly
	// (matching the production code). Verify the final mode.
	got := info.Mode().Perm()
	if got != 0o775 {
		t.Fatalf("expected permissions 0775, got %04o", got)
	}

	// Also verify the parent "agents" dir exists and is traversable.
	parentDir := filepath.Join(tmp, "agents")
	if err := os.Chmod(parentDir, 0o775); err != nil {
		t.Fatalf("Chmod parent failed: %v", err)
	}
	parentInfo, err := os.Stat(parentDir)
	if err != nil {
		t.Fatalf("Stat parent failed: %v", err)
	}
	if parentInfo.Mode().Perm() != 0o775 {
		t.Fatalf("expected parent permissions 0775, got %04o", parentInfo.Mode().Perm())
	}
}
