package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/alamparelli/alf/internal/ai"
	provider "github.com/alamparelli/alf/internal/ai/provider"
)

// buildConverseRequest translates the pipeline's legacy provider.Params +
// prompt into a ConverseRequest with an Engine override that carries
// any locally-applied wrapping (e.g. provider.NewToolLoop around an
// APIProvider). Added in #340 R4j3 so processStandard's happy path can
// reach the provider stack through Runtime.ConverseStream without reshaping
// the rest of its state machine.
//
// The caller is responsible for supplying the effective Provider (`prov`):
// the same variable that would otherwise be passed to `prov.Invoke`. Passing
// prov through provider.NewEngine lets tool-loop-wrapped providers keep
// their wrapper active on the Runtime path.
func buildConverseRequest(prompt string, prov provider.Provider, params provider.Params) ConverseRequest {
	history := make([]ai.Message, 0, len(params.ConvMessages))
	for _, m := range params.ConvMessages {
		history = append(history, ai.Message{
			Role:    ai.Role(m.Role),
			Content: m.Content,
		})
	}

	var media []ai.MediaEntry
	if len(params.Media) > 0 {
		media = make([]ai.MediaEntry, len(params.Media))
		for i, m := range params.Media {
			media[i] = ai.MediaEntry{
				Type:        m.Type,
				FileName:    m.FileName,
				MimeType:    m.MimeType,
				TempPath:    m.TempPath,
				FramePaths:  append([]string(nil), m.FramePaths...),
				Transcript:  m.Transcript,
				TextContent: m.TextContent,
			}
		}
	}

	var tools []ai.ToolSpec
	if len(params.Tools) > 0 {
		tools = make([]ai.ToolSpec, 0, len(params.Tools))
		for _, n := range params.Tools {
			if n == "" {
				continue
			}
			tools = append(tools, ai.ToolSpec{Name: n})
		}
	}

	return ConverseRequest{
		Model:           ai.ModelID(params.Model),
		Backend:         "", // carried via the Engine override instead.
		SystemPrompts:   append([]string(nil), params.SystemPrompts...),
		Prompt:          prompt,
		History:         history,
		Tools:           tools,
		MaxTurns:        params.MaxTurns,
		Effort:          params.Effort,
		WriteCapable:    params.WriteCapable,
		DataDir:         params.DataDir,
		CacheBreakpoint: params.CacheBreakpoint,
		Media:           media,
		Env:             append([]string(nil), params.Env...),
		ResumeID:        params.ResumeID,
		Engine:          provider.NewEngine(prov),
	}
}

// invokeViaRuntime drives one provider turn through ConverseStream
// and materialises a *provider.Result-compatible struct so the rest of
// processStandard can stay on the legacy Result shape. Stream events are
// translated back into provider.StreamEvent so the same progress callback
// (Accumulator + rawOnProgress) that the legacy path uses continues to
// fire — behaviour parity is the acceptance criterion.
//
// A mid-stream ai.EventError is surfaced as the function's returned error;
// cancellation is propagated as ctx.Err() so the caller's context.Canceled
// check keeps working. Added in #340 R4j3.
func (e *ChatEngine) invokeViaRuntime(
	ctx context.Context,
	req ConverseRequest,
	progressFn provider.OnProgress,
) (*provider.Result, error) {
	if e.Runtime == nil {
		return nil, fmt.Errorf("invokeViaRuntime: Runtime not installed")
	}

	stream, err := e.Runtime.ConverseStream(ctx, req)
	if err != nil {
		return nil, err
	}

	var (
		text      strings.Builder
		usage     *ai.Usage
		streamErr error
	)
	for ev := range stream {
		switch ev.Kind {
		case EventToken:
			text.WriteString(ev.Token)
			if progressFn != nil {
				progressFn(provider.StreamEvent{Type: "text_delta", Text: ev.Token})
			}
		case EventThinking:
			if progressFn != nil {
				progressFn(provider.StreamEvent{Type: "thinking", Text: ev.Text})
			}
		case EventToolUse:
			if progressFn != nil {
				progressFn(provider.StreamEvent{Type: "tool_use", Detail: ev.ToolName})
			}
		case EventToolInput:
			if progressFn != nil {
				progressFn(provider.StreamEvent{Type: "tool_input", Detail: ev.ToolName, Text: ev.Text})
			}
		case EventToolOutput:
			if progressFn != nil {
				progressFn(provider.StreamEvent{Type: "tool_result", Detail: ev.ToolID, Text: ev.Text})
			}
		case EventError:
			streamErr = ev.Err
		case EventDone:
			usage = ev.Usage
		}
	}
	if streamErr != nil {
		return nil, streamErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	result := &provider.Result{Text: text.String()}
	if usage != nil {
		result.Model = usage.Model
		result.CostUSD = usage.CostUSD
		result.NumTurns = usage.NumTurns
		result.SessionID = usage.SessionID
		result.InputTokens = usage.InputTokens
		result.OutputTokens = usage.OutputTokens
	}
	return result, nil
}
