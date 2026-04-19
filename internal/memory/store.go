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

// Channel identifies the frontend that originated a Message. Used by the
// Runtime to route replies and by Store to scope "active conv" prefs.
// Well-known values: "cc" (control-center UI), "tg" (Telegram). Empty is
// legal — Store does not gate on Channel.
type Channel = string

const (
	ChannelCC       Channel = "cc"
	ChannelTelegram Channel = "tg"
)

// Role identifies who produced a Message. Free-form so downstream code can
// introduce new roles (e.g. "tool", "system") without churning this package.
// The common values — "user", "assistant", "tool", "system" — are what the
// ai block generates; Store does not validate them.
type Role = string

// RoleSummary is the sentinel Role for a summary Message produced by
// AppendSummary. ListMessages replaces the messages whose IDs appear in a
// summary's CoveredIDs with that summary. The AI block never emits this role
// directly.
const RoleSummary Role = "summary"

// BlockType classifies one ContentBlock inside a Message. Mirrors Claude's
// streaming content model so the AI block and Store share a vocabulary.
type BlockType string

const (
	BlockText       BlockType = "text"
	BlockThinking   BlockType = "thinking"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
	BlockSummary    BlockType = "summary"
)

// ContentBlock is one structured piece of a Message. A Message carries an
// ordered slice of ContentBlocks so that tool_use/tool_result pairs, thinking
// spans, and plain text survive the round-trip through Store without being
// flattened to a single string. Flatteners (FlattenForAPI, FlattenForOpenAI,
// etc.) convert this model to provider-specific wire formats.
type ContentBlock struct {
	Type BlockType

	// Text is the content for BlockText, BlockThinking, and BlockSummary.
	Text string

	// Tool-call fields — set for BlockToolUse.
	Name   string // tool name (e.g. "read_file")
	Input  string // raw JSON arguments
	ToolID string // links BlockToolUse ↔ BlockToolResult within a Message

	// Output is the tool's result text — set for BlockToolResult.
	Output string
}

// Media is an uploaded file associated with a Message. Media is scoped to
// the owning ConvID; the Store enforces this on insert (see AppendMessage).
type Media struct {
	UploadID  string // stable external ID supplied by the caller
	FileName  string
	MimeType  string
	MediaType string // "photo" | "document" | "video" | "voice"
	FilePath  string // local path; not serialized over the wire
	URL       string // optional public/signed URL
}

// Reaction is an emoji reaction on a Message. Source is typically
// "user" or "alf" — Store does not interpret it.
type Reaction struct {
	Emoji  string
	Source string
}

