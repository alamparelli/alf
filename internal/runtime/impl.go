package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alamparelli/alf/internal/ai"
	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/sandbox"
)

const defaultMaxIterations = 20

// Options carries non-collaborator configuration for the concrete Runtime.
// Dependencies live in Deps; everything that is a tunable or a default lives
// here so New stays a two-argument constructor.
type Options struct {
	Model         ai.ModelID   // required — model passed to every ai.Engine.Run call
	Tier          sandbox.Tier // tier the chat turn runs under; used for Sandbox.Derive
	MaxIterations int          // cap on engine re-runs during a tool loop; 0 ⇒ defaultMaxIterations
}

// New returns the concrete Runtime that composes capability + memory + ai +
// sandbox. This is the orchestrator R3 is wiring: resolve Capability → load
// history → derive Policy → Sandbox.Apply → AI.Run → loop on ToolCall events
// → execute via Sandbox → persist.
func New(deps Deps, opts Options) (Runtime, error) {
	if deps.Registry == nil {
		return nil, fmt.Errorf("runtime: Deps.Registry required")
	}
	if deps.Memory == nil {
		return nil, fmt.Errorf("runtime: Deps.Memory required")
	}
	if deps.AI == nil {
		return nil, fmt.Errorf("runtime: Deps.AI required")
	}
	if deps.Sandbox == nil {
		return nil, fmt.Errorf("runtime: Deps.Sandbox required")
	}
	// Options.Model is checked at Chat call time, not here: Invoke-only
	// consumers (e.g. #340 R5a scheduler direct-tier) don't need a model.
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = defaultMaxIterations
	}
	return &defaultRuntime{deps: deps, opts: opts}, nil
}

type defaultRuntime struct {
	deps Deps
	opts Options
}

// Chat persists the user turn, loads history, lists available Capabilities as
// Tools, and drives the AI + tool loop until the engine completes the turn
// without further ToolCalls. All streamed events are surfaced to the caller
// over the returned channel; the channel is closed when the turn terminates.
func (r *defaultRuntime) Chat(ctx context.Context, convID memory.ConvID, userInput string) (<-chan Event, error) {
	if convID == "" {
		return nil, fmt.Errorf("runtime.Chat: convID required")
	}
	if userInput == "" {
		return nil, fmt.Errorf("runtime.Chat: userInput required")
	}
	if r.opts.Model == "" {
		return nil, fmt.Errorf("runtime.Chat: Options.Model required")
	}

	userMsg := memory.Message{
		Role:   "user",
		Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: userInput}},
	}
	if _, err := r.deps.Memory.AppendMessage(ctx, convID, userMsg); err != nil {
		return nil, fmt.Errorf("runtime.Chat: persist user message: %w", err)
	}

	history, err := r.deps.Memory.ListMessages(ctx, convID, memory.ListOpts{ApplySummary: true})
	if err != nil {
		return nil, fmt.Errorf("runtime.Chat: load history: %w", err)
	}

	messages := toAIMessages(history)
	tools := buildToolSpecs(r.deps.Registry.List())

	out := make(chan Event, 16)
	go r.runChatLoop(ctx, convID, messages, tools, out)
	return out, nil
}

