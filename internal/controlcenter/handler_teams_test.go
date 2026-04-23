package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agents "github.com/alamparelli/alf/internal/runtime/agents"
)

func newTestTeamsHandler(t *testing.T) (*TeamsHandler, string) {
	t.Helper()
	dataDir := t.TempDir()
	agentsDir := filepath.Join(dataDir, "agents", "teams")
	os.MkdirAll(agentsDir, 0o755)

	store := agents.NewFileAgentStore(agentsDir)
	h := &TeamsHandler{
		AgentStore: store,
		DataDir:    dataDir,
	}
	return h, agentsDir
}

func TestTeams_ListEmpty(t *testing.T) {
	h, _ := newTestTeamsHandler(t)

	req := httptest.NewRequest("GET", "/api/teams", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Teams []agents.TeamConfig `json:"teams"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Teams) != 0 {
		t.Errorf("expected 0 teams, got %d", len(resp.Teams))
	}
}

func TestTeams_SaveAndList(t *testing.T) {
	h, agentsDir := newTestTeamsHandler(t)

	team := `{"name":"test-team","description":"A test team","agents":[{"name":"worker","tier":"sonnet"}]}`
	req := httptest.NewRequest("PUT", "/api/teams", strings.NewReader(team))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Extract generated ID from response.
	var resp struct {
		OK   bool   `json:"ok"`
		ID   string `json:"id"`
		File string `json:"file"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.ID == "" {
		t.Fatal("expected non-empty id in response")
	}

	// Verify file was created using ID-based filename.
	data, err := os.ReadFile(filepath.Join(agentsDir, resp.File))
	if err != nil {
		t.Fatalf("team file not created: %v", err)
	}
	if !strings.Contains(string(data), "test-team") {
		t.Error("team file doesn't contain team name")
	}
}

func TestTeams_SaveRequiresName(t *testing.T) {
	h, _ := newTestTeamsHandler(t)

	req := httptest.NewRequest("PUT", "/api/teams", strings.NewReader(`{"description":"no name"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTeams_Delete(t *testing.T) {
	h, agentsDir := newTestTeamsHandler(t)

	// Create a team file.
	os.WriteFile(filepath.Join(agentsDir, "doomed.json"), []byte(`{"name":"doomed"}`), 0o644)

	req := httptest.NewRequest("DELETE", "/api/teams?name=doomed", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(filepath.Join(agentsDir, "doomed.json")); !os.IsNotExist(err) {
		t.Error("team file should have been deleted")
	}
}

func TestTeams_DeleteNotFound(t *testing.T) {
	h, _ := newTestTeamsHandler(t)

	req := httptest.NewRequest("DELETE", "/api/teams?name=nonexistent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTeams_SaveThenListShowsTeam(t *testing.T) {
	h, _ := newTestTeamsHandler(t)

	// Save a team.
	team := `{"name":"new-team","description":"fresh","agents":[{"name":"bot","tier":"haiku"}]}`
	req := httptest.NewRequest("PUT", "/api/teams", strings.NewReader(team))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// List immediately - should contain the new team (bug fix: store reloaded synchronously).
	req = httptest.NewRequest("GET", "/api/teams", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", rec.Code)
	}

	var resp struct {
		Teams []agents.TeamConfig `json:"teams"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	found := false
	for _, tc := range resp.Teams {
		if tc.Name == "new-team" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'new-team' in list after save, but not found")
	}
}

func TestTeams_SanitizeName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"my-team", "my-team"},
		{"../../../etc", "etc"},
		{"team name", "teamname"},
		{"team/slash", "teamslash"},
	}
	for _, c := range cases {
		got := sanitizeTeamName(c.input)
		if got != c.expected {
			t.Errorf("sanitizeTeamName(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}
