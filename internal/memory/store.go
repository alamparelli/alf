package memory

// This file is a Step 0 scaffold for the v0.7.10 foundation rework
// (see technical/ARCHITECTURE-v0.7.10.md).
//
// It defines the target Store interface that unifies chatdb (messages),
// conversation (summarization), memstore (embeddings), and memory/preferences
// into a single contract. All implementations land in Step 1 (#335 → #337).
//
// Dependency rule: memory MUST NOT import capability, ai, sandbox, or runtime.
//
// Hard rule: every function that touches a conversation takes ConvID as an
// explicit parameter. No hidden "current conv" singleton.

import "context"

// ConvID scopes messages, embeddings, and related state to one conversation.
type ConvID string

// Scope identifies a namespaced view over the embedding index
// (e.g. per-conv, per-app, per-user).
type Scope string

// Message is one entry in a conversation timeline.
type Message struct {
	Role      string    // "user" | "assistant" | "tool" | "system"
	Content   string    // plain text or serialized payload
	ToolCall  *ToolCall // set when Role == "assistant" and the turn issued a tool call
	CreatedAt int64     // unix millis
}

// ToolCall mirrors the AI's request to invoke a Capability during a turn.
// It lives in memory (not ai) because it is persisted alongside messages.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// Summary is a rolling abstract of an older conversation segment.
type Summary struct {
	ConvID    ConvID
	Text      string
	UpToMsgID string
	CreatedAt int64
}

// ListOpts filters and paginates ListMessages.
type ListOpts struct {
	Limit  int
	Before string // message ID cursor (exclusive)
	After  string // message ID cursor (exclusive)
}

// Document is a unit indexed into the embedding store.
type Document struct {
	ID       string
	Text     string
	Metadata map[string]string
}

// Hit is a similarity search result.
type Hit struct {
	Document Document
	Score    float32
}

// Value is the stored form of a preference. Always JSON-serializable.
type Value = any

// Store is the unified persistence contract.
//
// Conversations (replaces ChatDB + ConvStore):
//   - AppendMessage / ListMessages / Summarize
//
// Embeddings (replaces memstore):
//   - Index / Search
//
// Preferences (replaces memory/preferences):
//   - GetPref / SetPref
type Store interface {
	AppendMessage(ctx context.Context, convID ConvID, msg Message) error
	ListMessages(ctx context.Context, convID ConvID, opts ListOpts) ([]Message, error)
	Summarize(ctx context.Context, convID ConvID) (Summary, error)

	Index(ctx context.Context, scope Scope, doc Document) error
	Search(ctx context.Context, scope Scope, query string, k int) ([]Hit, error)

	GetPref(ctx context.Context, key string) (Value, error)
	SetPref(ctx context.Context, key string, val Value) error
}
