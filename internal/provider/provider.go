package provider

import "context"

// StreamEvent represents a real-time event from the Claude CLI stream.
type StreamEvent struct {
	Type   string // "thinking", "tool_use", "text"
	Detail string // tool name for tool_use, empty otherwise
}

// OnProgress is called with stream events as Claude processes.
type OnProgress func(event StreamEvent)

// Result holds the parsed output from a Claude invocation.
type Result struct {
	SessionID string
	Text      string
	Model     string
	CostUSD   float64
	NumTurns  int
}

// Params configures a Claude invocation.
type Params struct {
	Model         string
	Tools         []string
	Effort        string
	SystemPrompts []string // appended system prompts (memories, reactions)
	MaxTurns      int
	ResumeID      string
	DataDir       string // working directory for Claude subprocess
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
