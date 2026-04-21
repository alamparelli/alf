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

// MediaEntry is the provider-agnostic shape for an attached file (image,
// document, video, voice note) handed to a multimodal request. The Provider
// adapter maps it onto the underlying provider.MediaEntry; callers do not
// need to import the provider package. Added in #340 R4j2 so the pipeline
// can pass media through Runtime.ConverseStream.
type MediaEntry struct {
	Type        string   // "photo", "document", "video", "voice"
	FileName    string
	MimeType    string
	TempPath    string   // local filesystem path to media file
	FramePaths  []string // video: contact sheet frame paths
	Transcript  string   // voice/video: transcript
	TextContent string   // documents: extracted text
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
//
// CacheBreakpoint/Media/Env were added in #340 R4j2 to bring the remaining
// provider.Params fields consumed by comms/pipeline.go onto the ai contract,
// so Runtime.ConverseStream can replace direct provider.Invoke calls.
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

	// CacheBreakpoint is an index into SystemPrompts. Entries before the
	// index are stable (cacheable across turns); entries at or after are
	// dynamic (per-request). Providers that support prompt caching use
	// this to split the static/dynamic boundary — ignored otherwise.
	CacheBreakpoint int

	// Media carries multimodal attachments. Only API providers consume it;
	// CLI providers ignore.
	Media []MediaEntry

	// Env is an optional list of "KEY=VALUE" entries appended to the
	// subprocess environment for CLI providers (e.g. ALF_SIGNAL_SOCK,
	// ALF_CHAIN_ORIGIN). Ignored by API providers.
	Env []string

	// ResumeID lets a caller continue a provider-side session (e.g. Claude CLI
	// resume, OpenRouter thread). Empty means start fresh. Providers that do
	// not support session resumption ignore it. Added in #340 R4e so chat
	// follow-ups (chat_service.negativeFollowUp) can reach the provider via
	// Runtime.Converse without losing context from the previous turn.
	ResumeID string
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

	// InputTokens/OutputTokens are the raw token counts reported by the
	// Provider (API backends populate both; some CLI backends leave them
	// zero). Added in #340 R4j2 so comms.ChatEngine can surface the same
	// metrics it gets from provider.Result today without reaching past
	// Runtime into the provider layer.
	InputTokens  int
	OutputTokens int
}

// EventKind distinguishes streaming event payloads.
type EventKind int

const (
	EventToken    EventKind = iota // an incremental token or chunk (assistant text)
	EventToolCall                  // the model requested a tool invocation (dispatch)
	EventDone                      // terminal event for this request
	EventError                     // terminal error event

	// Observability sub-events surfaced by the Provider stack (#340 R4j1).
	// These are informational — they do NOT drive Runtime tool dispatch
	// (that remains EventToolCall) and they do NOT contribute to the
	// assistant text accumulation (that remains EventToken). Consumers
	// that want to render progress (spinners, tool chips, reasoning
	// traces) can switch on them; consumers that don't care drop them
	// silently via their default switch behaviour.
	EventThinking    // model reasoning text (chain-of-thought stream)
	EventToolUse     // tool invocation announcement (name only)
	EventToolInput   // streaming tool input chunks (partial JSON args)
	EventToolOutput  // streaming tool result chunks (partial output text)
)

// ToolCall is emitted when the model wants a Capability executed.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// Event is one unit of the streaming output.
//
// Field matrix by Kind:
//   EventToken       → Token
//   EventToolCall    → ToolCall
//   EventError       → Err
//   EventDone        → Usage (may be nil)
//   EventThinking    → Text
//   EventToolUse     → ToolName
//   EventToolInput   → ToolName, Text
//   EventToolOutput  → ToolID,  Text
type Event struct {
	Kind     EventKind
	Token    string    // set when Kind == EventToken
	ToolCall *ToolCall // set when Kind == EventToolCall
	Err      error     // set when Kind == EventError
	Usage    *Usage    // set when Kind == EventDone (may be nil if provider didn't surface usage)

	// Observability fields populated for EventThinking / EventToolUse /
	// EventToolInput / EventToolOutput (#340 R4j1). Unused for the
	// original four kinds and zero by default.
	Text     string // EventThinking (full or delta), EventToolInput (chunk), EventToolOutput (chunk)
	ToolName string // EventToolUse, EventToolInput
	ToolID   string // EventToolOutput
}

// Engine runs an AI Request and returns a streamed channel of Events.
type Engine interface {
	Run(ctx context.Context, req Request) (<-chan Event, error)
}
