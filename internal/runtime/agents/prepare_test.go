package agents

import (
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/skills"
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

// stubSkillStore is a minimal Store impl for the wrap-pinning test below.
type stubSkillStore struct{ skills []*skills.Skill }

func (s *stubSkillStore) All() []*skills.Skill                      { return s.skills }
func (s *stubSkillStore) Get(name string) (*skills.Skill, bool) {
	for _, sk := range s.skills {
		if sk.Name == name {
			return sk, true
		}
	}
	return nil, false
}
func (s *stubSkillStore) Reload() error                                          { return nil }
func (s *stubSkillStore) AddDynamicTriggers(_ string, _ []string)                {}

// TestPrepareOrchestration_SkillBodyWrappedWithMarker pins the audit
// D6 fix: when MatchTriggers picks up a skill from the user message,
// its prompt body is wrapped in <capability_content source="skill:NAME">
// so the kernel prompt's §3.2 marker rule treats injected skill text
// as non-authoritative.
func TestPrepareOrchestration_SkillBodyWrappedWithMarker(t *testing.T) {
	store := &stubSkillStore{skills: []*skills.Skill{
		{
			Name:     "weather",
			Triggers: []string{"weather"},
			Prompt:   "RAW_SKILL_BODY",
		},
	}}

	res := PrepareOrchestration(OrchestrationInputs{
		UserMessage: "what's the weather?",
		SkillStore:  store,
	})

	// SEC-002: wrap markers carry a {NONCE} placeholder that the
	// KernelPromptInjector substitutes per-Invoke. PrepareOrchestration
	// runs ahead of the LLM call, so the placeholder is still literal
	// here — the substitution happens later in the provider layer.
	want := `<capability_content_{NONCE} source="skill:weather">RAW_SKILL_BODY</capability_content_{NONCE}>`
	if len(res.Config.SkillPrompts) != 1 {
		t.Fatalf("Config.SkillPrompts len=%d, want 1; got %v", len(res.Config.SkillPrompts), res.Config.SkillPrompts)
	}
	if res.Config.SkillPrompts[0] != want {
		t.Errorf("Config.SkillPrompts[0]:\n got: %q\nwant: %q", res.Config.SkillPrompts[0], want)
	}
	// Negative: the raw, unwrapped form must NOT appear in SkillPrompts —
	// that would mean the wrap was bypassed somewhere.
	if res.Config.SkillPrompts[0] == "RAW_SKILL_BODY" {
		t.Error("Config.SkillPrompts[0] is unwrapped — §3.2 marker bypassed")
	}
	// SkillLookup must continue to return the RAW prompt (used by the
	// per-agent injection path, which has its own wrap site if needed).
	// This pins that the wrap happens only at the global SkillPrompts
	// build site, not at SkillLookup.Get.
	if res.Config.SkillLookup != nil {
		got, ok := res.Config.SkillLookup.Get("weather")
		if !ok {
			t.Error("SkillLookup.Get(weather) returned ok=false")
		} else if got != "RAW_SKILL_BODY" {
			t.Errorf("SkillLookup.Get returned wrapped text — should be raw: %q", got)
		}
	}
}
