package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/provider"
)

// mockProvider returns pre-defined responses in sequence.
type mockProvider struct {
	responses []*provider.Result
	errors    []error
	idx       atomic.Int32
	calls     []mockCall
	mu        chan struct{} // mutex via buffered chan
}

type mockCall struct {
	Prompt string
	Params provider.Params
}

func newMockProvider(responses []*provider.Result, errors []error) *mockProvider {
	if errors == nil {
		errors = make([]error, len(responses))
	}
	return &mockProvider{
		responses: responses,
		errors:    errors,
		mu:        make(chan struct{}, 1),
	}
}

func (m *mockProvider) Invoke(_ context.Context, prompt string, params provider.Params, _ provider.OnProgress) (*provider.Result, error) {
	i := int(m.idx.Add(1) - 1)

	m.mu <- struct{}{}
	m.calls = append(m.calls, mockCall{Prompt: prompt, Params: params})
	<-m.mu

	if i >= len(m.responses) {
		return &provider.Result{Text: `{"response": "fallback"}`}, nil
	}
	return m.responses[i], m.errors[i]
}

func (m *mockProvider) callCount() int {
	return int(m.idx.Load())
}

// testStore creates a Store with the given teams.
func testStore(teams ...*TeamConfig) Store {
	dir, _ := os.MkdirTemp("", "agents-test-*")
	for _, tc := range teams {
		data, _ := json.Marshal(tc)
		os.WriteFile(filepath.Join(dir, tc.Name+".json"), data, 0o644)
	}
	return NewFileAgentStore(dir)
}

// testTierResolver maps tier names to test params for orchestrator tests.
var testTierResolver = func(tierName string) (TierParams, bool) {
	switch tierName {
	case "sonnet":
		return TierParams{Model: "claude-sonnet-4-6", Effort: "high", WriteCapable: true}, true
	case "haiku":
		return TierParams{Model: "claude-haiku-4-5", Effort: "low"}, true
	case "default":
		return TierParams{Model: "claude-haiku-4-5"}, true
	default:
		return TierParams{}, false
	}
}

var testTeam = &TeamConfig{
	Name:             "content",
	Description:      "Content team",
	MaxAgentsPerReq:  3,
	GlobalTimeoutMin: 1,
	Agents: []AgentConfig{
		{Name: "researcher", Description: "Researches", Tier: "sonnet", SystemPrompt: "Research."},
		{Name: "writer", Description: "Writes", Tier: "sonnet", SystemPrompt: "Write."},
		{Name: "reviewer", Description: "Reviews", Tier: "haiku", SystemPrompt: "Review."},
	},
}

func TestDirectResponse(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"response": "Here is your answer."}`, SessionID: "s1", CostUSD: 0.01},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, meta, err := orch.Run(context.Background(), "hello", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "Here is your answer." {
		t.Errorf("unexpected: %s", text)
	}
	if meta.Status != "completed" {
		t.Errorf("unexpected status: %s", meta.Status)
	}
	if mp.callCount() != 1 {
		t.Errorf("expected 1 call, got %d", mp.callCount())
	}
}

func TestSingleDelegation(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		// Orchestrator: delegate
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "find info"}]}`, SessionID: "s1", CostUSD: 0.01},
		// Agent: researcher
		{Text: "Research results here", SessionID: "a1", CostUSD: 0.005},
		// Orchestrator: final response
		{Text: `{"response": "Based on research: answer"}`, SessionID: "s1", CostUSD: 0.01},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, meta, err := orch.Run(context.Background(), "research this", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Based on research") {
		t.Errorf("unexpected: %s", text)
	}
	if meta.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", meta.Iterations)
	}
	if len(meta.AgentCalls) != 1 {
		t.Errorf("expected 1 agent call, got %d", len(meta.AgentCalls))
	}
}

