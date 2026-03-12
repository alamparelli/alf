package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// ToolExecutor executes a tool call and returns the result.
type ToolExecutor interface {
	Execute(ctx context.Context, call ToolCallRequest) ToolCallResult
}

// ToolCallRequest represents a single tool invocation.
type ToolCallRequest struct {
	ID        string
	Name      string
	Arguments string
}

// ToolCallResult is the output of a tool execution.
type ToolCallResult struct {
	ID      string
	Output  string
	IsError bool
}

// ToolLoop wraps an APIProvider with an agentic tool-calling loop.
// It implements the Provider interface.
type ToolLoop struct {
	api      *APIProvider
	executor ToolExecutor
	tools    []map[string]any // OpenAI-format tool definitions
	maxTurns int
}

// NewToolLoop creates a ToolLoop that wraps an APIProvider.
func NewToolLoop(api *APIProvider, executor ToolExecutor, tools []map[string]any, maxTurns int) *ToolLoop {
	if maxTurns <= 0 {
		maxTurns = 10
	}
	return &ToolLoop{
		api:      api,
		executor: executor,
		tools:    tools,
		maxTurns: maxTurns,
	}
}

// Invoke implements Provider. It sends the prompt with tool definitions and
// loops when the LLM returns tool_calls until it gets a text response or
// hits maxTurns.
func (tl *ToolLoop) Invoke(ctx context.Context, prompt string, params Params, onProgress OnProgress) (*Result, error) {
	messages := tl.api.BuildMessages(prompt, params)
	model := params.Model
	if model == "" {
		model = tl.api.defaultModel
	}
	if model == "" {
		model = "anthropic/claude-haiku-4-5"
	}

	// Marshal tools to JSON for the request.
	toolsJSON, err := json.Marshal(tl.tools)
	if err != nil {
		return nil, fmt.Errorf("marshal tools: %w", err)
	}
	var toolsRaw json.RawMessage = toolsJSON

	turns := 0
	for {
		resp, err := tl.api.DoRequest(ctx, messages, model, toolsRaw, onProgress)
		if err != nil {
			return nil, err
		}

		// No tool calls — return text response.
		if len(resp.ToolCalls) == 0 || resp.FinishReason == "stop" {
			return &Result{
				Text:     resp.Text,
				Model:    model,
				NumTurns: turns,
			}, nil
		}

		turns++
		if turns >= tl.maxTurns {
			text := resp.Text
			if text == "" {
				text = "Tool calling turn limit reached."
			}
			log.Printf("toolloop: max turns (%d) reached", tl.maxTurns)
			return &Result{
				Text:     text,
				Model:    model,
				NumTurns: turns,
			}, nil
		}

		// Append assistant message with tool_calls.
		assistantMsg := apiMessage{
			Role:      "assistant",
			ToolCalls: resp.ToolCalls,
		}
		if resp.Text != "" {
			assistantMsg.Content = resp.Text
		}
		messages = append(messages, assistantMsg)

		// Execute each tool call sequentially.
		for _, tc := range resp.ToolCalls {
			if onProgress != nil {
				onProgress(StreamEvent{Type: "tool_use", Detail: tc.Function.Name})
			}

			result := tl.executor.Execute(ctx, ToolCallRequest{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})

			if onProgress != nil {
				onProgress(StreamEvent{Type: "tool_result", Detail: tc.ID, Text: result.Output})
			}

			messages = append(messages, apiMessage{
				Role:       "tool",
				Content:    result.Output,
				ToolCallID: tc.ID,
			})

			log.Printf("toolloop: tool %s → %d chars (error=%v)", tc.Function.Name, len(result.Output), result.IsError)
		}
	}
}
