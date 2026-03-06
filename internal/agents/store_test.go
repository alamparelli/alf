package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTeamFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const validTeamA = `{
	"name": "alpha",
	"description": "Alpha team",
	"max_agents_per_request": 2,
	"global_timeout_minutes": 30,
	"agents": [
		{"name": "writer", "description": "Writes content", "model": "sonnet", "system_prompt": "You write."},
		{"name": "reviewer", "description": "Reviews content", "model": "haiku", "system_prompt": "You review."}
	]
}`

const validTeamB = `{
	"name": "beta",
	"description": "Beta team",
	"max_agents_per_request": 1,
	"agents": [{"name": "coder", "description": "Codes", "model": "opus", "system_prompt": "You code.", "write_capable": true}]
}`

func TestLoadValidTeams(t *testing.T) {
	dir := t.TempDir()
	writeTeamFile(t, dir, "alpha.json", validTeamA)
	writeTeamFile(t, dir, "beta.json", validTeamB)

	s := NewFileAgentStore(dir)
	all := s.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(all))
	}
	if all[0].Name != "alpha" || all[1].Name != "beta" {
		t.Errorf("unexpected order: %s, %s", all[0].Name, all[1].Name)
	}
}

func TestLoadEmptyDir(t *testing.T) {
	dir := t.TempDir()
	s := NewFileAgentStore(dir)
	if len(s.All()) != 0 {
		t.Fatal("expected 0 teams")
	}
}

func TestLoadMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	writeTeamFile(t, dir, "bad.json", `{invalid`)
	writeTeamFile(t, dir, "good.json", validTeamA)

	s := NewFileAgentStore(dir)
	if len(s.All()) != 1 {
		t.Fatalf("expected 1 team, got %d", len(s.All()))
	}
}

func TestGetTeam(t *testing.T) {
	dir := t.TempDir()
	writeTeamFile(t, dir, "alpha.json", validTeamA)

	s := NewFileAgentStore(dir)
	tc, ok := s.Get("alpha")
	if !ok {
		t.Fatal("expected to find alpha")
	}
	if tc.Description != "Alpha team" {
		t.Errorf("unexpected description: %s", tc.Description)
	}
	if tc.MaxAgentsPerReq != 2 {
		t.Errorf("unexpected max_agents: %d", tc.MaxAgentsPerReq)
	}
	if len(tc.Agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(tc.Agents))
	}
}

func TestGetTeamNotFound(t *testing.T) {
	dir := t.TempDir()
	s := NewFileAgentStore(dir)
	_, ok := s.Get("nope")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestGetAgent(t *testing.T) {
	dir := t.TempDir()
	writeTeamFile(t, dir, "alpha.json", validTeamA)

	s := NewFileAgentStore(dir)
	tc, ac, ok := s.GetAgent("alpha/writer")
	if !ok {
		t.Fatal("expected to find alpha/writer")
	}
	if tc.Name != "alpha" {
		t.Errorf("unexpected team: %s", tc.Name)
	}
	if ac.Model != "sonnet" {
		t.Errorf("unexpected model: %s", ac.Model)
	}
}

func TestGetAgentNotFound(t *testing.T) {
	dir := t.TempDir()
	writeTeamFile(t, dir, "alpha.json", validTeamA)

	s := NewFileAgentStore(dir)
	_, _, ok := s.GetAgent("alpha/nope")
	if ok {
		t.Fatal("expected not found")
	}
	_, _, ok = s.GetAgent("nope/writer")
	if ok {
		t.Fatal("expected not found for wrong team")
	}
}

func TestReload(t *testing.T) {
	dir := t.TempDir()
	writeTeamFile(t, dir, "alpha.json", validTeamA)
	s := NewFileAgentStore(dir)
	if len(s.All()) != 1 {
		t.Fatal("expected 1 team")
	}
	writeTeamFile(t, dir, "beta.json", validTeamB)
	s.Reload()
	if len(s.All()) != 2 {
		t.Fatal("expected 2 teams after reload")
	}
}

func TestReloadRemovesDeleted(t *testing.T) {
	dir := t.TempDir()
	writeTeamFile(t, dir, "alpha.json", validTeamA)
	writeTeamFile(t, dir, "beta.json", validTeamB)
	s := NewFileAgentStore(dir)
	if len(s.All()) != 2 {
		t.Fatal("expected 2 teams")
	}
	os.Remove(filepath.Join(dir, "beta.json"))
	s.Reload()
	if len(s.All()) != 1 {
		t.Fatal("expected 1 team after reload")
	}
}

func TestDuplicateTeamName(t *testing.T) {
	dir := t.TempDir()
	writeTeamFile(t, dir, "a.json", validTeamA)
	writeTeamFile(t, dir, "b.json", validTeamA) // same name "alpha"

	s := NewFileAgentStore(dir)
	all := s.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 team (dedup), got %d", len(all))
	}
}

func TestTeamDefaults(t *testing.T) {
	dir := t.TempDir()
	writeTeamFile(t, dir, "minimal.json", `{"name":"min","agents":[{"name":"a","model":"haiku","system_prompt":"hi"}]}`)

	s := NewFileAgentStore(dir)
	tc, ok := s.Get("min")
	if !ok {
		t.Fatal("expected to find min")
	}
	if tc.GlobalTimeoutMin != 0 {
		t.Errorf("expected 0 timeout, got %d", tc.GlobalTimeoutMin)
	}
	if tc.MaxAgentsPerReq != 0 {
		t.Errorf("expected 0 max agents, got %d", tc.MaxAgentsPerReq)
	}
}