// runChatLoop drives the (engine, tool-exec) loop until the model returns a
// final turn without ToolCalls. Emits EventToken for every streamed token,
// EventToolResult after each Capability execution, and a single terminal
// EventDone / EventError.
func (r *defaultRuntime) runChatLoop(
	ctx context.Context,
	convID memory.ConvID,
	messages []ai.Message,
	tools []ai.ToolSpec,
	out chan<- Event,
) {
	defer close(out)

	// assistantBlocks accumulates every block produced during the turn so
	// the complete assistant message (text + tool_use + tool_result) lands
	// in Memory as one row at the end.
	var assistantBlocks []memory.ContentBlock

	for iter := 0; iter < r.opts.MaxIterations; iter++ {
		req := ai.Request{
			Model:    r.opts.Model,
			Messages: messages,
			Tools:    tools,
			Stream:   true,
		}

		stream, err := r.deps.AI.Run(ctx, req)
		if err != nil {
			emit(ctx, out, Event{Kind: EventError, Err: fmt.Errorf("runtime.Chat: ai.Run: %w", err)})
			return
		}

		var (
			pendingCalls []*ai.ToolCall
			turnText     []byte
			sawDone      bool
		)
		for ev := range stream {
			switch ev.Kind {
			case ai.EventToken:
				turnText = append(turnText, ev.Token...)
				if !emit(ctx, out, Event{Kind: EventToken, Token: ev.Token}) {
					return
				}
			case ai.EventToolCall:
				if ev.ToolCall != nil {
					pendingCalls = append(pendingCalls, ev.ToolCall)
				}
			case ai.EventError:
				emit(ctx, out, Event{Kind: EventError, Err: ev.Err})
				return
			case ai.EventDone:
				sawDone = true
			}
		}
		if !sawDone {
			// Engine closed the stream without EventDone — treat ctx cancel
			// silently, every other case is a protocol violation.
			if ctx.Err() != nil {
				return
			}
			emit(ctx, out, Event{Kind: EventError, Err: fmt.Errorf("runtime.Chat: ai engine closed stream without EventDone")})
			return
		}

		if len(turnText) > 0 {
			assistantBlocks = append(assistantBlocks, memory.ContentBlock{Type: memory.BlockText, Text: string(turnText)})
			// Keep the assistant text in the AI conversation for the next call.
			messages = append(messages, ai.Message{Role: ai.RoleAssistant, Content: string(turnText)})
		}

		if len(pendingCalls) == 0 {
			// Turn complete — persist the consolidated assistant message.
			if len(assistantBlocks) > 0 {
				if _, err := r.deps.Memory.AppendMessage(ctx, convID, memory.Message{
					Role:   "assistant",
					Blocks: assistantBlocks,
				}); err != nil {
					emit(ctx, out, Event{Kind: EventError, Err: fmt.Errorf("runtime.Chat: persist assistant: %w", err)})
					return
				}
			}
			emit(ctx, out, Event{Kind: EventDone})
			return
		}

		// Tool loop — execute each pending call under Sandbox, record the
		// use/result blocks, and reinject results into the next AI request.
		for _, tc := range pendingCalls {
			argsJSON, _ := json.Marshal(tc.Args)
			assistantBlocks = append(assistantBlocks, memory.ContentBlock{
				Type:   memory.BlockToolUse,
				Name:   tc.Name,
				Input:  string(argsJSON),
				ToolID: tc.ID,
			})

			result := r.executeCapability(ctx, tc)
			if !emit(ctx, out, Event{Kind: EventToolResult, ToolName: tc.Name, ToolResult: &result}) {
				return
			}

			resultText := formatToolResult(result)
			assistantBlocks = append(assistantBlocks, memory.ContentBlock{
				Type:   memory.BlockToolResult,
				Output: resultText,
				ToolID: tc.ID,
			})
			messages = append(messages, ai.Message{Role: ai.RoleTool, Content: resultText})
		}
	}

	emit(ctx, out, Event{Kind: EventError, Err: fmt.Errorf("runtime.Chat: max iterations (%d) exceeded", r.opts.MaxIterations)})
}

// Converse runs a stateless, one-shot LLM call. No Memory access, no
// Capability registry lookup, no Sandbox — the Provider is the sole
// executor. Caller supplies a model (Options.Model is the fallback),
// system prompts, the current prompt, and any conversational history.
// See #340 R5c.
func (r *defaultRuntime) Converse(ctx context.Context, req ConverseRequest) (ConverseResult, error) {
	if req.Prompt == "" {
		return ConverseResult{}, fmt.Errorf("runtime.Converse: Prompt required")
	}
	model := req.Model
	if model == "" {
		model = r.opts.Model
	}
	// Model is required unless a Strategy is handling the turn — a wrapping
	// Strategy (e.g. multi-agent orchestrator) may resolve models internally
	// via its own tier lookup. The single-ResolveModel rule still holds in
	// that case: the Strategy owns the resolution inside its scope.
	// See #340 R5e3.
	if model == "" && req.Strategy == nil {
		return ConverseResult{}, fmt.Errorf("runtime.Converse: Model required (none in Request, none in Options)")
	}

	messages := make([]ai.Message, 0, len(req.History)+1)
	messages = append(messages, req.History...)
	messages = append(messages, ai.Message{Role: ai.RoleUser, Content: req.Prompt})

	aiReq := ai.Request{
		Model:         model,
		Backend:       req.Backend,
		SystemPrompts: req.SystemPrompts,
		Messages:      messages,
		Tools:         req.Tools,
		MaxTurns:      req.MaxTurns,
		Effort:        req.Effort,
		WriteCapable:  req.WriteCapable,
		DataDir:       req.DataDir,
		Stream:        true,
	}

	// When the caller provided a Strategy, hand it the Engine and let it
	// drive the turn — could be a single Run, a retry loop, a multi-agent
	// orchestrator, anything. Otherwise fall through to the default
	// single-shot path. See #340 R5e.
	var (
		stream <-chan ai.Event
		err    error
	)
	if req.Strategy != nil {
		stream, err = req.Strategy.Run(ctx, r.deps.AI, aiReq)
	} else {
		stream, err = r.deps.AI.Run(ctx, aiReq)
	}
	if err != nil {
		return ConverseResult{}, fmt.Errorf("runtime.Converse: ai.Run: %w", err)
	}

	var (
		text    strings.Builder
		usage   *ai.Usage
		sawDone bool
	)
	for ev := range stream {
		switch ev.Kind {
		case ai.EventToken:
			text.WriteString(ev.Token)
		case ai.EventError:
			return ConverseResult{}, fmt.Errorf("runtime.Converse: %w", ev.Err)
		case ai.EventDone:
			sawDone = true
			usage = ev.Usage
		}
	}
	if !sawDone {
		if ctx.Err() != nil {
			return ConverseResult{}, ctx.Err()
		}
		return ConverseResult{}, fmt.Errorf("runtime.Converse: engine closed stream without EventDone")
	}
	return ConverseResult{Text: text.String(), Usage: usage}, nil
}

