package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alamparelli/alf/internal/ai"
)

// NewEngine returns an ai.Engine backed by an existing Provider. The adapter
// translates ai.Request → provider.Params + prompt, invokes the Provider,
// and surfaces streamed tokens + a terminal ai.EventDone.
//
// Scope (#340 R4a): this adapter is deliberately "blunt" — it does NOT emit
// ai.EventToolCall. Tool handling stays inside the Provider (CLI-native or
// wrapped with ToolLoop) so wrapping a tool-capable Provider here does not
// cause double execution when the adapter is driven from Runtime.Chat. A
// later chunk will split out a "raw" adapter that surfaces ToolCalls, once
// the Provider stack can opt out of its internal tool loop.
//
// Translation rules:
//   - req.Model is required; empty returns an error from Run.
//   - The last req.Message with Role == RoleUser becomes the prompt argument.
//     All other messages become params.ConvMessages, preserving order and
//     role names.
//   - req.Tools.Name values are copied into params.Tools so the Provider can
//     filter or whitelist capabilities internally.
//   - OnProgress "text_delta" events are forwarded as ai.EventToken. Other
//     StreamEvent types (thinking, tool_use, tool_input, tool_result,
//     block_stop) are dropped — they are not part of the ai.Engine contract.
//   - If the Provider returns text not already streamed via OnProgress, the
//     full Result.Text is emitted as a single trailing ai.EventToken so
//     consumers always see the complete response.
func NewEngine(p Provider) ai.Engine {
	return &engineAdapter{provider: p}
}

type engineAdapter struct {
	provider Provider
}

// Run satisfies ai.Engine.
func (e *engineAdapter) Run(ctx context.Context, req ai.Request) (<-chan ai.Event, error) {
	if e.provider == nil {
		return nil, errors.New("provider.Engine: nil Provider")
	}
	if req.Model == "" {
		return nil, errors.New("provider.Engine: Request.Model required")
	}

	prompt, history, systemFromMessages, err := splitPrompt(req.Messages)
	if err != nil {
		return nil, err
	}

	// Per-call Request.SystemPrompts come first, then any RoleSystem messages
	// (which the Runtime may still inject via Messages during history replay).
	// Order matters: identity/job-context lives in Request.SystemPrompts,
	// conversational system turns come after — this mirrors how
	// Provider.Params stacks them. See #340 R5b.
	systemPrompts := mergeSystemPrompts(req.SystemPrompts, systemFromMessages)

	params := Params{
		Model:         string(req.Model),
		Tools:         toolNames(req.Tools),
		ConvMessages:  history,
		SystemPrompts: systemPrompts,
	}

	out := make(chan ai.Event, 16)
	go e.runInvoke(ctx, prompt, params, out)
	return out, nil
}

func (e *engineAdapter) runInvoke(ctx context.Context, prompt string, params Params, out chan<- ai.Event) {
	defer close(out)

	// Track how much of the final Result.Text has already been streamed via
	// OnProgress so we can emit only the delta at the end (avoiding a
	// double-emission of any already-streamed prefix).
	var streamed strings.Builder
	onProgress := func(ev StreamEvent) {
		if ev.Type != "text_delta" || ev.Text == "" {
			return
		}
		streamed.WriteString(ev.Text)
		// Non-blocking-ish send: if ctx is done, drop.
		select {
		case <-ctx.Done():
			return
		case out <- ai.Event{Kind: ai.EventToken, Token: ev.Text}:
		}
	}

	result, err := e.provider.Invoke(ctx, prompt, params, onProgress)
	if err != nil {
		// ctx.Err() takes precedence so cancellation surfaces as the
		// original context error rather than a wrapped Invoke failure.
		if ctx.Err() != nil {
			select {
			case out <- ai.Event{Kind: ai.EventError, Err: ctx.Err()}:
			default:
			}
			return
		}
		select {
		case out <- ai.Event{Kind: ai.EventError, Err: fmt.Errorf("provider.Invoke: %w", err)}:
		default:
		}
		return
	}

	// Emit any trailing text not already sent via OnProgress (e.g. the CLI
	// provider returns full text in Result, not always via deltas).
	if result != nil {
		already := streamed.String()
		tail := result.Text
		if tail != "" && tail != already {
			if strings.HasPrefix(tail, already) {
				tail = tail[len(already):]
			}
			if tail != "" {
				select {
				case <-ctx.Done():
					return
				case out <- ai.Event{Kind: ai.EventToken, Token: tail}:
				}
			}
		}
	}

	// #340 R5b: surface usage (cost / model / turns / session) to consumers.
	// A nil Usage is valid when the Provider returned no Result (rare
	// success path); EventDone still fires so Runtime can finalise the turn.
	var usage *ai.Usage
	if result != nil {
		usage = &ai.Usage{
			CostUSD:   result.CostUSD,
			Model:     result.Model,
			NumTurns:  result.NumTurns,
			SessionID: result.SessionID,
		}
	}
	select {
	case <-ctx.Done():
		return
	case out <- ai.Event{Kind: ai.EventDone, Usage: usage}:
	}
}

// mergeSystemPrompts concatenates two slices, dropping empty entries. Keeping
// this in one place gives us a single allocation on the hot path and a single
// test target. The result may be nil when both inputs are empty.
func mergeSystemPrompts(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if s != "" {
			out = append(out, s)
		}
	}
	for _, s := range b {
		if s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// splitPrompt isolates the last RoleUser message (used as the Invoke prompt),
// groups RoleSystem messages into SystemPrompts (order-preserving), and
// returns the non-system remainder as ContextMessages.
//
// Provider.Params draws a sharp line between SystemPrompts and ConvMessages
// — dropping every non-user turn into ConvMessages (as the first cut of the
// adapter did) confuses downstream providers that cache or weight system
// prompts separately. Every ai.RoleSystem message is routed through
// Params.SystemPrompts; every other non-last-user message stays in
// ConvMessages with its original Role.
//
// An empty history is valid (some callers issue fresh conversations). An
// absence of any user message is an error — the Provider.Invoke contract
// requires a prompt.
func splitPrompt(msgs []ai.Message) (prompt string, history []ContextMessage, systemPrompts []string, err error) {
	if len(msgs) == 0 {
		return "", nil, nil, errors.New("provider.Engine: Request.Messages is empty")
	}
	lastUser := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == ai.RoleUser {
			lastUser = i
			break
		}
	}
	if lastUser < 0 {
		return "", nil, nil, errors.New("provider.Engine: Request.Messages has no user message")
	}

	history = make([]ContextMessage, 0, len(msgs)-1)
	for i, m := range msgs {
		if i == lastUser {
			continue
		}
		if m.Role == ai.RoleSystem {
			if m.Content != "" {
				systemPrompts = append(systemPrompts, m.Content)
			}
			continue
		}
		history = append(history, ContextMessage{Role: string(m.Role), Content: m.Content})
	}
	return msgs[lastUser].Content, history, systemPrompts, nil
}

func toolNames(specs []ai.ToolSpec) []string {
	if len(specs) == 0 {
		return nil
	}
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		if s.Name != "" {
			names = append(names, s.Name)
		}
	}
	return names
}

// Compile-time assertion: the adapter satisfies ai.Engine.
var _ ai.Engine = (*engineAdapter)(nil)
