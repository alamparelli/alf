package agents

import (
	"strings"
	"testing"
)

func TestPrepareOrchestration_MinimalInputs(t *testing.T) {
	res := PrepareOrchestration(OrchestrationInputs{
		UserMessage: "build me a weather app",
		Source:      "telegram",
		Model:       "claude-opus-4-6",
	})

	if res.Config.Model != "claude-opus-4-6" {
		t.Errorf("Model = %q, want claude-opus-4-6", res.Config.Model)
	}
	if res.Config.Source != "telegram" {
		t.Errorf("Source = %q, want telegram", res.Config.Source)
	}
	if res.Config.OriginalRequest != "build me a weather app" {
		t.Errorf("OriginalRequest = %q, want original message", res.Config.OriginalRequest)
	}
	// No system prompts expected with minimal inputs (no recall, no skills, no datadir, no conv).
	if len(res.SystemPrompts) != 0 {
		t.Errorf("SystemPrompts = %d, want 0 with minimal inputs", len(res.SystemPrompts))
	}
}

func TestPrepareOrchestration_RecallBlock(t *testing.T) {
	res := PrepareOrchestration(OrchestrationInputs{
		UserMessage: "hello",
		RecallBlock: "Memory: user prefers dark mode",
	})

	if len(res.SystemPrompts) == 0 {
		t.Fatal("expected at least 1 system prompt for recall block")
	}
	if res.SystemPrompts[0] != "Memory: user prefers dark mode" {
		t.Errorf("first system prompt = %q, want recall block", res.SystemPrompts[0])
	}
}

func TestPrepareOrchestration_ConversationContext(t *testing.T) {
	res := PrepareOrchestration(OrchestrationInputs{
		UserMessage:         "do it",
		ConversationContext: "[user]: make a weather app\n[assistant]: sure, what API?\n",
	})

	found := false
	for _, sp := range res.SystemPrompts {
		if strings.Contains(sp, "Recent conversation") && strings.Contains(sp, "weather app") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected conversation context in system prompts")
	}

	if res.Config.ConversationContext == "" {
		t.Error("Config.ConversationContext should be propagated")
	}
}

func TestPrepareOrchestration_TierFieldsPropagated(t *testing.T) {
	res := PrepareOrchestration(OrchestrationInputs{
		UserMessage:          "test",
		Model:                "grok-4",
		Backend:              "openrouter",
		Effort:               "high",
		MaxTurns:             10,
		OrchestratorMaxTurns: 5,
		MaxIterations:        15,
		TimeoutMin:           30,
		NeedValidation:       true,
		Team:                 "research",
		Source:               "schedule",
	})

	rc := res.Config
	if rc.Model != "grok-4" {
		t.Errorf("Model = %q", rc.Model)
	}
	if rc.Backend != "openrouter" {
		t.Errorf("Backend = %q", rc.Backend)
	}
	if rc.Effort != "high" {
		t.Errorf("Effort = %q", rc.Effort)
	}
	if rc.MaxTurns != 10 {
		t.Errorf("MaxTurns = %d", rc.MaxTurns)
	}
	if rc.OrchestratorMaxTurns != 5 {
		t.Errorf("OrchestratorMaxTurns = %d", rc.OrchestratorMaxTurns)
	}
	if rc.MaxIterations != 15 {
		t.Errorf("MaxIterations = %d", rc.MaxIterations)
	}
	if rc.TimeoutMin != 30 {
		t.Errorf("TimeoutMin = %d", rc.TimeoutMin)
	}
	if !rc.NeedValidation {
		t.Error("NeedValidation should be true")
	}
	if rc.Team != "research" {
		t.Errorf("Team = %q", rc.Team)
	}
	if rc.Source != "schedule" {
		t.Errorf("Source = %q", rc.Source)
	}
}

func TestPrepareOrchestration_WorkspaceSummary(t *testing.T) {
	// Use a real temp dir so WorkspaceSummary produces output.
	dir := t.TempDir()

	res := PrepareOrchestration(OrchestrationInputs{
		UserMessage: "test",
		DataDir:     dir,
	})

	found := false
	for _, sp := range res.SystemPrompts {
		if strings.Contains(sp, "Workspace") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected workspace summary in system prompts when DataDir is set")
	}
}

func TestPrepareOrchestration_PromptOrdering(t *testing.T) {
	dir := t.TempDir()

	res := PrepareOrchestration(OrchestrationInputs{
		UserMessage:         "test",
		DataDir:             dir,
		RecallBlock:         "recall block",
		ConversationContext: "conv context",
	})

	// Expected order: recall → workspace → conversation.
	if len(res.SystemPrompts) < 3 {
		t.Fatalf("expected at least 3 system prompts, got %d", len(res.SystemPrompts))
	}
	if !strings.Contains(res.SystemPrompts[0], "recall block") {
		t.Errorf("first prompt should be recall, got %q", res.SystemPrompts[0])
	}
	if !strings.Contains(res.SystemPrompts[1], "Workspace") {
		t.Errorf("second prompt should be workspace, got %q", res.SystemPrompts[1])
	}
	if !strings.Contains(res.SystemPrompts[2], "Recent conversation") {
		t.Errorf("third prompt should be conversation, got %q", res.SystemPrompts[2])
	}
}
