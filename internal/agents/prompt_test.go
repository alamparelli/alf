package agents

import (
	"strings"
	"testing"
)

func TestBuildPromptContainsTeams(t *testing.T) {
	teams := []*TeamConfig{
		{Name: "alpha", Description: "Alpha team"},
		{Name: "beta", Description: "Beta team"},
	}
	prompt := BuildOrchestratorPrompt(teams, "")
	if !strings.Contains(prompt, "alpha") || !strings.Contains(prompt, "Alpha team") {
		t.Error("prompt missing alpha team")
	}
	if !strings.Contains(prompt, "beta") || !strings.Contains(prompt, "Beta team") {
		t.Error("prompt missing beta team")
	}
}

func TestBuildPromptContainsAgents(t *testing.T) {
	teams := []*TeamConfig{
		{
			Name: "content",
			Agents: []AgentConfig{
				{Name: "researcher", Description: "Researches things", Tier: "research"},
				{Name: "writer", Description: "Writes content", Tier: "writing"},
			},
		},
	}
	prompt := BuildOrchestratorPrompt(teams, "")
	if !strings.Contains(prompt, "content/researcher") {
		t.Error("prompt missing researcher agent")
	}
	if !strings.Contains(prompt, "content/writer") {
		t.Error("prompt missing writer agent")
	}
	if !strings.Contains(prompt, "tier: research") {
		t.Error("prompt missing tier reference for researcher")
	}
	if !strings.Contains(prompt, "tier: writing") {
		t.Error("prompt missing tier reference for writer")
	}
}

func TestBuildPromptContainsProtocol(t *testing.T) {
	teams := []*TeamConfig{{Name: "t", Agents: []AgentConfig{{Name: "a", Tier: "default", SystemPrompt: "hi"}}}}
	prompt := BuildOrchestratorPrompt(teams, "")
	if !strings.Contains(prompt, "delegates") {
		t.Error("prompt missing delegation protocol")
	}
	if !strings.Contains(prompt, "response") {
		t.Error("prompt missing response protocol")
	}
}

func TestBuildPromptEmptyTeams(t *testing.T) {
	prompt := BuildOrchestratorPrompt(nil, "")
	if !strings.Contains(prompt, "No agent teams") {
		t.Error("prompt should indicate no teams available")
	}
}

func TestBuildPromptGoalDriven(t *testing.T) {
	teams := []*TeamConfig{{Name: "t", Agents: []AgentConfig{{Name: "a", Tier: "default", SystemPrompt: "hi"}}}}
	prompt := BuildOrchestratorPrompt(teams, "")
	if !strings.Contains(prompt, "NEVER DO THE WORK YOURSELF") {
		t.Error("prompt should contain strict delegation instruction")
	}
}
