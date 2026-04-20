package tooling

import (
	"path/filepath"
	"testing"
)

// The full integrity Guard behaviour is tested in internal/sandbox/integrity.
// These tests only cover the thin re-export shim kept here so that
// cmd/alf-daemon and tooling/{registry,executor} keep compiling unchanged.

func TestShim_NewIntegrityGuard_CreatesGuard(t *testing.T) {
	dir := t.TempDir()
	ig, err := NewIntegrityGuard(dir, nil)
	if err != nil {
		t.Fatalf("NewIntegrityGuard: %v", err)
	}
	if ig == nil {
		t.Fatal("NewIntegrityGuard returned nil guard")
	}
}

func TestShim_IsUserTool(t *testing.T) {
	dataDir := "/tmp/alf-test"
	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(dataDir, "tools", "foo"), true},
		{filepath.Join(dataDir, "other", "foo"), false},
		{"/unrelated/tools/foo", false},
	}
	for _, tc := range tests {
		if got := IsUserTool(tc.path, dataDir); got != tc.want {
			t.Errorf("IsUserTool(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
