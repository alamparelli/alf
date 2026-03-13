package controlcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/provider"
)

// slowProvider blocks until released, simulating a long-running agent call.
type slowProvider struct {
	invocations atomic.Int32
	gate        chan struct{} // close to unblock all calls
}

func newSlowProvider() *slowProvider {
	return &slowProvider{gate: make(chan struct{})}
}

func (p *slowProvider) Invoke(ctx context.Context, _ string, _ provider.Params, _ provider.OnProgress) (*provider.Result, error) {
	p.invocations.Add(1)
	select {
	case <-p.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Return a valid orchestrator JSON response that ends the run.
	return &provider.Result{
		Text:  `{"response": "done"}`,
		Model: "mock",
	}, nil
}

func (p *slowProvider) release() { close(p.gate) }

// newTestOrchestrator creates an orchestrator backed by the given provider.
func newTestOrchestrator(t *testing.T, prov provider.Provider) *agents.Orchestrator {
	t.Helper()
	dataDir := t.TempDir()
	// Seed a minimal team so orchestrator doesn't error on "no teams".
	teamsDir := filepath.Join(dataDir, "teams")
	os.MkdirAll(teamsDir, 0o755)
	os.WriteFile(filepath.Join(teamsDir, "default.json"), []byte(`{
		"name": "default",
		"description": "test team",
		"agents": [{"name": "worker", "description": "test", "tier": "mock"}]
	}`), 0o644)
	store := agents.NewFileAgentStore(teamsDir)
	return agents.NewOrchestrator(prov, store, dataDir, nil, nil)
}

// --- POST /api/tasks ---

func TestTasksHandler_LaunchReturnsOK(t *testing.T) {
	orch := newTestOrchestrator(t, &mockProvider{})

	h := &TasksHandler{Orchestrator: orch, DataDir: t.TempDir(), ContextDir: t.TempDir()}
	req := httptest.NewRequest("POST", "/api/tasks", strings.NewReader(`{"message":"build something"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	// Wait for background goroutine to finish.
	time.Sleep(50 * time.Millisecond)
}

func TestTasksHandler_LaunchEmptyMessage(t *testing.T) {
	orch := newTestOrchestrator(t, &mockProvider{})
	h := &TasksHandler{Orchestrator: orch, DataDir: t.TempDir(), ContextDir: t.TempDir()}

	req := httptest.NewRequest("POST", "/api/tasks", strings.NewReader(`{"message":""}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestTasksHandler_LaunchNoOrchestrator(t *testing.T) {
	h := &TasksHandler{Orchestrator: nil, DataDir: t.TempDir(), ContextDir: t.TempDir()}
	req := httptest.NewRequest("POST", "/api/tasks", strings.NewReader(`{"message":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

func TestTasksHandler_LaunchIsNonBlocking(t *testing.T) {
	sp := newSlowProvider()
	orch := newTestOrchestrator(t, sp)
	h := &TasksHandler{Orchestrator: orch, DataDir: t.TempDir(), ContextDir: t.TempDir()}

	// Launch should return immediately even though the provider blocks.
	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest("POST", "/api/tasks", strings.NewReader(`{"message":"test task"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
		// Good — returned before provider finished.
	case <-time.After(2 * time.Second):
		t.Fatal("launch blocked waiting for orchestrator to complete")
	}

	// Release the provider so the background goroutine can finish cleanly.
	sp.release()
	time.Sleep(50 * time.Millisecond)
}

func TestTasksHandler_ConcurrentTasksRun(t *testing.T) {
	sp := newSlowProvider()
	defer sp.release()
	orch := newTestOrchestrator(t, sp)
	h := &TasksHandler{Orchestrator: orch, DataDir: t.TempDir(), ContextDir: t.TempDir()}

	// Launch 3 tasks concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/api/tasks", strings.NewReader(`{"message":"concurrent task"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", rec.Code)
			}
		}()
	}
	wg.Wait()

	// All 3 should be tracked as running.
	// Wait briefly for goroutines to register with orchestrator.
	time.Sleep(100 * time.Millisecond)
	running := orch.Running()
	if len(running) < 2 {
		t.Errorf("expected at least 2 concurrent running tasks, got %d", len(running))
	}
}

// --- Existing GET/DELETE tests ---

func TestTasksHandler_ListEmpty(t *testing.T) {
	h := &TasksHandler{Orchestrator: nil, DataDir: t.TempDir(), ContextDir: t.TempDir()}
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	// running/completed should be null or empty arrays.
	if resp["running"] != nil {
		if arr, ok := resp["running"].([]any); ok && len(arr) > 0 {
			t.Errorf("expected empty running, got %v", resp["running"])
		}
	}
}

func TestTasksHandler_CancelMissingID(t *testing.T) {
	orch := newTestOrchestrator(t, &mockProvider{})
	h := &TasksHandler{Orchestrator: orch, DataDir: t.TempDir(), ContextDir: t.TempDir()}
	req := httptest.NewRequest("DELETE", "/api/tasks", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestTasksHandler_MethodNotAllowed(t *testing.T) {
	h := &TasksHandler{Orchestrator: nil, DataDir: t.TempDir(), ContextDir: t.TempDir()}
	req := httptest.NewRequest("PATCH", "/api/tasks", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
