package cli

import (
	"encoding/json"
	"io/fs"
	"os"
	"testing"

	"github.com/alamparelli/alf/internal/agents"
)

func TestBundledAgentsValid(t *testing.T) {
	entries, err := fs.ReadDir(bundledAgentsFS, "bundled_agents")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no bundled agent files found")
	}
	for _, e := range entries {
		data, err := bundledAgentsFS.ReadFile("bundled_agents/" + e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var tc agents.TeamConfig
		if err := json.Unmarshal(data, &tc); err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		if tc.Name == "" {
			t.Errorf("%s: missing team name", e.Name())
		}
		if len(tc.Agents) == 0 {
			t.Errorf("%s: no agents defined", e.Name())
		}
		for _, a := range tc.Agents {
			if a.Name == "" {
				t.Errorf("%s: agent missing name", e.Name())
			}
			if a.Tier == "" {
				t.Errorf("%s/%s: agent missing tier", tc.Name, a.Name)
			}
			if a.SystemPrompt == "" {
				t.Errorf("%s/%s: agent missing system_prompt", tc.Name, a.Name)
			}
		}
	}
}

func TestSeedBundledAgents(t *testing.T) {
	dir := t.TempDir()
	if err := SeedBundledAgents(dir); err != nil {
		t.Fatal(err)
	}

	// Verify the store can load the seeded files.
	agentsDir := dir + "/data/agents/teams"
	store := agents.NewFileAgentStore(agentsDir)
	all := store.All()
	if len(all) == 0 {
		t.Fatal("no teams loaded after seeding")
	}

	// Verify starter team specifically.
	tc, ok := store.Get("starter")
	if !ok {
		t.Fatal("starter team not found")
	}
	if len(tc.Agents) != 3 {
		t.Errorf("expected 3 agents in starter, got %d", len(tc.Agents))
	}

	// Verify all agents are addressable.
	for _, a := range tc.Agents {
		_, _, ok := store.GetAgent("starter/" + a.Name)
		if !ok {
			t.Errorf("agent starter/%s not found via GetAgent", a.Name)
		}
	}
}

func TestSeedBundledAgentsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := SeedBundledAgents(dir); err != nil {
		t.Fatal(err)
	}

	// Modify the seeded file.
	modifiedPath := dir + "/data/agents/teams/starter.json"
	if err := os.WriteFile(modifiedPath, []byte(`{"name":"starter","agents":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Seed again — should NOT overwrite.
	if err := SeedBundledAgents(dir); err != nil {
		t.Fatal(err)
	}

	store := agents.NewFileAgentStore(dir + "/data/agents/teams")
	tc, _ := store.Get("starter")
	if len(tc.Agents) != 0 {
		t.Error("expected modified file to be preserved (0 agents), but it was overwritten")
	}
}

