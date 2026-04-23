package tooling

import (
	"path/filepath"
	"testing"

	"github.com/alamparelli/alf/internal/sandbox/integrity"
)

// The full integrity Guard behaviour is tested in internal/sandbox/integrity.
// These tests cover the tooling-side integration: Executor/Registry consume
// integrity.IntegrityGuard directly after the shim was removed in #340 R5l.

func TestIntegrityGuardConstructs(t *testing.T) {
	dir := t.TempDir()
	ig, err := integrity.NewIntegrityGuard(dir, nil)
	if err != nil {
		t.Fatalf("NewIntegrityGuard: %v", err)
	}
	if ig == nil {
		t.Fatal("NewIntegrityGuard returned nil guard")
	}
}

func TestIsUserTool(t *testing.T) {
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
		if got := integrity.IsUserTool(tc.path, dataDir); got != tc.want {
			t.Errorf("IsUserTool(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
