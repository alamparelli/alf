// Package runtime is the single orchestrator that composes capability + memory
// + ai + sandbox. It is the only package allowed to import all four.
//
// This package is a Step 0 scaffold for the v0.7.10 foundation rework
// (see technical/ARCHITECTURE-v0.7.10.md). Signatures only — no implementation.
//
// Dependency rule:
//   - runtime MAY import capability, memory, ai, sandbox.
//   - None of the four may import runtime.
//   - Consumers (controlcenter, telegram, scheduler, cli, ...) import ONLY runtime.
//
// What Runtime does per turn:
//  1. Resolve the Capability from the registry.
//  2. Load history from Memory (scoped by convID).
//  3. Derive the Policy from Manifest + user tier.
//  4. Sandbox.Apply to prepare a scoped ctx.
//  5. AI.Run with the prepared Request.
//  6. Loop on ToolCall events: execute each Capability through Sandbox, reinject results.
//  7. Persist the turn into Memory.
package runtime

import (
	"context"

	"github.com/alamparelli/alf/internal/ai"
	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/sandbox"
)

// Args are the arguments handed to a one-shot Invoke.
type Args map[string]any

// EventKind enumerates the unified stream surfaced to consumers.
type EventKind int

const (
	EventToken      EventKind = iota // streamed assistant token
	EventToolResult                  // a Capability finished executing (full Output)
	EventDone                        // terminal
	EventError                       // terminal error

	// Observability sub-events forwarded verbatim from the Provider stack
	// (#340 R4j1). Unlike EventToolResult (which fires on Capability
	// execution inside Runtime.Chat), these describe what the model is
	// doing while the turn is still in flight and carry no dispatch
	// responsibility — consumers render them for UX only.
	EventThinking   // model reasoning text
	EventToolUse    // tool invocation announcement (name)
	EventToolInput  // streaming tool input chunks
	EventToolOutput // streaming tool output chunks
)

// Event is the unified stream item returned to UI / scheduler / telegram.
// It merges AI token events with Capability execution results, hiding the
// internal loop.
//
// Field matrix by Kind:
//   EventToken       → Token
//   EventToolResult  → ToolResult, ToolName (Capability output)
//   EventDone        → Usage
//   EventError       → Err
//   EventThinking    → Text
//   EventToolUse     → ToolName
//   EventToolInput   → ToolName, Text
//   EventToolOutput  → ToolID,  Text
type Event struct {
	Kind       EventKind
	Token      string             // set when Kind == EventToken
	ToolResult *capability.Output // set when Kind == EventToolResult
	ToolName   string             // set when Kind == EventToolResult, EventToolUse, EventToolInput
	Err        error              // set when Kind == EventError
	Usage      *ai.Usage          // set on EventDone when the engine surfaces usage (#340 R4i)

	// Observability payload fields populated for EventThinking /
	// EventToolUse / EventToolInput / EventToolOutput (#340 R4j1). Unused
	// for the original four kinds and zero by default.
	Text   string // EventThinking, EventToolInput (chunk), EventToolOutput (chunk)
	ToolID string // EventToolOutput
}

// Output is the result of a one-shot Invoke (scheduler, button, ...).
type Output = capability.Output

// Runtime is the single orchestration surface exposed to all consumers.
type Runtime interface {
	// Chat processes one user turn inside a conversation. Streams events.
	// Request fields mirror ConverseRequest where both surfaces overlap so
	// consumers can reuse per-tier resolution logic across Chat + Converse.
	Chat(ctx context.Context, req ChatRequest) (<-chan Event, error)

	// Invoke runs a single Capability (scheduler, UI button, cron).
	Invoke(ctx context.Context, capID capability.ID, args Args) (Output, error)

	// Converse is a stateless one-shot LLM call — no Memory persistence, no
	// tool loop. The Provider adapter forwards tools CLI-native if any are
	// listed. Intended for scheduler jobs, CLI commands, and anything that
	// needs "just run the model with this context" without touching a
	// conversation. See #340 R5c.
	Converse(ctx context.Context, req ConverseRequest) (ConverseResult, error)

	// ConverseStream mirrors Converse but returns the event stream instead
	// of aggregating, so consumers that need to surface tokens as they
	// arrive (CC SSE, Telegram typing indicators, comms.ChatEngine) can
	// reach the model through Runtime without falling back to
	// provider.Invoke directly. Semantics match Converse: stateless, no
	// Memory access, no Capability execution. Added in #340 R4i.
	ConverseStream(ctx context.Context, req ConverseRequest) (<-chan Event, error)
}

