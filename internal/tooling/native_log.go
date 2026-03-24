package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// LogNativeTool provides access to system logs.
type LogNativeTool struct {
	Service LogService
}

func (LogNativeTool) ToolName() string { return "log" }

func (LogNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "log",
		Description: "List available log files or tail a specific log. Useful for debugging and monitoring system activity.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"list", "tail"},
					"description": "Action to perform.",
				},
				"name": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Log file name (required for tail).",
				},
				"lines": map[string]any{
					"type":        []string{"integer", "null"},
					"description": "Number of lines to return (default 100, max 500).",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (t LogNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Action string `json:"action"`
		Name   string `json:"name"`
		Lines  int    `json:"lines"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch args.Action {
	case "list":
		available := t.Service.Available()
		if len(available) == 0 {
			return "No log files available.", nil
		}
		return "Available logs: " + strings.Join(available, ", "), nil

	case "tail":
		if args.Name == "" {
			return "", fmt.Errorf("name is required for tail")
		}
		lines := args.Lines
		if lines <= 0 {
			lines = 100
		}
		if lines > 500 {
			lines = 500
		}
		logLines, err := t.Service.Tail(args.Name, lines)
		if err != nil {
			return "", err
		}
		if len(logLines) == 0 {
			return "(empty log)", nil
		}
		return strings.Join(logLines, "\n"), nil

	default:
		return "", fmt.Errorf("unknown action: %s (valid: list, tail)", args.Action)
	}
}
