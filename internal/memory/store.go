package memory

// Store is the target persistence contract for the v0.7.10 foundation rework
// (see technical/ARCHITECTURE-v0.7.10.md). It unifies the four packages that
// today fragment conversational state: chatdb (messages), conversation
// (summarization), memstore (embeddings), and memory/preferences (prefs).
//
// Dependency rule: memory MUST NOT import capability, ai, sandbox, or runtime.
// ConvID-in-signature rule: every function that touches conversation state
// takes ConvID as an explicit parameter. No hidden "current conv".
// See doc.go for the formal rules.

import "context"

// ConvID scopes messages, summaries, and conversation-linked embeddings to
// one conversation. Callers MUST pass it explicitly on every call that
// touches conversation state.
type ConvID string

// Scope identifies a namespaced view over the embedding index
// (e.g. per-conv, per-app, per-user).
type Scope string

// MsgID is the Store-assigned identifier of a persisted Message. Empty on
// input to AppendMessage; populated on read.
type MsgID string

// Message is one entry in a conversation timeline.
//
// Concurrency: Message values are plain data; the Store implementation is
// responsible for its own synchronisation. Callers MUST NOT mutate a Message
// returned by ListMessages.
type Message struct {
	ID        MsgID     // Store-assigned on AppendMessage; empty on input.
	Role      string    // "user" | "assistant" | "tool" | "system"
	Content   string    // plain text or serialized payload
	ToolCall  *ToolCall // set when Role == "assistant" and the turn issued a tool call
	CreatedAt int64     // unix millis (Store-assigned on AppendMessage; empty on input)
}

// ToolCall mirrors the AI's request to invoke a Capability during a turn.
// It lives in memory (not ai) because it is persisted alongside messages.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// Summary is a rolling abstract of an older conversation segment.
// A Summary with an empty Text is the legitimate zero value for a conv that
// has no messages or is too short to summarize.
type Summary struct {
	ConvID    ConvID
	Text      string
	UpToMsgID MsgID
	CreatedAt int64
}

// ListOpts filters and paginates ListMessages.
//
// Cursor semantics: Before/After are Store-assigned MsgIDs and are exclusive.
// The Store MUST return messages in chronological order (oldest first).
// Before alone => messages older than the cursor.
// After alone  => messages newer than the cursor.
// Setting both is valid and narrows to the half-open interval (After, Before).
// Limit == 0 means "no limit".
type ListOpts struct {
	Limit  int
	Before MsgID // exclusive
	After  MsgID // exclusive
}

// Document is a unit indexed into the embedding store.
type Document struct {
	ID       string
	Text     string
	Metadata map[string]string
}

// Hit is a similarity search result. Higher Score means more relevant.
type Hit struct {
	Document Document
	Score    float32
}

// Value is the stored form of a preference. Always JSON-serializable.
// A nil Value returned by GetPref means the key is unset; that is not an
// error.
type Value = any

// Store is the unified persistence contract for conversations, embeddings,
// and user preferences.
//
// Not-found semantics (contract):
//   - ListMessages on an unknown convID returns (nil, nil).
//   - Summarize on an empty/unknown convID returns a zero Summary, nil error.
//   - Search with zero hits returns (nil, nil).
//   - GetPref on an unset key returns (nil, nil).
//
// Errors are reserved for:
//   - Contract violations (empty convID, empty Document.ID, k < 0, etc.)
//   - I/O / backend failures
//   - ctx cancellation (must propagate ctx.Err())
//
// Concurrency: every implementation MUST be safe for concurrent use across
// goroutines. Writer serialization is the implementer's problem, not the
// caller's.
type Store interface {
	// Conversations — replaces ChatDB + ConvStore.

	// AppendMessage persists msg under convID. The implementation assigns
	// msg.ID and msg.CreatedAt; caller-supplied values in those fields are
	// ignored.
	AppendMessage(ctx context.Context, convID ConvID, msg Message) error

	// ListMessages returns messages in chronological order (oldest first),
	// filtered and paginated by opts. An unknown convID returns (nil, nil).
	ListMessages(ctx context.Context, convID ConvID, opts ListOpts) ([]Message, error)

	// Summarize returns the current rolling Summary for convID. Empty/unknown
	// conv returns a zero Summary, nil error.
	Summarize(ctx context.Context, convID ConvID) (Summary, error)

	// Embeddings — replaces memstore.

	// Index stores doc under scope. doc.ID MUST be non-empty. Re-indexing
	// the same (scope, doc.ID) replaces the previous entry.
	Index(ctx context.Context, scope Scope, doc Document) error

	// Search returns up to k hits for query within scope, ordered by
	// descending Score. No hits returns (nil, nil). k < 0 is an error;
	// k == 0 returns (nil, nil).
	Search(ctx context.Context, scope Scope, query string, k int) ([]Hit, error)

	// Preferences — replaces memory/preferences.

	// GetPref returns the value for key or (nil, nil) if unset.
	GetPref(ctx context.Context, key string) (Value, error)

	// SetPref upserts val at key. Passing a nil val clears the key.
	SetPref(ctx context.Context, key string, val Value) error
}
