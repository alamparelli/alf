// Package ai defines the target contract for the component that turns an
// intent into tokens: provider + router + agent strategies merged into one.
//
// This package is a Step 0 scaffold for the v0.7.10 foundation rework
// (see technical/ARCHITECTURE-v0.7.10.md). Signatures only — no implementation.
// Business code from provider/, router/, and agents/ migrates here in Step 4.
//
// Dependency rule: ai MUST NOT import capability, memory, sandbox, or runtime.
// Hard rules:
//   - AI does not read Memory. The Runtime prepares Request.Messages.
//   - AI does not execute Capabilities. It emits ToolCall events; Runtime runs them.
//   - A single ResolveModel. CI test forbids hardcoded model fallbacks elsewhere.
package ai

import "context"

// ModelID is the canonical identifier returned by ResolveModel.
type ModelID string

// Role enumerates message authorship as seen by the model.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is the provider-facing message, independent of storage shape.
// It intentionally does not import memory.Message: the Runtime adapts.
type Message struct {
	Role    Role
	Content string
}

// ToolSpec is the provider-facing declaration of a callable Capability.
// The Runtime builds this from capability.Manifest; ai does not import capability.
type ToolSpec struct {
	Name        string
	Description string
	Schema      map[string]any // JSON schema for arguments
}

// Request is the single shape handed to Engine.Run.
//
// SystemPrompts carries per-call system instructions (identity, job context,
// skill prompts). They are concatenated with any RoleSystem entries in
// Messages by the Provider adapter; keeping them separate lets callers build
// a Request without fabricating synthetic messages. See #340 R5b.
//
// Backend/Effort/WriteCapable/MaxTurns/DataDir are provider-shaped passthroughs
// added in #340 R5d so consumers that previously built provider.Params
// directly (scheduler) can reach the provider layer via the ai contract
// without losing tier-level configuration. An Engine implementation that does
// not care about these fields simply ignores them; the provider adapter
// maps them into Params.
type Request struct {
	Model         ModelID
	Backend       string
	SystemPrompts []string
	Messages      []Message
	Tools         []ToolSpec
	MaxTokens     int
	MaxTurns      int
	Effort        string
	WriteCapable  bool
	DataDir       string
	Stream        bool
}

// Usage summarises what an Engine.Run actually cost. Populated by the Provider
// adapter and attached to the terminal EventDone so consumers (scheduler,
// chat_service, …) can record cost/model/turn metrics without reaching into
// the provider layer. Fields that a given provider does not supply stay zero.
type Usage struct {
	CostUSD   float64
	Model     string
	NumTurns  int
	SessionID string
}

// EventKind distinguishes streaming event payloads.
type EventKind int

const (
	EventToken    EventKind = iota // an incremental token or chunk
	EventToolCall                  // the model requested a tool invocation
	EventDone                      // terminal event for this request
	EventError                     // terminal error event
)

// ToolCall is emitted when the model wants a Capability executed.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// Event is one unit of the streaming output.
type Event struct {
	Kind     EventKind
	Token    string    // set when Kind == EventToken
	ToolCall *ToolCall // set when Kind == EventToolCall
	Err      error     // set when Kind == EventError
	Usage    *Usage    // set when Kind == EventDone (may be nil if provider didn't surface usage)
}

// Engine runs an AI Request and returns a streamed channel of Events.
type Engine interface {
	Run(ctx context.Context, req Request) (<-chan Event, error)
}