// ChatRequest is the per-turn input to Runtime.Chat. It carries everything a
// consumer needs to route a user message through the AI + tool loop without
// reaching into the Provider layer directly.
//
// ConvID + UserInput are required. Model falls back to Options.Model when
// empty. Tools, when nil, default to the capability Registry projection;
// pass a non-nil (possibly empty) slice to pin a specific tool set.
//
// Backend/Effort/WriteCapable/MaxTurns/DataDir/SystemPrompts/ResumeID mirror
// the ConverseRequest fields so consumers can reuse the same tier-resolution
// logic across Chat + Converse. Added in #340 R4h so chat_service /
// comms.ChatEngine can migrate off Provider.Invoke directly.
type ChatRequest struct {
	ConvID        memory.ConvID
	UserInput     string
	Model         ai.ModelID
	Backend       string
	SystemPrompts []string
	Tools         []ai.ToolSpec // nil ⇒ use Registry.List(); non-nil overrides
	MaxTurns      int
	Effort        string
	WriteCapable  bool
	DataDir       string
	ResumeID      string
}

// ConverseRequest is the stateless LLM surface: system prompts + prompt +
// optional history + optional tool list + model override. The Runtime never
// reads or writes Memory — History is the caller's responsibility.
//
// Backend/Effort/WriteCapable/MaxTurns/DataDir are provider-shaped passthroughs
// (#340 R5d) that let consumers which previously built provider.Params
// directly (scheduler) reach the provider layer via Runtime.Converse without
// losing tier configuration.
type ConverseRequest struct {
	Model         ai.ModelID
	Backend       string
	SystemPrompts []string
	Prompt        string
	History       []ai.Message
	Tools         []ai.ToolSpec
	MaxTurns      int
	Effort        string
	WriteCapable  bool
	DataDir       string

	// Strategy, when non-nil, is the driver of the Engine for this call.
	// It lets callers plug a multi-turn orchestrator (multi-agent, retry-
	// with-reflection, chain-of-thought) into the same Runtime surface that
	// normally does a single Engine.Run. A nil Strategy means "one Run, one
	// result" — the pre-R5e behaviour.
	//
	// Strategy belongs to the per-call request rather than Runtime.Options
	// because different scheduled jobs (or different chat turns, later) may
	// need different orchestration shapes on the same underlying Engine.
	Strategy ai.Strategy

	// ResumeID continues a provider-side session (Claude CLI resume, etc.)
	// without replaying History. Empty means start fresh. Providers that do
	// not support session resumption ignore it. Added in #340 R4e so
	// chat_service.negativeFollowUp can reach the provider via Converse
	// while preserving the session the previous turn opened.
	ResumeID string
}

// ConverseResult carries the aggregated response plus whatever usage data the
// Provider surfaced (cost, turn count, session id). Usage is nil when the
// Provider did not report it.
type ConverseResult struct {
	Text  string
	Usage *ai.Usage
}

// Deps groups the collaborators. The concrete Runtime (next milestones) wires
// these together; at Step 0 the struct exists only to document the surface.
type Deps struct {
	Registry CapabilityRegistry
	Memory   memory.Store
	AI       ai.Engine
	Sandbox  sandbox.Sandbox
}

// CapabilityRegistry is the resolver that turns an ID into a Capability.
// Lives here (not in capability/) to keep capability dependency-free.
type CapabilityRegistry interface {
	Resolve(id capability.ID) (capability.Capability, bool)
	List() []capability.Manifest
}
