package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/alamparelli/alf/internal/platform/trace"
)

// TaskNativeTool manages agent team tasks and LLM chains.
type TaskNativeTool struct {
	Service     TaskService
	TeamService TeamService
	LLMService  LLMService
	NotifyFunc  func(origin ChainOrigin, chainID, status, message string)
	DataDir     string // for trace file output (optional)
}

func (TaskNativeTool) ToolName() string { return "task" }

func (TaskNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "task",
		Description: "Run background work: multi-agent team tasks OR LLM chains. Use action 'chain' to run a sequence of LLM calls (e.g. generate then transform). Use action 'launch' for multi-agent team delegation. NOT for simple one-shot LLM calls — use the llm tool for that.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"chain", "launch", "list", "cancel", "delete", "approve"},
					"description": "Action to perform. Use 'chain' for sequential LLM pipeline, 'launch' for team task.",
				},
				"steps": map[string]any{
					"type":        []string{"array", "null"},
					"description": "Ordered list of LLM steps for chain action. Each step has tier, prompt, and optional system. Use {result} in prompt to inject previous step's output.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"tier":   map[string]any{"type": "string", "description": "LLM tier name to use for this step."},
							"prompt": map[string]any{"type": "string", "description": "Prompt for this step. Use {result} to reference previous step's output."},
							"system": map[string]any{"type": []string{"string", "null"}, "description": "Optional system prompt."},
						},
						"required": []string{"tier", "prompt"},
					},
				},
				"prompt": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Task objective (required for launch).",
				},
				"team": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Team name to run with (optional, forces a specific team).",
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
		Action         string      `json:"action"`
		Steps          []ChainStep `json:"steps"`
		Prompt         string      `json:"prompt"`
		Team           string      `json:"team"`
		NeedValidation bool        `json:"need_validation"`
		ID             string      `json:"id"`
		Approved       *bool       `json:"approved"`
		Feedback       string      `json:"feedback"`
	}
	if err := parseArgs(argsJSON, &args); err != nil {
		return "", err
	}

	switch args.Action {
	case "chain":
		if t.LLMService == nil {
			return "", fmt.Errorf("chain not available: LLM service not configured")
		}
		if len(args.Steps) < 2 {
			return "", fmt.Errorf("chain requires at least 2 steps")
		}
		for i, s := range args.Steps {
			if s.Tier == "" || s.Prompt == "" {
				return "", fmt.Errorf("step %d: tier and prompt are required", i+1)
			}
		}

		chainID := NewChainID()

		// Resolve origin from context.
		origin, _ := ChainOriginFromContext(ctx)

		go t.executeChain(origin, chainID, args.Steps)

		resp, _ := json.Marshal(map[string]string{
			"chain_id": chainID,
			"status":   "launched",
		})
		return string(resp), nil

	case "launch":
		if args.Prompt == "" {
			return "", fmt.Errorf("prompt is required for launch")
		}
		// Early check: fail fast if no teams are configured.
		if t.TeamService != nil {
			teams := t.TeamService.All()
			if len(teams) == 0 {
				return "", fmt.Errorf("no agent teams configured — create a team first using the team tool (action: save)")
			}
			if args.Team != "" {
				if _, ok := t.TeamService.Get(args.Team); !ok {
					return "", fmt.Errorf("team %q not found — use team tool (action: list) to see available teams", args.Team)
				}
			}
		}
		id, err := t.Service.Launch(ctx, TaskLaunchOpts{
			Prompt:         args.Prompt,
			Team:           args.Team,
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
		return "", fmt.Errorf("unknown action: %s (valid: chain, launch, list, cancel, delete, approve)", args.Action)
	}
}

// executeChain runs steps sequentially, injecting {result} between steps.
func (t TaskNativeTool) executeChain(origin ChainOrigin, chainID string, steps []ChainStep) {
	short := chainID
	if len(short) > 8 {
		short = short[:8]
	}

	// Create a dedicated trace for this async chain execution.
	tracer := trace.New(origin.Source, origin.ConvID, "chain:"+short)
	defer func() {
		if t.DataDir != "" {
			tracer.Flush(t.DataDir)
		}
	}()

	var lastResult LLMChainResult

	for i, step := range steps {
		prompt := step.Prompt
		if i > 0 {
			prompt = InjectChainResult(step.Prompt, lastResult)
		}

		log.Printf("[chain] %s step %d/%d tier=%s", short, i+1, len(steps), step.Tier)

		spanHandle := tracer.StartSpan("chain_step", map[string]string{
			"chain_id": short,
			"step":     strconv.Itoa(i + 1),
			"tier":     step.Tier,
		})

		start := time.Now()
		result, err := t.LLMService.Invoke(context.Background(), LLMInvokeOpts{
			Tier:    step.Tier,
			Prompt:  prompt,
			System:  step.System,
			ChainID: chainID,
		})

		if err != nil {
			lastResult = ErrorToChainResult(err)
			log.Printf("[chain] %s step %d failed: %v", short, i+1, err)
			spanHandle.EndWithError(err)
			break
		}
		lastResult = LLMChainResult{Status: 200, Message: result}
		spanHandle.Tag("duration_ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
		spanHandle.End()
	}

	// Final status span.
	finalStatus := "completed"
	if lastResult.Status != 200 {
		finalStatus = "failed"
	}
	tracer.AddSpan("chain_result", 0, map[string]string{
		"chain_id": short,
		"status":   finalStatus,
		"steps":    strconv.Itoa(len(steps)),
	}, nil)

	// Notify with final result.
	if t.NotifyFunc != nil {
		t.NotifyFunc(origin, chainID, finalStatus, lastResult.Message)
	}
}
