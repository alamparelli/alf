package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/alamparelli/alf/internal/platform/trace"
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
	ID           string
	Output       string
	IsError      bool
	ExitCode     int    // 0 success, non-zero failure, -1 timeout / launch error
	ErrorMessage string // short error description (stderr/timeout), empty on success
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
	// Sort tools by name for deterministic ordering (improves prompt cache hits).
	slices.SortFunc(tools, func(a, b map[string]any) int {
		nameA, _ := nestedString(a, "function", "name")
		nameB, _ := nestedString(b, "function", "name")
		return strings.Compare(nameA, nameB)
	})
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
		// No hardcoded model fallback: fail fast rather than silently
		// switching backends. Caller must provide a model via params or
		// the API backend's configured default.
		return nil, fmt.Errorf("toolloop: no model configured (pass Params.Model or configure the backend default)")
	}

	// Marshal tools to JSON for the request.
	toolsJSON, err := json.Marshal(tl.tools)
	if err != nil {
		return nil, fmt.Errorf("marshal tools: %w", err)
	}
	var toolsRaw json.RawMessage = toolsJSON

	turns := 0
	var totalInput, totalOutput int
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("tool loop cancelled: %w", err)
		}
		resp, err := tl.api.DoRequest(ctx, messages, model, toolsRaw, params.Effort, onProgress)
		if err != nil {
			return nil, err
		}
		totalInput += resp.InputTokens
		totalOutput += resp.OutputTokens

		// No tool calls - return text response.
		if len(resp.ToolCalls) == 0 {
			log.Printf("toolloop: no tool calls (finish=%s, output=%d chars)", resp.FinishReason, len(resp.Text))
			return &Result{
				Text:         resp.Text,
				Model:        model,
				NumTurns:     turns,
				InputTokens:  totalInput,
				OutputTokens: totalOutput,
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
				Text:         text,
				Model:        model,
				NumTurns:     turns,
				InputTokens:  totalInput,
				OutputTokens: totalOutput,
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

		// Log which tools the model is calling.
		callNames := make([]string, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			callNames[i] = tc.Function.Name
		}
		log.Printf("toolloop: turn %d, calling tools: %v", turns, callNames)

		// Execute each tool call sequentially.
		for _, tc := range resp.ToolCalls {
			// tool_use and tool_input already emitted during SSE streaming — don't re-emit.

			toolSpan := trace.StartSpanFromContext(ctx, "tool_exec", map[string]string{
				"tool": tc.Function.Name,
				"args": sanitizeToolArgs(tc.Function.Arguments),
			})

			result := tl.executor.Execute(ctx, ToolCallRequest{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})

			if toolSpan != nil {
				toolSpan.Tag("output_len", fmt.Sprintf("%d", len(result.Output)))
				toolSpan.Tag("exit_code", fmt.Sprintf("%d", result.ExitCode))
				if result.IsError {
					toolSpan.Tag("is_error", "true")
					if msg := sanitizeToolError(result.ErrorMessage); msg != "" {
						toolSpan.Tag("error", msg)
					}
				}
				toolSpan.End()
			}

			if onProgress != nil {
				onProgress(StreamEvent{Type: "tool_result", Detail: tc.ID, Text: result.Output})
			}

			messages = append(messages, apiMessage{
				Role:       "tool",
				Content:    wrapToolOutputForLLM(tc.Function.Name, result.Output),
				ToolCallID: tc.ID,
			})

			log.Printf("toolloop: tool %s → %d chars (error=%v)", tc.Function.Name, len(result.Output), result.IsError)
		}

		// Tag last tool result for cache (Anthropic models) so the next
		// DoRequest iteration gets a cache hit on system + conversation + tools.
		if strings.HasPrefix(model, "anthropic/") {
			tagLastMessageCache(messages)
		}
	}
}

// wrapToolOutputForLLM mirrors internal/runtime/llm.WrapToolOutput so the
// kernel prompt's <tool_output> marker discipline (§3.2 of
// docs/ARCHITECTURE-SECURITY.md) covers tool results that flow through
// the API tool loop. Inlined here because the foundation dependency
// rule forbids internal/ai/provider from importing internal/runtime/*.
//
// The literal "tool_output" tag MUST match
// internal/runtime/llm.TagToolOutput. If you rename the tag in the
// kernel prompt, update both sites.
func wrapToolOutputForLLM(toolName, content string) string {
	if toolName == "" {
		return "<tool_output>" + content + "</tool_output>"
	}
	return "<tool_output source=\"" + escapeMarkerAttr(toolName) + "\">" + content + "</tool_output>"
}

// escapeMarkerAttr does the same minimal HTML-attribute escape as
// internal/runtime/llm.escapeAttr so source identifiers cannot break
// out of the tag.
func escapeMarkerAttr(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			out = append(out, '&', 'q', 'u', 'o', 't', ';')
		case '<':
			out = append(out, '&', 'l', 't', ';')
		case '>':
			out = append(out, '&', 'g', 't', ';')
		case '&':
			out = append(out, '&', 'a', 'm', 'p', ';')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

// nestedString extracts a string from nested maps: m[keys[0]][keys[1]]...
func nestedString(m map[string]any, keys ...string) (string, bool) {
	cur := any(m)
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur = mm[k]
	}
	s, ok := cur.(string)
	return s, ok
}
