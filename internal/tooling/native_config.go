package tooling

import (
	"context"
	"encoding/json"
	"fmt"
)

// ConfigNativeTool provides read access to system configuration.
type ConfigNativeTool struct {
	Service ConfigService
}

func (ConfigNativeTool) ToolName() string { return "config" }

func (ConfigNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "config",
		Description: "Read the current system configuration (log level, quiet hours, backends, etc.).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"get"},
					"description": "Action to perform.",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (t ConfigNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch args.Action {
	case "get":
		cfg, err := t.Service.Get()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(cfg, "", "  ")
		return string(data), nil

	default:
		return "", fmt.Errorf("unknown action: %s (valid: get)", args.Action)
	}
}
