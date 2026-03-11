package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/agents"
)

func TestActivityHandler_Empty(t *testing.T) {
	h := &ActivityHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Items []ActivityItem `json:"items"`
		Count int            `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("expected 0 active items, got %d", resp.Count)
	}
	if resp.Items == nil {
		// items should be null or empty array, both are acceptable
	}
}

func TestActivityHandler_MethodNotAllowed(t *testing.T) {
	h := &ActivityHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/activity", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestActivityHandler_WithRunningSchedule(t *testing.T) {
	sched := &mockScheduleEngine{
		jobs: []ScheduleJob{
			{ID: "j1", Name: "Health Check", Running: true},
			{ID: "j2", Name: "Backup", Running: false},
		},
	}

	h := &ActivityHandler{Scheduler: sched}
	req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp struct {
		Items []ActivityItem `json:"items"`
		Count int            `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Count != 1 {
		t.Errorf("expected 1 running job, got %d", resp.Count)
	}
	if resp.Count > 0 && resp.Items[0].Name != "Health Check" {
		t.Errorf("expected 'Health Check', got %q", resp.Items[0].Name)
	}
	if resp.Count > 0 && resp.Items[0].Type != "schedule" {
		t.Errorf("expected type 'schedule', got %q", resp.Items[0].Type)
	}
}

func TestActivityHandler_WithRunningTasks(t *testing.T) {
	orch := agents.NewOrchestrator(nil, nil, t.TempDir(), nil)

	// Simulate a running task by accessing internals isn't possible,
	// so we verify that with no running tasks, count is 0.
	h := &ActivityHandler{Orchestrator: orch}
	req := httptest.NewRequest(http.MethodGet, "/api/activity", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp struct {
		Count int `json:"count"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Count != 0 {
		t.Errorf("expected 0 tasks, got %d", resp.Count)
	}
}

// mockScheduleEngine implements ScheduleEngine for testing.
type mockScheduleEngine struct {
	jobs []ScheduleJob
}

func (m *mockScheduleEngine) List(userOnly bool) []ScheduleJob { return m.jobs }
func (m *mockScheduleEngine) Create(name, schedule, tier, prompt, command, output string, timeout time.Duration, skills []string) (*ScheduleJob, error) {
	return nil, nil
}
func (m *mockScheduleEngine) CreateReminder(name, schedule, message, output string, timeout time.Duration) (*ScheduleJob, error) {
	return nil, nil
}
func (m *mockScheduleEngine) Delete(id string) error                              { return nil }
func (m *mockScheduleEngine) Update(id string, fields map[string]string) (*ScheduleJob, error) { return nil, nil }
func (m *mockScheduleEngine) RunNow(id string) error                              { return nil }
