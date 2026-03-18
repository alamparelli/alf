package comms

import (
	"sync"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/conversation"
	"github.com/alamparelli/alf/internal/eventlog"
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

// NewSession archives the current session for a channel and rotates the conversation ID.
func (e *ChatEngine) NewSession(channelID ChannelID, onboard bool) (oldSessionID string) {
	key := channelID.SessionKey()
	old := e.Sessions.Archive(key)
	if e.ConvStore != nil {
		e.ConvStore.NewConversation(channelID.ConvChannel())
	}
	return old
}
