package agents

import (
	"context"
	"testing"

	"github.com/alamparelli/alf/internal/ai"
)

// TestStrategy_NilOrchestratorErrors guards against a miswired strategy.
func TestStrategy_NilOrchestratorErrors(t *testing.T) {
	s := &orchestratorStrategy{orch: nil}
	_, err := s.Run(context.Background(), nil, ai.Request{
		Messages: []ai.Message{{Role: ai.RoleUser, Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for nil Orchestrator")
	}
}

// TestStrategy_NoUserMessageErrors enforces the contract: the Strategy is a
// one-turn driver and needs a user message in Request.Messages. The
// orchestrator never gets invoked.
func TestStrategy_NoUserMessageErrors(t *testing.T) {
	// Non-nil orch only to pass the nil check; no user msg means we fail
	// before orch.Run ever runs.
	orch := &Orchestrator{}
	s := &orchestratorStrategy{orch: orch}
	_, err := s.Run(context.Background(), nil, ai.Request{
		Messages: []ai.Message{{Role: ai.RoleAssistant, Content: "earlier"}},
	})
	if err == nil {
		t.Fatal("expected error when Request has no user message")
	}
}

// TestStrategy_BuildRunConfigTranslation pins the pure ai.Request +
// StrategyOptions → RunConfig mapping. This is the translation the runtime
// boundary relies on — any silent drop would be a regression.
func TestStrategy_BuildRunConfigTranslation(t *testing.T) {
	req := ai.Request{
		Model:    "anthropic/claude-opus-4-7",
		Backend:  "openrouter",
		Effort:   "high",
		MaxTurns: 12,
	}
	lookup := stubSkillLookup{m: map[string]string{"s1": "prompt-for-s1"}}
	opts := StrategyOptions{
		SkillLookup:   lookup,
		MemoryContext: []string{"mem-a", "mem-b"},
		Source:        "schedule",
	}

	rc := buildRunConfig(req, opts)

	if rc.Model != "anthropic/claude-opus-4-7" {
		t.Fatalf("Model: got %q", rc.Model)
	}
	if rc.Backend != "openrouter" {
		t.Fatalf("Backend: got %q", rc.Backend)
	}
	if rc.Effort != "high" {
		t.Fatalf("Effort: got %q", rc.Effort)
	}
	if rc.MaxTurns != 12 {
		t.Fatalf("MaxTurns: got %d", rc.MaxTurns)
	}
	if rc.Source != "schedule" {
		t.Fatalf("Source: got %q", rc.Source)
	}
	if len(rc.MemoryContext) != 2 || rc.MemoryContext[0] != "mem-a" {
		t.Fatalf("MemoryContext: %v", rc.MemoryContext)
	}
	if rc.SkillLookup == nil {
		t.Fatal("SkillLookup: nil")
	}
	got, ok := rc.SkillLookup.Get("s1")
	if !ok || got != "prompt-for-s1" {
		t.Fatalf("SkillLookup passthrough: got %q, ok=%v", got, ok)
	}
}

// TestStrategy_LastUserMessage walks the message list from the tail so
// multi-turn conversations pick the latest user turn as the prompt.
func TestStrategy_LastUserMessage(t *testing.T) {
	got := lastUserMessage([]ai.Message{
		{Role: ai.RoleUser, Content: "first"},
		{Role: ai.RoleAssistant, Content: "in between"},
		{Role: ai.RoleUser, Content: "latest"},
	})
	if got != "latest" {
		t.Fatalf("got %q want latest", got)
	}
	if lastUserMessage(nil) != "" {
		t.Fatal("empty messages should return empty string")
	}
	only := []ai.Message{{Role: ai.RoleAssistant, Content: "x"}}
	if lastUserMessage(only) != "" {
		t.Fatal("no user message should return empty string")
	}
}

type stubSkillLookup struct{ m map[string]string }

func (s stubSkillLookup) Get(name string) (string, bool) {
	v, ok := s.m[name]
	return v, ok
}