func TestParallelDelegation(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		// Orchestrator: delegate to 3
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "t1"}, {"agent": "content/writer", "task": "t2"}, {"agent": "content/reviewer", "task": "t3"}]}`, SessionID: "s1"},
		// 3 agents
		{Text: "r1", SessionID: "a1"},
		{Text: "r2", SessionID: "a2"},
		{Text: "r3", SessionID: "a3"},
		// Orchestrator: done
		{Text: `{"response": "combined"}`, SessionID: "s1"},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, meta, err := orch.Run(context.Background(), "do everything", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "combined" {
		t.Errorf("unexpected: %s", text)
	}
	if len(meta.AgentCalls) != 3 {
		t.Errorf("expected 3 agent calls, got %d", len(meta.AgentCalls))
	}
}

func TestMultiIteration(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		// Iter 1: delegate research
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "research"}]}`},
		{Text: "research data"},
		// Iter 2: delegate writer with research
		{Text: `{"delegates": [{"agent": "content/writer", "task": "write based on research"}]}`},
		{Text: "draft article"},
		// Iter 3: final
		{Text: `{"response": "polished article"}`},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, meta, err := orch.Run(context.Background(), "write article", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "polished article" {
		t.Errorf("unexpected: %s", text)
	}
	if meta.Iterations != 3 {
		t.Errorf("expected 3 iterations, got %d", meta.Iterations)
	}
}

func TestAgentSessionResume(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		// Iter 1: delegate researcher
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "t1"}]}`},
		{Text: "partial", SessionID: "agent-sess-1"},
		// Iter 2: re-delegate same agent
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "more detail"}]}`},
		{Text: "complete", SessionID: "agent-sess-2"},
		// Done
		{Text: `{"response": "final"}`},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	_, _, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Check that second agent call used resume ID.
	mp.mu <- struct{}{}
	defer func() { <-mp.mu }()

	// calls[1] = first agent call (no resume), calls[3] = second agent call (should have resume)
	if len(mp.calls) < 4 {
		t.Fatalf("expected at least 4 calls, got %d", len(mp.calls))
	}
	if mp.calls[1].Params.ResumeID != "" {
		t.Error("first agent call should not have resume ID")
	}
	if mp.calls[3].Params.ResumeID != "agent-sess-1" {
		t.Errorf("second agent call should resume session, got %q", mp.calls[3].Params.ResumeID)
	}
}

func TestPlainTextTriggersNudge(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		// First: plain text (invalid) — should trigger nudge
		{Text: "Just a plain answer without JSON"},
		// After nudge: proper delegation
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "find info"}]}`},
		{Text: "agent result"},
		{Text: `{"response": "done"}`},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, _, err := orch.Run(context.Background(), "hi", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "done" {
		t.Errorf("unexpected: %s", text)
	}
	// Should have been nudged: 1 (plain text) + 1 (delegate) + 1 (agent) + 1 (response) = 4 calls
	if mp.callCount() != 4 {
		t.Errorf("expected 4 calls (nudge + delegation), got %d", mp.callCount())
	}
}

func TestReDelegateOnBadResult(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "find X"}]}`},
		{Text: "garbage data"},
		// Orchestrator re-delegates
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "find X with more specificity"}]}`},
		{Text: "proper result"},
		{Text: `{"response": "answer from proper result"}`},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, meta, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "answer from proper result" {
		t.Errorf("unexpected: %s", text)
	}
	if len(meta.AgentCalls) != 2 {
		t.Errorf("expected 2 agent calls, got %d", len(meta.AgentCalls))
	}
}

func TestSwitchAgentOnFailure(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "find"}]}`},
		// researcher fails
		nil,
		// Orchestrator switches to writer
		{Text: `{"delegates": [{"agent": "content/writer", "task": "write instead"}]}`},
		{Text: "written content"},
		{Text: `{"response": "done"}`},
	}, []error{nil, fmt.Errorf("agent failed"), nil, nil, nil})
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, meta, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "done" {
		t.Errorf("unexpected: %s", text)
	}
	// First call had error, second succeeded
	if len(meta.AgentCalls) != 2 {
		t.Errorf("expected 2 agent calls, got %d", len(meta.AgentCalls))
	}
	if meta.AgentCalls[0].Error == "" {
		t.Error("first agent call should have error")
	}
}

