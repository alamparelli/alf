package tooling

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockTaskService struct {
	tasks     []TaskInfo
	launchID  string
	launchErr error
}

func (m *mockTaskService) Launch(_ context.Context, opts TaskLaunchOpts) (string, error) {
	if m.launchErr != nil {
		return "", m.launchErr
	}
	return m.launchID, nil
}

func (m *mockTaskService) List() []TaskInfo { return m.tasks }

func (m *mockTaskService) Cancel(id string) bool {
	for _, t := range m.tasks {
		if t.ID == id && t.Status == "running" {
			return true
		}
	}
	return false
}

func (m *mockTaskService) Delete(id string) bool {
	for _, t := range m.tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

func (m *mockTaskService) Approve(id string, approved bool, feedback string) bool {
	for _, t := range m.tasks {
		if t.ID == id && t.Status == "awaiting_approval" {
			return true
		}
	}
	return false
}

func TestTaskTool_Launch(t *testing.T) {
	svc := &mockTaskService{launchID: "task-abc123"}
	tool := TaskNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"launch","prompt":"analyze logs"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "task-abc123") {
		t.Fatalf("expected task ID in output, got: %s", out)
	}
}

func TestTaskTool_LaunchWithTeam(t *testing.T) {
	svc := &mockTaskService{launchID: "task-team1"}
	tool := TaskNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"launch","prompt":"deploy","team":"ops-team","skills":"docker,k8s"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "task-team1") {
		t.Fatalf("expected task ID in output, got: %s", out)
	}
}

func TestTaskTool_LaunchMissingPrompt(t *testing.T) {
	tool := TaskNativeTool{Service: &mockTaskService{}}

	_, err := tool.Run(context.Background(), `{"action":"launch"}`)
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("expected prompt required error, got: %v", err)
	}
}

func TestTaskTool_LaunchError(t *testing.T) {
	svc := &mockTaskService{launchErr: fmt.Errorf("tier not found")}
	tool := TaskNativeTool{Service: svc}

	_, err := tool.Run(context.Background(), `{"action":"launch","prompt":"test"}`)
	if err == nil || !strings.Contains(err.Error(), "tier not found") {
		t.Fatalf("expected launch error, got: %v", err)
	}
}

func TestTaskTool_List(t *testing.T) {
	svc := &mockTaskService{
		tasks: []TaskInfo{
			{ID: "t1", Prompt: "test", Status: "running"},
			{ID: "t2", Prompt: "done", Status: "completed"},
		},
	}
	tool := TaskNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "t1") || !strings.Contains(out, "t2") {
		t.Fatalf("expected task IDs in output, got: %s", out)
	}
}