// Invoke resolves a Capability, derives its Policy under the Runtime tier,
// applies the Sandbox to ctx, and executes.
func (r *defaultRuntime) Invoke(ctx context.Context, capID capability.ID, args Args) (Output, error) {
	if capID == "" {
		return Output{}, fmt.Errorf("runtime.Invoke: capID required")
	}
	cap, ok := r.deps.Registry.Resolve(capID)
	if !ok {
		return Output{}, fmt.Errorf("runtime.Invoke: capability %q not found", capID)
	}
	view := manifestView(cap.Manifest())
	policy, err := r.deps.Sandbox.Derive(view, r.opts.Tier)
	if err != nil {
		return Output{}, fmt.Errorf("runtime.Invoke: derive policy: %w", err)
	}
	sctx, err := r.deps.Sandbox.Apply(ctx, view, policy)
	if err != nil {
		return Output{}, fmt.Errorf("runtime.Invoke: apply policy: %w", err)
	}
	out, err := cap.Execute(sctx, capability.Input(args))
	if err != nil {
		return out, fmt.Errorf("runtime.Invoke: execute %q: %w", capID, err)
	}
	return out, nil
}

// executeCapability runs one ToolCall under a freshly-derived Policy. Errors
// are folded into Output.Error so the loop keeps advancing — the AI gets a
// chance to react to a failed tool call rather than aborting the turn.
func (r *defaultRuntime) executeCapability(ctx context.Context, tc *ai.ToolCall) capability.Output {
	cap, ok := r.deps.Registry.Resolve(capability.ID(tc.Name))
	if !ok {
		return capability.Output{Error: fmt.Sprintf("capability %q not found", tc.Name)}
	}
	view := manifestView(cap.Manifest())
	policy, err := r.deps.Sandbox.Derive(view, r.opts.Tier)
	if err != nil {
		return capability.Output{Error: fmt.Sprintf("derive policy: %v", err)}
	}
	sctx, err := r.deps.Sandbox.Apply(ctx, view, policy)
	if err != nil {
		return capability.Output{Error: fmt.Sprintf("apply policy: %v", err)}
	}
	out, err := cap.Execute(sctx, capability.Input(tc.Args))
	if err != nil && out.Error == "" {
		out.Error = err.Error()
	}
	return out
}

// manifestView adapts capability.Manifest → sandbox.ManifestView so the
// sandbox package never imports capability.
func manifestView(m capability.Manifest) sandbox.ManifestView {
	return sandbox.ManifestView{
		ID: string(m.ID),
		Permissions: sandbox.PermissionsView{
			FilePaths: append([]string(nil), m.Permissions.FilePaths...),
			Networks:  append([]string(nil), m.Permissions.Networks...),
			Secrets:   append([]string(nil), m.Permissions.Secrets...),
		},
	}
}

// buildToolSpecs projects the registry's manifests into the provider-facing
// ToolSpec slice. An empty registry yields a nil slice — valid for turns
// that should not exercise any Capability.
func buildToolSpecs(manifests []capability.Manifest) []ai.ToolSpec {
	if len(manifests) == 0 {
		return nil
	}
	specs := make([]ai.ToolSpec, 0, len(manifests))
	for _, m := range manifests {
		specs = append(specs, ai.ToolSpec{
			Name:        string(m.ID),
			Description: m.Description,
		})
	}
	return specs
}

// toAIMessages flattens the persisted history into the provider-facing
// {Role, Content} shape the ai.Engine consumes. Tool-use/tool-result
// structure is preserved by FlattenForAPI only as plain text; richer
// round-tripping is the provider adapter's job.
func toAIMessages(msgs []memory.Message) []ai.Message {
	flat := memory.FlattenForAPI(msgs)
	out := make([]ai.Message, 0, len(flat))
	for _, m := range flat {
		out = append(out, ai.Message{Role: ai.Role(m.Role), Content: m.Content})
	}
	return out
}

// formatToolResult picks the text the next AI turn should see. Error takes
// precedence so the model can self-correct; otherwise we stringify Data.
func formatToolResult(o capability.Output) string {
	if o.Error != "" {
		return "ERROR: " + o.Error
	}
	switch v := o.Data.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// emit sends ev on out unless ctx is done. Returns false when the caller
// should abort (ctx cancelled); true otherwise.
func emit(ctx context.Context, out chan<- Event, ev Event) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}