// Message is one entry in a conversation timeline.
//
// The ConvID is NOT a field: every Store method that touches a message
// takes ConvID as an explicit parameter (see doc.go, rule 1). Messages
// returned by ListMessages belong to the convID passed into that call.
//
// Concurrency: Message values are plain data; the Store implementation is
// responsible for its own synchronisation. Callers MUST NOT mutate a Message
// returned by ListMessages.
type Message struct {
	ID        MsgID     // Store-assigned on AppendMessage; empty on input.
	Seq       int64     // Store-assigned 1-based order within the conv.
	Role      Role      // "user" | "assistant" | "tool" | "system" | RoleSummary
	Channel   Channel   // originating frontend ("cc", "tg"); empty is legal
	Content   string    // plain-text content — kept for cheap paths (no blocks).
	Blocks    []ContentBlock
	Media     []Media
	Reactions []Reaction

	// Tool call metadata retained from the Step 1.1 contract. Prefer Blocks
	// (BlockToolUse) for new code; this field stays for Runtime paths that
	// consume tool calls without walking the block list.
	ToolCall *ToolCall

	// Provider / billing bookkeeping — persisted for the chat-history UI
	// and for cost dashboards. Empty means "not recorded."
	Model      string
	Tier       string
	Backend    string
	CostUSD    float64
	DurationMs int64
	SessionID  string
	ReplyTo    MsgID

	// CoveredIDs lists the message IDs this Message replaces in the
	// context window. Only set when Role == RoleSummary.
	CoveredIDs []MsgID

	CreatedAt int64 // unix millis (Store-assigned on AppendMessage; empty on input)
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

// ConvInfo summarises a conversation for listings (chat sidebar, history
// picker). It is a projection — mutating it does not affect Store state.
type ConvInfo struct {
	ID          ConvID
	Title       string
	Channel     Channel
	Archived    bool
	CreatedAt   int64 // unix millis
	UpdatedAt   int64
	LastMessage int64 // timestamp of most recent message; 0 if none
	MsgCount    int
}

// ConvFilter narrows ListConvs.
type ConvFilter struct {
	Channel         Channel // empty = all channels
	IncludeArchived bool
}

// ListOpts filters and paginates ListMessages.
//
// Cursor semantics: Before/After are Store-assigned MsgIDs and are exclusive.
// The Store MUST return messages in chronological order (oldest first).
// Before alone => messages older than the cursor.
// After alone  => messages newer than the cursor.
// Setting both is valid and narrows to the half-open interval (After, Before).
// Limit == 0 means "no limit".
//
// ApplySummary controls summary replacement. When true (the default for
// context-building callers), the Store returns the most recent summary in
// place of the messages whose IDs appear in its CoveredIDs. When false, the
// raw uncondensed timeline is returned — used by the summarizer to see the
// full history it needs to summarize.
type ListOpts struct {
	Limit        int
	Before       MsgID // exclusive
	After        MsgID // exclusive
	ApplySummary bool
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
//   - GetConv on an unknown convID returns (ConvInfo{}, nil) with ID == "".
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

	// EnsureConv creates the conversation if missing. Idempotent: returns
	// nil if the conv already exists. title and channel may be empty.
	EnsureConv(ctx context.Context, convID ConvID, title string, channel Channel) error

	// GetConv returns the ConvInfo for convID. Unknown conv returns zero
	// ConvInfo (ID == ""), nil error.
	GetConv(ctx context.Context, convID ConvID) (ConvInfo, error)

	// ListConvs returns conversations matching filter, ordered by CreatedAt
	// ascending. Empty result is (nil, nil).
	ListConvs(ctx context.Context, filter ConvFilter) ([]ConvInfo, error)

	// UpdateConvTitle changes the title of convID. Unknown conv is a no-op
	// (returns nil) — idempotent, matches the legacy ChatDB behaviour.
	UpdateConvTitle(ctx context.Context, convID ConvID, title string) error

	// ArchiveConv marks convID as archived. Unknown conv is a no-op.
	ArchiveConv(ctx context.Context, convID ConvID) error

	// DeleteConv hard-deletes convID and cascades to its messages, blocks,
	// reactions, media, and summaries. Unknown conv is a no-op.
	DeleteConv(ctx context.Context, convID ConvID) error

	// LatestConvID returns the most recently active non-archived conv for a
	// given channel, by last-message timestamp. Empty string if none.
	LatestConvID(ctx context.Context, channel Channel) (ConvID, error)

	// AppendMessage persists msg under convID and returns the stored value
	// with Store-assigned ID, Seq, and CreatedAt populated. Caller-supplied
	// values in those fields on input are ignored. If the conv does not yet
	// exist the Store creates it (with empty title/channel) — mirrors the
	// chatdb behaviour.
	//
	// Returning the populated Message (rather than just acknowledging the
	// write) lets callers chain downstream work — echoing the ID to SSE,
	// attaching media refs, logging — without a racy read-back query.
	AppendMessage(ctx context.Context, convID ConvID, msg Message) (Message, error)

	// GetMessage returns a single message by ID within convID, including its
	// blocks, media, and reactions. Unknown message returns (nil, nil).
	GetMessage(ctx context.Context, convID ConvID, msgID MsgID) (*Message, error)

	// ListMessages returns messages in chronological order (oldest first),
	// filtered and paginated by opts. An unknown convID returns (nil, nil).
	// When opts.ApplySummary is true, the latest summary for convID replaces
	// the messages whose IDs appear in its CoveredIDs.
	ListMessages(ctx context.Context, convID ConvID, opts ListOpts) ([]Message, error)

	// AddReaction appends a reaction to msgID within convID. Idempotent on
	// (msgID, emoji, source). Returns false if the message does not exist.
	AddReaction(ctx context.Context, convID ConvID, msgID MsgID, r Reaction) (bool, error)

	// AppendSummary records a Summary message replacing coveredIDs in the
	// readable timeline. No-op if coveredIDs is empty or text is empty.
	// The resulting Message has Role == RoleSummary and is visible via
	// ListMessages when ApplySummary is true.
	AppendSummary(ctx context.Context, convID ConvID, text string, coveredIDs []MsgID) error

	// LatestSummaryCovered returns the IDs covered by the most recent
	// summary for convID. Empty when no summary exists. Used by the
	// summarizer to avoid re-summarizing the same window twice.
	LatestSummaryCovered(ctx context.Context, convID ConvID) ([]MsgID, error)

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
