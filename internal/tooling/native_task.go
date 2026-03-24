package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// TaskNativeTool manages agent tasks (orchestrator).
type TaskNativeTool struct {
	Service TaskService
}

func (TaskNativeTool) ToolName() string { return "task" }

func (TaskNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "task",
		Description: "Launch, list, cancel, or approve autonomous agent tasks. Tasks run in the background and can use teams for multi-agent orchestration.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"launch", "list", "cancel", "delete", "approve"},
					"description": "Action to perform.",
				},
				"prompt": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Task objective (required for launch).",
				},
				"tier": map[string]any{
					"type":        []string{"string", "null"},
					"description": "LLM tier for execution (optional).",
				},
				"team": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Team name to run with (optional, enables multi-agent).",
				},
				"skills": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Comma-separated skill names to inject (optional).",
				},
				"need_validation": map[string]any{
					"type":        []string{"boolean", "null"},
					"description": "Require user approval before execution (default false).",
				},
				"id": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Task ID (required for cancel/delete/approve).",
				},
				"approved": map[string]any{
					"type":        []string{"boolean", "null"},
					"description": "Approval decision (required for approve).",
				},
				"feedback": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Feedback for approval/rejection (optional).",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (t TaskNativeTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Action         string `json:"action"`
		Prompt         string `json:"prompt"`
		Tier           string `json:"tier"`
		Team           string `json:"team"`
		Skills         string `json:"skills"`
		NeedValidation bool   `json:"need_validation"`
		ID             string `json:"id"`
		Approved       *bool  `json:"approved"`
		Feedback       string `json:"feedback"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch args.Action {
	case "launch":
		if args.Prompt == "" {
			return "", fmt.Errorf("prompt is required for launch")
		}
		var skills []string
		if args.Skills != "" {
			for _, s := range strings.Split(args.Skills, ",") {
				skills = append(skills, strings.TrimSpace(s))
			}
		}
		id, err := t.Service.Launch(ctx, TaskLaunchOpts{
			Prompt:         args.Prompt,
			Tier:           args.Tier,
			Team:           args.Team,
			Skills:         skills,
			NeedValidation: args.NeedValidation,
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Task launched successfully. ID: %s", id), nil

	case "list":
		tasks := t.Service.List()
		if len(tasks) == 0 {
			return "No tasks found.", nil
		}
		data, _ := json.MarshalIndent(tasks, "", "  ")
		return string(data), nil

	case "cancel":
		if args.ID == "" {
			return "", fmt.Errorf("id is required for cancel")
		}
		if t.Service.Cancel(args.ID) {
			return fmt.Sprintf("Task %s cancelled.", args.ID), nil
		}
		return "", fmt.Errorf("task %s not found or not running", args.ID)

	case "delete":
		if args.ID == "" {
			return "", fmt.Errorf("id is required for delete")
		}
		if t.Service.Delete(args.ID) {
			return fmt.Sprintf("Task %s deleted.", args.ID), nil
		}
		return "", fmt.Errorf("task %s not found", args.ID)

	case "approve":
		if args.ID == "" {
			return "", fmt.Errorf("id is required for approve")
		}
		if args.Approved == nil {
			return "", fmt.Errorf("approved (true/false) is required")
		}
		if t.Service.Approve(args.ID, *args.Approved, args.Feedback) {
			decision := "approved"
			if !*args.Approved {
				decision = "rejected"
			}
			return fmt.Sprintf("Task %s %s.", args.ID, decision), nil
		}
		return "", fmt.Errorf("task %s not found or not awaiting approval", args.ID)

	default:
		return "", fmt.Errorf("unknown action: %s (valid: launch, list, cancel, delete, approve)", args.Action)
	}
}