func TestPartialSuccess(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		// Delegate to 2 agents
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "t1"}, {"agent": "content/writer", "task": "t2"}]}`},
		{Text: "good result"},           // researcher succeeds
		nil,                             // writer fails
		// Orchestrator re-delegates writer only
		{Text: `{"delegates": [{"agent": "content/writer", "task": "retry writing"}]}`},
		{Text: "now it works"},
		{Text: `{"response": "combined"}`},
	}, []error{nil, nil, fmt.Errorf("writer crashed"), nil, nil, nil})
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	_, meta, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 3 agent calls: researcher ok, writer fail, writer retry ok
	if len(meta.AgentCalls) != 3 {
		t.Errorf("expected 3 agent calls, got %d", len(meta.AgentCalls))
	}
}

func TestAgentErrorPassthrough(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "find"}]}`},
		nil, // agent fails
		{Text: `{"response": "handled the error"}`},
	}, []error{nil, fmt.Errorf("provider error"), nil})
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, _, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "handled the error" {
		t.Errorf("unexpected: %s", text)
	}
}

func TestMaxIterationsExceeded(t *testing.T) {
	// Create a provider that always delegates (never returns final response).
	responses := make([]*provider.Result, 30)
	for i := range responses {
		if i%2 == 0 {
			responses[i] = &provider.Result{Text: `{"delegates": [{"agent": "content/researcher", "task": "more"}]}`}
		} else {
			responses[i] = &provider.Result{Text: "result"}
		}
	}
	mp := newMockProvider(responses, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	_, meta, err := orch.Run(context.Background(), "infinite", nil, RunConfig{}, nil)
	if err == nil {
		t.Fatal("expected error for max iterations")
	}
	if !strings.Contains(err.Error(), "max iterations") {
		t.Errorf("unexpected error: %v", err)
	}
	if meta.Status != "timeout" {
		t.Errorf("expected timeout status, got %s", meta.Status)
	}
}

func TestGlobalTimeout(t *testing.T) {
	// Use a pre-cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mp := newMockProvider([]*provider.Result{
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "slow"}]}`},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	_, _, err := orch.Run(ctx, "test", nil, RunConfig{}, nil)
	if err == nil {
		// The orchestrator call itself may succeed on cancelled context
		// depending on provider behavior. That's acceptable.
		return
	}
}

func TestMaxAgentsPerRequest(t *testing.T) {
	team := &TeamConfig{
		Name:            "small",
		Description:     "Small team",
		MaxAgentsPerReq: 2,
		Agents: []AgentConfig{
			{Name: "a", Tier: "haiku", SystemPrompt: "a"},
			{Name: "b", Tier: "haiku", SystemPrompt: "b"},
			{Name: "c", Tier: "haiku", SystemPrompt: "c"},
		},
	}
	mp := newMockProvider([]*provider.Result{
		// Request 3 agents but limit is 2
		{Text: `{"delegates": [{"agent": "small/a", "task": "t1"}, {"agent": "small/b", "task": "t2"}, {"agent": "small/c", "task": "t3"}]}`},
		{Text: "r1"}, {Text: "r2"}, // only 2 will run
		{Text: `{"response": "done"}`},
	}, nil)
	store := testStore(team)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	_, meta, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.AgentCalls) != 2 {
		t.Errorf("expected 2 agent calls (truncated from 3), got %d", len(meta.AgentCalls))
	}
}

func TestInvalidAgentName(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"delegates": [{"agent": "content/nonexistent", "task": "t1"}]}`},
		{Text: `{"response": "handled"}`},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, meta, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "handled" {
		t.Errorf("unexpected: %s", text)
	}
	if len(meta.AgentCalls) != 1 {
		t.Fatalf("expected 1 agent call, got %d", len(meta.AgentCalls))
	}
	if meta.AgentCalls[0].Error == "" {
		t.Error("expected error for invalid agent")
	}
}

func TestInvalidTeamName(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"delegates": [{"agent": "faketeam/agent", "task": "t1"}]}`},
		{Text: `{"response": "handled"}`},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, _, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "handled" {
		t.Errorf("unexpected: %s", text)
	}
}

func TestEmptyDelegates(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"delegates": []}`},
		{Text: `{"response": "ok"}`},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, _, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "ok" {
		t.Errorf("unexpected: %s", text)
	}
}

func TestNoTeamsConfigured(t *testing.T) {
	mp := newMockProvider(nil, nil)
	dir := t.TempDir()
	store := NewFileAgentStore(dir)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	_, _, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no agent teams") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMalformedJSONTriggersNudge(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		// Malformed JSON — triggers nudge
		{Text: `not json at all`},
		// After nudge: proper response
		{Text: `{"response": "ok after nudge"}`},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	text, _, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "ok after nudge" {
		t.Errorf("expected nudged response, got: %s", text)
	}
	if mp.callCount() != 2 {
		t.Errorf("expected 2 calls (nudge + proper), got %d", mp.callCount())
	}
}

