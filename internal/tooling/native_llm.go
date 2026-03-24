package tooling

import (
	"context"
	"encoding/json"
	"fmt"
)

// LLMNativeTool invokes a specific LLM tier with a prompt and returns the response.
// Use this for one-shot LLM calls: text processing, classification, extraction, etc.
// For multi-step autonomous tasks, use the task tool instead.
type LLMNativeTool struct {
	Service LLMService
}

func (LLMNativeTool) ToolName() string { return "llm" }

func (LLMNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "llm",
		Description: "Invoke a specific LLM tier with a prompt. Use for one-shot text processing: summarize, classify, extract, translate, etc. For multi-step autonomous tasks, use the task tool instead.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tier": map[string]any{
					"type":        "string",
					"description": "LLM tier name to invoke. Use the tier tool with action 'list' to see available tiers.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The prompt to send to the LLM.",
				},
				"system": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Optional system prompt to prepend (e.g. for persona or constraints).",
				},
			},
			"required":             []string{"tier", "prompt"},
			"additionalProperties": false,
		},
	}
}

func (t LLMNativeTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Tier   string `json:"tier"`
		Prompt string `json:"prompt"`
		System string `json:"system"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Tier == "" {
		return "", fmt.Errorf("tier is required")
	}
	if args.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}

	result, err := t.Service.Invoke(ctx, LLMInvokeOpts{
		Tier:   args.Tier,
		Prompt: args.Prompt,
		System: args.System,
	})
	if err != nil {
		return "", err
	}
	return result, nil
}
