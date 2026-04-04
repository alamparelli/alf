package provider

import "context"

// StreamEvent represents a real-time event from the Claude CLI stream.
type StreamEvent struct {
	Type   string // "thinking", "tool_use", "text_delta", "tool_input", "tool_result", "block_stop"
	Detail string // tool name for tool_use, tool_id for tool_result, block type for block_stop
	Text   string // delta text for text_delta/thinking, partial JSON for tool_input, result for tool_result
}

// OnProgress is called with stream events as Claude processes.
type OnProgress func(event StreamEvent)

// Result holds the parsed output from a Claude invocation.
type Result struct {
	SessionID    string
	Text         string
	Model        string
	CostUSD      float64
	NumTurns     int
	InputTokens  int
	OutputTokens int
}

// Params configures a Claude invocation.
type Params struct {
	Model         string
	Tools         []string
	WriteCapable  bool     // if true, use --dangerously-skip-permissions; if false, restrict to Tools whitelist
	ReadOnly      bool     // if true, use read-only sandbox (Codex: -s read-only, no shell tools)
	Effort        string
	SystemPrompts []string // appended system prompts (context files, reactions)
	MaxTurns      int
	ResumeID      string
	DataDir       string   // working directory for Claude subprocess
	Env           []string // additional env vars for subprocess (e.g. ALF_SIGNAL_SOCK)
	SessionKey    string            // API history key (e.g. "tg:12345"); CLI ignores this
	ConvMessages  []ContextMessage  // conversation history from unified store; takes priority over SessionKey history
}

// ContextToolCall represents a tool invocation within a ContextMessage.
type ContextToolCall struct {
	ID        string // tool call ID
	Name      string // tool name
	Arguments string // JSON arguments
}

// ContextMessage carries conversation history to providers.
// For API providers, ToolCalls and ToolCallID enable proper OpenAI-format
// messages instead of flattening tool calls to text (which causes weaker
// models to hallucinate tool usage instead of making real function calls).
type ContextMessage struct {
	Role       string
	Content    string
	ToolCalls  []ContextToolCall // assistant messages: tool invocations
	ToolCallID string            // tool role messages: links result to call
}

// Provider invokes Claude and returns a result.
type Provider interface {
	Invoke(ctx context.Context, prompt string, params Params, onProgress OnProgress) (*Result, error)
}

// ClassifyResult holds the output from a classification.
type ClassifyResult struct {
	Tier     string // tier name, empty for direct responses
	Response string // non-empty for direct router responses
	Reason   string
	React    string // optional emoji reaction
}

// Classifier routes messages to tiers.
type Classifier interface {
	Classify(ctx context.Context, message string) (*ClassifyResult, error)
	// InjectContext sends a post-response context summary to the classifier
	// so it can track conversation flow. Format: "[tierName (access) responded: summary]"
	InjectContext(tierName, access, summary string) error
	Restart() error
	Close() error
}
