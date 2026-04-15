package tooling

import (
	"context"
	"encoding/json"
	"fmt"
)

// SkillNativeTool provides skill catalog access.
type SkillNativeTool struct {
	Service SkillService
}

func (SkillNativeTool) ToolName() string { return "skill" }

func (SkillNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "skill",
		Description: "List available skills or get skill details. Skills provide specialized capabilities that can be injected into tasks and conversations.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "get"},
					"description": "Action to perform.",
				},
				"name": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Skill name (required for get).",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (t SkillNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if err := parseArgs(argsJSON, &args); err != nil {
		return "", err
	}

	switch args.Action {
	case "list":
		skills := t.Service.All()
		if len(skills) == 0 {
			return "No skills available.", nil
		}
		data, _ := json.MarshalIndent(skills, "", "  ")
		return string(data), nil

	case "get":
		if args.Name == "" {
			return "", fmt.Errorf("name is required for get")
		}
		skill, ok := t.Service.Get(args.Name)
		if !ok {
			return "", fmt.Errorf("skill %q not found", args.Name)
		}
		data, _ := json.MarshalIndent(skill, "", "  ")
		return string(data), nil

	default:
		return "", fmt.Errorf("unknown action: %s (valid: list, get)", args.Action)
	}
}
