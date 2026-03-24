package tooling

import (
	"context"
	"encoding/json"
	"fmt"
)

// TierNativeTool lists available LLM tiers.
type TierNativeTool struct {
	Service TierService
}

func (TierNativeTool) ToolName() string { return "tier" }

func (TierNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "tier",
		Description: "List available LLM tiers with their models, backends, tools, and capabilities.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list"},
					"description": "Action to perform.",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (t TierNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch args.Action {
	case "list":
		tiers := t.Service.List()
		if len(tiers) == 0 {
			return "No tiers configured.", nil
		}
		data, _ := json.MarshalIndent(tiers, "", "  ")
		return string(data), nil

	default:
		return "", fmt.Errorf("unknown action: %s (valid: list)", args.Action)
	}
}
