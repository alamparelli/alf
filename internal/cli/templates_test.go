package cli

import (
	"testing"
)

func TestSeedBundledAgents_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	// Should succeed even with no bundled agents.
	if err := SeedBundledAgents(dir); err != nil {
		t.Fatal(err)
	}
}