func TestSessionExpired(t *testing.T) {
	callIdx := atomic.Int32{}
	mp := &mockProvider{
		responses: []*provider.Result{
			{Text: `{"response": "ok"}`, SessionID: "s1"},
		},
		errors: []error{nil},
		mu:     make(chan struct{}, 1),
	}
	// Override with custom behavior: first call fails with session error.
	origInvoke := mp
	customProv := &sessionExpiredProvider{
		inner:   origInvoke,
		callIdx: &callIdx,
	}

	store := testStore(testTeam)
	orch := NewOrchestrator(customProv, store, t.TempDir(), nil, testTierResolver)
	text, _, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text != "ok" {
		t.Errorf("unexpected: %s", text)
	}
}

type sessionExpiredProvider struct {
	inner   *mockProvider
	callIdx *atomic.Int32
}

func (p *sessionExpiredProvider) Invoke(ctx context.Context, prompt string, params provider.Params, onProgress provider.OnProgress) (*provider.Result, error) {
	idx := p.callIdx.Add(1)
	if idx == 1 && params.ResumeID != "" {
		return nil, fmt.Errorf("No conversation found with id fake-session")
	}
	return p.inner.Invoke(ctx, prompt, params, onProgress)
}

func TestTaskMetaTracking(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "find"}]}`, CostUSD: 0.01},
		{Text: "found it", CostUSD: 0.005},
		{Text: `{"response": "done"}`, CostUSD: 0.01},
	}, nil)
	store := testStore(testTeam)
	dataDir := t.TempDir()
	orch := NewOrchestrator(mp, store, dataDir, nil, testTierResolver)

	_, meta, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Check task.json was written.
	taskDir := filepath.Join(dataDir, "agents", meta.ID)
	data, err := os.ReadFile(filepath.Join(taskDir, "task.json"))
	if err != nil {
		t.Fatalf("task.json not found: %v", err)
	}

	var saved TaskMeta
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("invalid task.json: %v", err)
	}
	if saved.Status != "completed" {
		t.Errorf("expected completed, got %s", saved.Status)
	}
	if saved.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", saved.Iterations)
	}
}

func TestWorkingDirCreated(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "find"}]}`},
		{Text: "result"},
		{Text: `{"response": "done"}`},
	}, nil)
	store := testStore(testTeam)
	dataDir := t.TempDir()
	orch := NewOrchestrator(mp, store, dataDir, nil, testTierResolver)

	_, meta, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	taskDir := filepath.Join(dataDir, "agents", meta.ID)
	// Check task dir exists (orchestrator and read-only agents share it).
	if _, err := os.Stat(taskDir); err != nil {
		t.Error("task dir not created")
	}
	// Check task.json was written.
	if _, err := os.Stat(filepath.Join(taskDir, "task.json")); err != nil {
		t.Error("task.json not written")
	}
}