func TestTaskTool_ListEmpty(t *testing.T) {
	tool := TaskNativeTool{Service: &mockTaskService{}}

	out, err := tool.Run(context.Background(), `{"action":"list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No tasks") {
		t.Fatalf("expected empty message, got: %s", out)
	}
}

func TestTaskTool_Cancel(t *testing.T) {
	svc := &mockTaskService{
		tasks: []TaskInfo{{ID: "t1", Status: "running"}},
	}
	tool := TaskNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"cancel","id":"t1"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "cancelled") {
		t.Fatalf("expected cancelled message, got: %s", out)
	}
}

func TestTaskTool_CancelNotFound(t *testing.T) {
	tool := TaskNativeTool{Service: &mockTaskService{}}

	_, err := tool.Run(context.Background(), `{"action":"cancel","id":"bad"}`)
	if err == nil {
		t.Fatal("expected error for missing task")
	}
}

func TestTaskTool_CancelMissingID(t *testing.T) {
	tool := TaskNativeTool{Service: &mockTaskService{}}

	_, err := tool.Run(context.Background(), `{"action":"cancel"}`)
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("expected id required error, got: %v", err)
	}
}

func TestTaskTool_Delete(t *testing.T) {
	svc := &mockTaskService{
		tasks: []TaskInfo{{ID: "t1", Status: "completed"}},
	}
	tool := TaskNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"delete","id":"t1"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "deleted") {
		t.Fatalf("expected deleted message, got: %s", out)
	}
}

func TestTaskTool_Approve(t *testing.T) {
	svc := &mockTaskService{
		tasks: []TaskInfo{{ID: "t1", Status: "awaiting_approval"}},
	}
	tool := TaskNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"approve","id":"t1","approved":true}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "approved") {
		t.Fatalf("expected approved message, got: %s", out)
	}
}

func TestTaskTool_Reject(t *testing.T) {
	svc := &mockTaskService{
		tasks: []TaskInfo{{ID: "t1", Status: "awaiting_approval"}},
	}
	tool := TaskNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"action":"approve","id":"t1","approved":false,"feedback":"needs more work"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "rejected") {
		t.Fatalf("expected rejected message, got: %s", out)
	}
}

func TestTaskTool_ApproveMissingApproved(t *testing.T) {
	tool := TaskNativeTool{Service: &mockTaskService{}}

	_, err := tool.Run(context.Background(), `{"action":"approve","id":"t1"}`)
	if err == nil || !strings.Contains(err.Error(), "approved") {
		t.Fatalf("expected approved required error, got: %v", err)
	}
}

func TestTaskTool_UnknownAction(t *testing.T) {
	tool := TaskNativeTool{Service: &mockTaskService{}}

	_, err := tool.Run(context.Background(), `{"action":"explode"}`)
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown action error, got: %v", err)
	}
}

func TestTaskTool_InvalidJSON(t *testing.T) {
	tool := TaskNativeTool{Service: &mockTaskService{}}

	_, err := tool.Run(context.Background(), `{bad json}`)
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}

// --- Chain action tests ---

func TestTaskTool_Chain(t *testing.T) {
	svc := &mockLLMService{result: "Bruxelles est la capitale"}
	var notified sync.WaitGroup
	notified.Add(1)

	var gotChainID, gotStatus, gotMessage string
	tool := TaskNativeTool{
		Service: &mockTaskService{},
		LLMService: svc,
		NotifyFunc: func(_ ChainOrigin, chainID, status, message string) {
			gotChainID = chainID
			gotStatus = status
			gotMessage = message
			notified.Done()
		},
	}

	out, err := tool.Run(context.Background(), `{"action":"chain","steps":[{"tier":"haiku","prompt":"fact about Brussels"},{"tier":"sonnet","prompt":"make tweet: {result}"}]}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "chain_id") {
		t.Fatalf("expected chain_id in output, got: %s", out)
	}

	// Wait for async chain completion.
	done := make(chan struct{})
	go func() { notified.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("chain did not complete in time")
	}

	if gotStatus != "completed" {
		t.Fatalf("expected completed, got %s", gotStatus)
	}
	if gotChainID == "" {
		t.Fatal("expected non-empty chain ID")
	}

	// Verify 2 LLM calls were made.
	calls := svc.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(calls))
	}
	if calls[0].Tier != "haiku" {
		t.Fatalf("expected first call tier=haiku, got %s", calls[0].Tier)
	}
	if calls[1].Tier != "sonnet" {
		t.Fatalf("expected second call tier=sonnet, got %s", calls[1].Tier)
	}
	// Second prompt should have {result} replaced.
	if !strings.Contains(calls[1].Prompt, "chain_result") {
		t.Fatalf("expected chain_result injection in second prompt, got: %s", calls[1].Prompt)
	}
	if gotMessage != "Bruxelles est la capitale" {
		t.Fatalf("expected final message, got: %s", gotMessage)
	}
}

func TestTaskTool_ChainTooFewSteps(t *testing.T) {
	tool := TaskNativeTool{
		Service:    &mockTaskService{},
		LLMService: &mockLLMService{},
	}

	_, err := tool.Run(context.Background(), `{"action":"chain","steps":[{"tier":"haiku","prompt":"only one"}]}`)
	if err == nil || !strings.Contains(err.Error(), "at least 2 steps") {
		t.Fatalf("expected 2 steps error, got: %v", err)
	}
}

func TestTaskTool_ChainNoLLMService(t *testing.T) {
	tool := TaskNativeTool{Service: &mockTaskService{}}

	_, err := tool.Run(context.Background(), `{"action":"chain","steps":[{"tier":"a","prompt":"x"},{"tier":"b","prompt":"y"}]}`)
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected not available error, got: %v", err)
	}
}

func TestTaskTool_ChainStepError(t *testing.T) {
	svc := &mockLLMService{err: fmt.Errorf("tier not found")}
	var notified sync.WaitGroup
	notified.Add(1)

	var gotStatus string
	tool := TaskNativeTool{
		Service:    &mockTaskService{},
		LLMService: svc,
		NotifyFunc: func(_ ChainOrigin, _, status, _ string) {
			gotStatus = status
			notified.Done()
		},
	}

	_, err := tool.Run(context.Background(), `{"action":"chain","steps":[{"tier":"bad","prompt":"x"},{"tier":"sonnet","prompt":"{result}"}]}`)
	if err != nil {
		t.Fatalf("chain launch should not error: %v", err)
	}

	done := make(chan struct{})
	go func() { notified.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("chain did not complete in time")
	}

	if gotStatus != "failed" {
		t.Fatalf("expected failed status, got %s", gotStatus)
	}
}

func TestTaskTool_ToolName(t *testing.T) {
	tool := TaskNativeTool{}
	if tool.ToolName() != "agent_task" {
		t.Fatalf("expected 'agent_task', got %q", tool.ToolName())
	}
}

func TestTaskTool_Schema(t *testing.T) {
	tool := TaskNativeTool{}
	s := tool.Schema()
	if s.Name != "agent_task" {
		t.Fatalf("expected schema name 'agent_task', got %q", s.Name)
	}
	props, ok := s.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties in schema")
	}
	if _, ok := props["action"]; !ok {
		t.Fatal("expected 'action' property in schema")
	}
}
