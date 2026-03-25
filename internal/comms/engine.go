package comms

import (
	"log"
	"sync"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/chatdb"
	"github.com/alamparelli/alf/internal/conversation"
	"github.com/alamparelli/alf/internal/eventlog"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/session"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
)

// ChatEngine owns all business logic for message processing.
// Adapters call Process() and receive events via OnEvent.
type ChatEngine struct {
	// Directories
	DataDir    string
	ConfigDir  string
	ContextDir string

	// Stores
	Sessions  *session.Store
	ConvStore *conversation.Store
	ChatDB    *chatdb.DB // may be nil — UI chat persistence (SQLite)
	EventLog  *eventlog.Logger
	TierStore TierStoreReader
	SkillStore skills.Store

	// Providers
	Registry     *provider.Registry
	Orchestrator *agents.Orchestrator
	Recaller     MemoryRecaller
	ToolRegistry *tooling.Registry
	ToolExecutor *tooling.Executor

	// Injected functions
	ClassifyFull   ClassifyFullFunc
	ResolveModel   func(short string) string // maps "haiku" → full model ID
	BackendConfigs func() map[string]BackendConfig

	// Memory extraction hooks (optional).
	OnSessionEnd func(sessionID string)
	OnMessage    func(sessionID string)

	// Signal socket path (persistent, set by daemon after StartSignal).
	SignalSockPath string

	// Adapters
	adapters map[string]ChannelAdapter
	mu       sync.RWMutex
}

// NewEngine creates a ChatEngine with the given configuration.
func NewEngine(cfg EngineConfig) *ChatEngine {
	return &ChatEngine{
		DataDir:        cfg.DataDir,
		ConfigDir:      cfg.ConfigDir,
		ContextDir:     cfg.ContextDir,
		Sessions:       cfg.Sessions,
		ConvStore:      cfg.ConvStore,
		ChatDB:         cfg.ChatDB,
		EventLog:       cfg.EventLog,
		TierStore:      cfg.TierStore,
		SkillStore:     cfg.SkillStore,
		Registry:       cfg.Registry,
		Orchestrator:   cfg.Orchestrator,
		Recaller:       cfg.Recaller,
		ToolRegistry:   cfg.ToolRegistry,
		ToolExecutor:   cfg.ToolExecutor,
		ClassifyFull:   cfg.ClassifyFull,
		ResolveModel:   cfg.ResolveModel,
		BackendConfigs: cfg.BackendConfigs,
		adapters:       make(map[string]ChannelAdapter),
	}
}

// EngineConfig holds all dependencies for NewEngine.
type EngineConfig struct {
	DataDir    string
	ConfigDir  string
	ContextDir string

	Sessions   *session.Store
	ConvStore  *conversation.Store
	ChatDB     *chatdb.DB
	EventLog   *eventlog.Logger
	TierStore  TierStoreReader
	SkillStore skills.Store

	Registry     *provider.Registry
	Orchestrator *agents.Orchestrator
	Recaller     MemoryRecaller
	ToolRegistry *tooling.Registry
	ToolExecutor *tooling.Executor

	ClassifyFull   ClassifyFullFunc
	ResolveModel   func(short string) string
	BackendConfigs func() map[string]BackendConfig
}

// RegisterAdapter adds a channel adapter to the engine.
func (e *ChatEngine) RegisterAdapter(adapter ChannelAdapter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.adapters[adapter.Channel()] = adapter
}

// Adapter returns the adapter for a channel, or nil if not registered.
func (e *ChatEngine) Adapter(channel string) ChannelAdapter {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.adapters[channel]
}

// emit sends an event to the adapter for a channel.
func (e *ChatEngine) emit(channelID ChannelID, event OutEvent) {
	adapter := e.Adapter(channelID.Prefix())
	if adapter != nil {
		adapter.OnEvent(channelID, event)
	}
}

// BroadcastChannel controls which adapters receive system alerts.
// "all" (default) sends to all, "tg" or "cc" targets a specific channel.
var BroadcastChannel string

// Broadcast sends a text message to adapters based on BroadcastChannel config.
// Used for system alerts (e.g., vault token expiry).
func (e *ChatEngine) Broadcast(text string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	log.Printf("[comms] broadcast: %d adapters, filter=%q, text_len=%d", len(e.adapters), BroadcastChannel, len(text))
	for channel, adapter := range e.adapters {
		if BroadcastChannel != "" && BroadcastChannel != "all" && channel != BroadcastChannel {
			continue
		}
		if _, err := adapter.SendText(ChannelID(channel+":system"), text); err != nil {
			log.Printf("[comms] broadcast to %s failed: %v", channel, err)
		}
	}
}

// signalEnv appends ALF_SIGNAL_SOCK to the env if a persistent signal server is running
// and the env doesn't already have it (TG path sets it per-request).
func (e *ChatEngine) signalEnv(env []string) []string {
	if e.SignalSockPath == "" {
		return env
	}
	for _, v := range env {
		if len(v) > 16 && v[:16] == "ALF_SIGNAL_SOCK=" {
			return env // already set (TG per-request socket takes priority)
		}
	}
	return append(env, "ALF_SIGNAL_SOCK="+e.SignalSockPath)
}

// NewSession archives the current session for a channel, rotates the conversation ID,
// clears skills, manages onboarding state, and fires the OnSessionEnd hook.
// This is the single authoritative path — all channels (CC, TG) must use this.
func (e *ChatEngine) NewSession(channelID ChannelID, onboard bool) (oldSessionID string) {
	key := channelID.SessionKey()
	old := e.Sessions.Archive(key)
	e.Sessions.ClearSkills(key)

	if e.ConvStore != nil {
		e.ConvStore.NewConversation(channelID.ConvChannel())
	}

	if onboard {
		memory.SetOnboarding(e.ContextDir)
	} else {
		memory.ClearOnboarding(e.ContextDir)
	}

	if old != "" {
		if e.EventLog != nil {
			e.EventLog.Log("session_archived", map[string]any{
				"channel":        channelID.Prefix(),
				"old_session_id": old,
			})
		}
		if e.OnSessionEnd != nil {
			e.OnSessionEnd(old)
		}
	}

	return old
}
