package tooling

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// LLMNativeTool invokes a specific LLM tier with a prompt and returns the response.
// Use this for one-shot LLM calls: text processing, classification, extraction, etc.
// With fire_and_forget=true, runs async and chains via on_complete callbacks.
// For multi-agent team tasks, use the task tool instead.
type LLMNativeTool struct {
	Service    LLMService
	NotifyFunc func(origin ChainOrigin, chainID, status, message string) // called when the last chain step completes
}

func (LLMNativeTool) ToolName() string { return "llm" }

func (LLMNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "llm",
		Description: "Invoke a specific LLM tier with a prompt. The primary tool for all LLM calls: one-shot processing (summarize, classify, extract, translate) and async chains (fire_and_forget=true with on_complete callbacks). Always use this tool when you need to call an LLM tier.",
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
				"fire_and_forget": map[string]any{
					"type":        "boolean",
					"default":     false,
					"description": "If true, run asynchronously and return immediately. Requires on_complete.",
				},
				"on_complete": map[string]any{
					"type":        []string{"object", "null"},
					"description": "Next LLM call to execute with the result. Required when fire_and_forget=true. The prompt may contain {result} which is replaced with the previous step's output wrapped in <chain_result>. The on_complete receives a structured result with status code and message.",
					"properties": map[string]any{
						"tier":            map[string]any{"type": "string"},
						"prompt":          map[string]any{"type": "string", "description": "May contain {result} placeholder."},
						"system":          map[string]any{"type": []string{"string", "null"}},
						"fire_and_forget": map[string]any{"type": "boolean"},
						"on_complete":     map[string]any{"type": []string{"object", "null"}, "description": "Recursive: next step in the chain."},
					},
					"required": []string{"tier", "prompt"},
				},
				"max_depth": map[string]any{
					"type":        "integer",
					"description": "Maximum chain depth (required when fire_and_forget=true). Decremented at each step.",
				},
			},
			"required":             []string{"tier", "prompt"},
			"additionalProperties": false,
		},
	}
}

func (t LLMNativeTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Tier          string         `json:"tier"`
		Prompt        string         `json:"prompt"`
		System        string         `json:"system"`
		FireAndForget bool           `json:"fire_and_forget"`
		OnComplete    *LLMOnComplete `json:"on_complete"`
		MaxDepth      int            `json:"max_depth"`
		ChainID       string         `json:"chain_id"`    // internal, propagated between steps
		Origin        ChainOrigin    `json:"origin"`      // internal, propagated for callback routing
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

	// Sync mode (default): unchanged behavior.
	if !args.FireAndForget {
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

	// Resolve origin: explicit args > context > zero value.
	origin := args.Origin
	if origin.Source == "" {
		if ctxOrigin, ok := ChainOriginFromContext(ctx); ok {
			origin = ctxOrigin
		}
	}

	// Fire-and-forget validation.
	if args.OnComplete == nil {
		return "", fmt.Errorf("on_complete is required when fire_and_forget=true")
	}
	if args.MaxDepth <= 0 {
		return "", fmt.Errorf("max_depth must be > 0 when fire_and_forget=true")
	}

	chainID := args.ChainID
	if chainID == "" {
		chainID = NewChainID()
	}

	// Launch chain in background goroutine.
	go t.executeChain(origin, chainID, args.Tier, args.Prompt, args.System, args.OnComplete, args.MaxDepth)

	resp, _ := json.Marshal(map[string]string{
		"chain_id": chainID,
		"status":   "launched",
	})
	return string(resp), nil
}

// executeChain runs the current step and chains to on_complete.
func (t LLMNativeTool) executeChain(origin ChainOrigin, chainID, tier, prompt, system string, onComplete *LLMOnComplete, depth int) {
	// Execute current step.
	result, err := t.Service.Invoke(context.Background(), LLMInvokeOpts{
		Tier:    tier,
		Prompt:  prompt,
		System:  system,
		ChainID: chainID,
	})

	var chainResult LLMChainResult
	if err != nil {
		chainResult = ErrorToChainResult(err)
	} else {
		chainResult = LLMChainResult{Status: 200, Message: result}
	}

	// No callback = last step → notify.
	if onComplete == nil {
		t.notify(origin, chainID, chainResult)
		return
	}

	// Depth exhausted → notify with the current result instead of continuing.
	if depth <= 1 {
		log.Printf("[llm-chain] chain %s reached max depth, stopping", chainID[:8])
		t.notify(origin, chainID, chainResult)
		return
	}

	// Inject result into callback prompt.
	callbackPrompt := InjectChainResult(onComplete.Prompt, chainResult)

	// If callback is fire-and-forget, continue the chain.
	if onComplete.FireAndForget && onComplete.OnComplete != nil {
		t.executeChain(origin, chainID, onComplete.Tier, callbackPrompt, onComplete.System, onComplete.OnComplete, depth-1)
		return
	}

	// Last maillon: execute sync and notify.
	finalResult, err := t.Service.Invoke(context.Background(), LLMInvokeOpts{
		Tier:    onComplete.Tier,
		Prompt:  callbackPrompt,
		System:  onComplete.System,
		ChainID: chainID,
	})

	var finalChain LLMChainResult
	if err != nil {
		finalChain = ErrorToChainResult(err)
	} else {
		finalChain = LLMChainResult{Status: 200, Message: finalResult}
	}
	t.notify(origin, chainID, finalChain)
}

// notify calls NotifyFunc if set.
func (t LLMNativeTool) notify(origin ChainOrigin, chainID string, result LLMChainResult) {
	if t.NotifyFunc == nil {
		return
	}
	status := "completed"
	if result.Status != 200 {
		status = "failed"
	}
	t.NotifyFunc(origin, chainID, status, result.Message)
}

// InjectChainResult replaces {result} in prompt with the structured chain result.
func InjectChainResult(prompt string, result LLMChainResult) string {
	replacement := fmt.Sprintf("<chain_result status=\"%d\">\n%s\n</chain_result>", result.Status, result.Message)
	return strings.ReplaceAll(prompt, "{result}", replacement)
}

// NewChainID generates a random 16-char hex ID for chain tracking.
func NewChainID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// ErrorToChainResult converts an error to a LLMChainResult with appropriate status code.
func ErrorToChainResult(err error) LLMChainResult {
	msg := err.Error()
	status := 500
	if strings.Contains(msg, "not found") {
		status = 404
	} else if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") {
		status = 408
	} else if strings.Contains(msg, "invalid") {
		status = 400
	}
	return LLMChainResult{Status: status, Message: msg}
}
