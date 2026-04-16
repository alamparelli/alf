package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSeedBundledAgents_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	// Should succeed even with no bundled agents.
	if err := SeedBundledAgents(dir); err != nil {
		t.Fatal(err)
	}
}

// TestSeedBootstrapScript_NotSeededByDefault verifies that a fresh install
// does not create bootstrap.sh (deprecated — entrypoint.sh warns on every start).
func TestSeedBootstrapScript_NotSeededByDefault(t *testing.T) {
	dir := t.TempDir()

	// SeedBootstrapScript should only be called explicitly, not by generateFiles.
	// Verify the file is absent from a freshly seeded data dir.
	bootstrapPath := filepath.Join(dir, "data", "bootstrap.sh")
	if _, err := os.Stat(bootstrapPath); err == nil {
		t.Fatal("bootstrap.sh must not exist in a fresh install — it triggers a deprecation warning on every daemon start")
	}
}
