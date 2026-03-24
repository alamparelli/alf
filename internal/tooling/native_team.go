package tooling

import (
	"context"
	"encoding/json"
	"fmt"
)

// TeamNativeTool manages agent team configurations.
type TeamNativeTool struct {
	Service TeamService
}

func (TeamNativeTool) ToolName() string { return "team" }

func (TeamNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "team",
		Description: "Manage agent teams: list, get, create/update, or delete. Teams define groups of agents with roles and tiers for multi-agent task execution.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "get", "save", "delete"},
					"description": "Action to perform.",
				},
				"name": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Team name (required for get/save/delete).",
				},
				"description": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Team description (for save).",
				},
				"agents": map[string]any{
					"type":        []string{"array", "null"},
					"description": "Agent definitions: [{name, description, tier, skills}] (required for save).",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":        map[string]any{"type": "string"},
							"description": map[string]any{"type": "string"},
							"tier":        map[string]any{"type": "string"},
							"skills":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
						"required": []string{"name", "tier"},
					},
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (t TeamNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Action      string          `json:"action"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Agents      json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch args.Action {
	case "list":
		teams := t.Service.All()
		if len(teams) == 0 {
			return "No teams configured.", nil
		}
		data, _ := json.MarshalIndent(teams, "", "  ")
		return string(data), nil

	case "get":
		if args.Name == "" {
			return "", fmt.Errorf("name is required for get")
		}
		team, ok := t.Service.Get(args.Name)
		if !ok {
			return "", fmt.Errorf("team %q not found", args.Name)
		}
		data, _ := json.MarshalIndent(team, "", "  ")
		return string(data), nil

	case "save":
		if args.Name == "" {
			return "", fmt.Errorf("name is required for save")
		}
		var agents []AgentInfo
		if len(args.Agents) > 0 {
			if err := json.Unmarshal(args.Agents, &agents); err != nil {
				return "", fmt.Errorf("invalid agents: %w", err)
			}
		}
		if len(agents) == 0 {
			return "", fmt.Errorf("at least one agent is required")
		}
		if err := t.Service.Save(TeamSaveRequest{
			Name:        args.Name,
			Description: args.Description,
			Agents:      agents,
		}); err != nil {
			return "", err
		}
		return fmt.Sprintf("Team %q saved.", args.Name), nil

	case "delete":
		if args.Name == "" {
			return "", fmt.Errorf("name is required for delete")
		}
		if err := t.Service.Delete(args.Name); err != nil {
			return "", err
		}
		return fmt.Sprintf("Team %q deleted.", args.Name), nil

	default:
		return "", fmt.Errorf("unknown action: %s (valid: list, get, save, delete)", args.Action)
	}
}
