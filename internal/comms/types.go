package comms

import (
	"strconv"
	"strings"

	"github.com/alamparelli/alf/internal/conversation"
)

// ChannelID identifies a conversation channel. Format: "tg:12345", "cc:default".
type ChannelID string

// Prefix returns the channel type prefix ("tg" or "cc").
func (c ChannelID) Prefix() string {
	s := string(c)
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i]
	}
	return s
}

// SessionKey returns a backward-compatible int64 key for session.Store.
// TG channels parse the numeric chat ID; CC channels return -1.
func (c ChannelID) SessionKey() int64 {
	s := string(c)
	i := strings.Index(s, ":")
	if i < 0 {
		return -1
	}
	prefix := s[:i]
	value := s[i+1:]
	if prefix == "tg" {
		if id, err := strconv.ParseInt(value, 10, 64); err == nil {
			return id
		}
	}
	return -1
}

// ConvChannel returns the conversation.Channel constant for this channel ID.
func (c ChannelID) ConvChannel() string {
	switch c.Prefix() {
	case "tg":
		return conversation.ChannelTelegram
	case "cc":
		return conversation.ChannelCC
	default:
		return c.Prefix()
	}
}

// MediaEntry represents a pre-processed media attachment from an adapter.
type MediaEntry struct {
	Type        string // "photo", "document", "video", "voice"
	FileName    string
	MimeType    string
	TempPath    string   // local path to downloaded file
	FramePaths  []string // extracted video frames
	Transcript  string   // voice/video transcript
	TextContent string   // extracted text (PDF, etc.)
}

// InMessage is the unified input from an adapter to the engine.
type InMessage struct {
	ChannelID    ChannelID
	Text         string       // full prompt for provider (includes media refs, reply context)
	RawText      string       // raw user message for display/persistence; falls back to Text if empty
	RouterText   string       // shortened for router (adapter builds this)
	ReplyTo      string       // quoted text (empty if not reply)
	IsReply      bool
	Media        []MediaEntry // pre-processed by adapter
	ForcedTier   string       // from force command or session override
	Env          []string     // additional env vars for provider (e.g., ALF_SIGNAL_SOCK)
	Metadata     map[string]any
	ConvID       string // conversation ID for ChatDB persistence (CC tab ID, TG "tg-{chatID}")
	Source       string // "cc", "telegram", "mobile", "scheduler"
	ReplyToMsgID string // message ID for ChatDB reply tracking
}

// DisplayText returns RawText if set, otherwise Text.
func (m InMessage) DisplayText() string {
	if m.RawText != "" {
		return m.RawText
	}
	return m.Text
}

// OutEvent is a streaming event from the engine to an adapter.
type OutEvent struct {
	Type string            // "typing", "thinking", "tool_use", "tool_input", "tool_result",
	                       // "text_delta", "text", "reaction", "routed", "system",
	                       // "agent_start", "agent_done", "planning", "done", "error",
	                       // "task_started", "agent_thinking", "agent_tool", "synthesizing"
	Data map[string]string
}

// ProcessResult is returned after engine.Process completes.
type ProcessResult struct {
	Text           string
	Model          string
	Tier           string
	Reason         string
	CostUSD        float64
	SessionID      string
	Skills         []string
	Reaction       string // suggested emoji (extracted from [[react:EMOJI]])
	IsAgent        bool
	Blocks         []conversation.ContentBlock
	Duration       int64  // milliseconds
	UserMsgID      string // ID of persisted user message
	AssistantMsgID string // ID of persisted assistant message
	TurnLimitHit   bool   // true if the provider hit its turn limit mid-run
}

// TierParams holds resolved tier configuration for Claude invocation.
type TierParams struct {
	Model                string
	Tools                []string
	Effort               string
	Backend              string
	WriteCapable         bool
	MaxTurns             int
	OrchestratorMaxTurns int
	MaxIterations        int
	TimeoutMin           int
	SystemPrompt         string
	RouterLabel          string
	ContextWeight        string
}

// EffectiveContextWeight returns the context weight, defaulting to "full".
func (tp TierParams) EffectiveContextWeight() string {
	switch tp.ContextWeight {
	case "light", "standard", "full":
		return tp.ContextWeight
	default:
		return "full"
	}
}

// ChannelAdapter defines what each channel (TG, CC) must implement.
type ChannelAdapter interface {
	Channel() string // "tg", "cc"
	SendText(channelID ChannelID, text string) (msgID string, err error)
	SendReaction(channelID ChannelID, msgID string, emoji string) error
	OnEvent(channelID ChannelID, event OutEvent)
}

// ClassifyFullFunc is the router classification function signature.
type ClassifyFullFunc func(message string, lastTier string, msgCount int, recentContext string) RouteResult

// RouteResult is the output of message classification.
type RouteResult struct {
	Tier     string
	Response string
	Reason   string
	React    string
}

// MemoryRecaller searches long-term memory by semantic similarity.
type MemoryRecaller interface {
	Search(query string, limit int) ([]MemoryResult, error)
}

// MemoryResult is a single memory search hit.
type MemoryResult struct {
	Text     string
	Type     string
	Distance float64
}

// BackendConfig defines an API backend's pricing info.
type BackendConfig struct {
	InputPrice  float64
	OutputPrice float64
}