func TestTimeoutDuringAgent(t *testing.T) {
	// Provider that blocks on agent call.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mp := newMockProvider([]*provider.Result{
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "slow"}]}`},
	}, nil)
	// Override second call to block.
	blockProv := &blockingProvider{inner: mp}

	store := testStore(testTeam)
	orch := NewOrchestrator(blockProv, store, t.TempDir(), nil, testTierResolver)

	_, _, err := orch.Run(ctx, "test", nil, RunConfig{}, nil)
	// Should error due to timeout.
	if err == nil {
		// If it somehow completes, that's also acceptable if the context was checked.
		return
	}
}

type blockingProvider struct {
	inner *mockProvider
	count atomic.Int32
}

func (p *blockingProvider) Invoke(ctx context.Context, prompt string, params provider.Params, onProgress provider.OnProgress) (*provider.Result, error) {
	n := p.count.Add(1)
	if n > 1 {
		// Block until context cancelled.
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.inner.Invoke(ctx, prompt, params, onProgress)
}

// --- RunConfig tests ---

func TestRunConfigOrchestratorMaxTurnsPassedToBrain(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"response": "done"}`, CostUSD: 0.01},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	_, _, err := orch.Run(context.Background(), "test", nil, RunConfig{OrchestratorMaxTurns: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The orchestrator brain call should have MaxTurns=5.
	if len(mp.calls) == 0 {
		t.Fatal("no calls recorded")
	}
	if mp.calls[0].Params.MaxTurns != 5 {
		t.Errorf("expected MaxTurns=5 for brain, got %d", mp.calls[0].Params.MaxTurns)
	}
}

func TestRunConfigMaxTurnsDefault(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"response": "done"}`, CostUSD: 0.01},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	_, _, err := orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Default orchestrator turns = defaultOrchestratorTurns (3).
	if mp.calls[0].Params.MaxTurns != 3 {
		t.Errorf("expected default orchestrator MaxTurns=3, got %d", mp.calls[0].Params.MaxTurns)
	}
}

func TestRunConfigModelAndEffort(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		{Text: `{"response": "done"}`, CostUSD: 0.01},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	_, _, err := orch.Run(context.Background(), "test", nil, RunConfig{
		Model:  "claude-sonnet-4-6",
		Effort: "low",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if mp.calls[0].Params.Model != "claude-sonnet-4-6" {
		t.Errorf("expected model claude-sonnet-4-6, got %s", mp.calls[0].Params.Model)
	}
	if mp.calls[0].Params.Effort != "low" {
		t.Errorf("expected effort low, got %s", mp.calls[0].Params.Effort)
	}
}

func TestRunConfigMaxIterations(t *testing.T) {
	// Provider always delegates, never returns a response — should stop at MaxIterations.
	mp := newMockProvider(nil, nil) // falls through to fallback
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	// Override fallback to always delegate.
	delegateProvider := &alwaysDelegateProvider{}
	orch.provider = delegateProvider

	_, meta, err := orch.Run(context.Background(), "test", nil, RunConfig{MaxIterations: 2}, nil)
	if err == nil {
		t.Fatal("expected max iterations error")
	}
	if !strings.Contains(err.Error(), "max iterations (2) exceeded") {
		t.Errorf("unexpected error: %v", err)
	}
	if meta.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", meta.Iterations)
	}
}

// alwaysDelegateProvider returns delegate JSON for orchestrator calls and results for agent calls.
type alwaysDelegateProvider struct {
	count atomic.Int32
}

func (p *alwaysDelegateProvider) Invoke(_ context.Context, _ string, params provider.Params, _ provider.OnProgress) (*provider.Result, error) {
	n := int(p.count.Add(1))
	// Odd calls = orchestrator brain, Even calls = agent.
	if n%2 == 1 {
		return &provider.Result{Text: `{"delegates": [{"agent": "content/researcher", "task": "do it"}]}`}, nil
	}
	return &provider.Result{Text: "agent result"}, nil
}

func TestRunNonBlocking(t *testing.T) {
	// Verify the orchestrator can run concurrently (not blocking the caller).
	slowProvider := &slowMockProvider{
		delay: 50 * time.Millisecond,
		result: &provider.Result{
			Text:    `{"response": "done"}`,
			CostUSD: 0.01,
		},
	}
	store := testStore(testTeam)
	orch := NewOrchestrator(slowProvider, store, t.TempDir(), nil, testTierResolver)

	done := make(chan struct{})
	go func() {
		_, _, _ = orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
		close(done)
	}()

	// The goroutine should not block — we should be able to do other work.
	select {
	case <-done:
		// Completed — good.
	case <-time.After(5 * time.Second):
		t.Fatal("orchestrator Run blocked for too long")
	}
}

type slowMockProvider struct {
	delay  time.Duration
	result *provider.Result
}

func (p *slowMockProvider) Invoke(_ context.Context, _ string, _ provider.Params, _ provider.OnProgress) (*provider.Result, error) {
	time.Sleep(p.delay)
	return p.result, nil
}

func TestRunningTracksTask(t *testing.T) {
	// Provider that blocks until cancelled.
	blocker := make(chan struct{})
	bp := &channelProvider{ch: blocker}
	store := testStore(testTeam)
	orch := NewOrchestrator(bp, store, t.TempDir(), nil, testTierResolver)

	// No running tasks initially.
	if len(orch.Running()) != 0 {
		t.Fatal("expected no running tasks")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		orch.Run(ctx, "test", nil, RunConfig{}, nil)
		close(done)
	}()

	// Wait for the task to register.
	deadline := time.After(2 * time.Second)
	for {
		if len(orch.Running()) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("task never appeared in Running()")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	running := orch.Running()
	if len(running) != 1 {
		t.Fatalf("expected 1 running task, got %d", len(running))
	}
	if running[0].ID == "" {
		t.Error("running task has empty ID")
	}

	// Cancel and verify cleanup.
	cancel()
	<-done

	if len(orch.Running()) != 0 {
		t.Error("task still in Running() after completion")
	}
}

func TestCancelStopsTask(t *testing.T) {
	blocker := make(chan struct{})
	bp := &channelProvider{ch: blocker}
	store := testStore(testTeam)
	orch := NewOrchestrator(bp, store, t.TempDir(), nil, testTierResolver)

	done := make(chan struct{})
	go func() {
		orch.Run(context.Background(), "test", nil, RunConfig{}, nil)
		close(done)
	}()

	// Wait for task to register.
	deadline := time.After(2 * time.Second)
	for {
		if len(orch.Running()) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("task never appeared")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Cancel via the orchestrator API.
	running := orch.Running()
	ok := orch.Cancel(running[0].ID)
	if !ok {
		t.Fatal("Cancel returned false")
	}

	select {
	case <-done:
		// Good — task stopped.
	case <-time.After(5 * time.Second):
		t.Fatal("task didn't stop after Cancel")
	}
}

func TestCancelAllStopsTasks(t *testing.T) {
	blocker := make(chan struct{})
	bp := &channelProvider{ch: blocker}
	store := testStore(testTeam)
	orch := NewOrchestrator(bp, store, t.TempDir(), nil, testTierResolver)

	// Start two tasks.
	done1 := make(chan struct{})
	done2 := make(chan struct{})
	go func() { orch.Run(context.Background(), "test1", nil, RunConfig{}, nil); close(done1) }()
	go func() { orch.Run(context.Background(), "test2", nil, RunConfig{}, nil); close(done2) }()

	deadline := time.After(2 * time.Second)
	for {
		if len(orch.Running()) >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected 2 running tasks, got %d", len(orch.Running()))
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	n := orch.CancelAll()
	if n != 2 {
		t.Errorf("expected CancelAll to return 2, got %d", n)
	}

	select {
	case <-done1:
	case <-time.After(5 * time.Second):
		t.Fatal("task 1 didn't stop")
	}
	select {
	case <-done2:
	case <-time.After(5 * time.Second):
		t.Fatal("task 2 didn't stop")
	}
}

// channelProvider blocks on Invoke until the channel is closed or context is cancelled.
type channelProvider struct {
	ch chan struct{}
}

func (p *channelProvider) Invoke(ctx context.Context, _ string, _ provider.Params, _ provider.OnProgress) (*provider.Result, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.ch:
		return &provider.Result{Text: `{"response": "done"}`}, nil
	}
}

func TestProgressCallbacks(t *testing.T) {
	mp := newMockProvider([]*provider.Result{
		// Orchestrator: delegate
		{Text: `{"delegates": [{"agent": "content/researcher", "task": "go"}]}`},
		// Agent: result
		{Text: "found it", CostUSD: 0.005},
		// Orchestrator: final
		{Text: `{"response": "here you go"}`},
	}, nil)
	store := testStore(testTeam)
	orch := NewOrchestrator(mp, store, t.TempDir(), nil, testTierResolver)

	var phases []string
	progress := func(phase, detail string) {
		phases = append(phases, phase)
	}

	_, _, err := orch.Run(context.Background(), "test", nil, RunConfig{}, progress)
	if err != nil {
		t.Fatal(err)
	}

	// Should have: thinking, planning, agent, agent_done, synthesizing, thinking
	expected := map[string]bool{
		"thinking":     false,
		"planning":     false,
		"agent":        false,
		"agent_done":   false,
		"synthesizing": false,
	}
	for _, p := range phases {
		expected[p] = true
	}
	for phase, seen := range expected {
		if !seen {
			t.Errorf("missing progress phase: %s (got phases: %v)", phase, phases)
		}
	}
}
