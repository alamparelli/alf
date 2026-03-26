package controlcenter

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"path/filepath"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/chatdb"
	"github.com/alamparelli/alf/internal/firewall"
	"github.com/alamparelli/alf/internal/marketplace"
	"github.com/alamparelli/alf/internal/provider"
	scheduler_pkg "github.com/alamparelli/alf/internal/scheduler"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
	"github.com/alamparelli/alf/internal/vault"
)

// DefaultPort is the Control Center HTTP listen port.
const DefaultPort = "8080"

//go:embed web/*
var webFS embed.FS

// Server is the Control Center HTTP server.
type Server struct {
	httpServer     *http.Server
	addr           string
	statusProvider *daemonStatusProvider
}

// New creates a Control Center server.
// dataDir is the path to data directory, configDir is the RW config path.
// stats, version, authToken, and reloadCh are provided by the daemon.
// magic and sessions enable magic link authentication (may be nil to disable).
func New(dataDir, configDir, skillsDir string, stats *Stats, version string, authToken string, externalURL string, cfg *Config, reloadCh chan ReloadEvent, magic *MagicStore, sessions *SessionStore, chatService *ChatService, memStore MemoryStorer, memProvider provider.Provider, orchestrator *agents.Orchestrator, agentStore agents.Store, scheduler ScheduleEngine, fwStore *firewall.Store, fwProxy *firewall.Proxy, vaultMgr *vault.Manager, providerRegistry *provider.Registry, onVaultUnlock func(), onTaskEvent func(taskID, status, summary string), mp *marketplace.Manager) (*Server, *EventBroker, error) {
	configStore, tierStore, contextStore, toolStore, skillStore, appStore := StoreFactory(dataDir, configDir)
	logReader := LogReaderFactory(dataDir)
	var chatDB *chatdb.DB
	if chatService != nil {
		chatDB = chatService.ChatDB
	}
	statusProvider := NewStatusProvider(stats, version, chatDB)
	notifier := NewChannelNotifier(reloadCh)

	// Load initial tiers into memory.
	if err := tierStore.Reload(); err != nil {
		log.Printf("[CC] warning: failed to load tiers: %v", err)
	}

	schedEventBroker := NewScheduleEventBroker()
	eventBroker := NewEventBroker()

	htmlBytes, err := webFS.ReadFile("web/index.html")
	if err != nil {
		return nil, nil, fmt.Errorf("read dashboard HTML: %w", err)
	}

	// Sub-filesystem rooted at web/ for static asset serving.
	webSub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, nil, fmt.Errorf("create web sub-fs: %w", err)
	}

	// Schedule run log uses the same logs/scheduler directory as the scheduler engine.
	schedRunLog := scheduler_pkg.NewRunLog(filepath.Join(dataDir, "logs", "scheduler"))

	handler := HandlerFactory(Deps{
		ConfigStore:    configStore,
		TierStore:      tierStore,
		ContextStore:    contextStore,
		ToolStore:      toolStore,
		SkillStore:     skillStore,
		SkillCatalog:   chatServiceSkillCatalog(chatService),
		AppStore:       appStore,
		LogReader:      logReader,
		StatusProvider: statusProvider,
		Notifier:       notifier,
		Magic:          magic,
		Sessions:       sessions,
		ChatService:    chatService,
		MemStore:       memStore,
		MemProvider:    memProvider,
		AgentStore:     agentStore,
		Orchestrator:   orchestrator,
		Scheduler:      scheduler,
		ScheduleRunLog: schedRunLog,
		FirewallStore:  fwStore,
		FirewallProxy:  fwProxy,
		VaultManager:     vaultMgr,
		EventBroker:     eventBroker,
		ScheduleEvents:  schedEventBroker,
		ToolRegistry:     chatServiceToolRegistry(chatService),
		ProviderRegistry: providerRegistry,
		ModelCache:       newModelCacheIfRegistry(providerRegistry),
		Marketplace:     mp,
		OnVaultUnlock:   onVaultUnlock,
		OnTaskEvent:     onTaskEvent,
		AuthToken:      authToken,
		AllowedOrigin:    strings.TrimRight(externalURL, "/"),
		SecureCookies:    strings.HasPrefix(externalURL, "https://"),
		AuthBanThreshold: cfg.AuthBanThreshold,
		AuthBanDuration:  cfg.AuthBanDuration,
		DataDir:        dataDir,
		ConfigDir:      configDir,
		SkillsDir:      skillsDir,
		ExternalURL:    strings.TrimRight(externalURL, "/"),
		DashboardHTML:  string(htmlBytes),
		WebFS:          webSub,
	})

	addr := "0.0.0.0:" + DefaultPort
	return &Server{
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadTimeout:       30 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
			WriteTimeout:      10 * time.Minute, // long for SSE streaming
			MaxHeaderBytes:    1 << 20,           // 1MB
		},
		addr:           addr,
		statusProvider: statusProvider,
	}, eventBroker, nil
}

// SetUpdater attaches the update checker so /api/status can report available updates.
func (s *Server) SetUpdater(u UpdateChecker) {
	if s.statusProvider != nil {
		s.statusProvider.SetUpdater(u)
	}
}

// Start begins listening. Blocks until the server stops.
func (s *Server) Start() error {
	log.Printf("[CC] listening on %s", s.addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// newModelCacheIfRegistry creates a ModelCache if a registry is available.
func newModelCacheIfRegistry(reg *provider.Registry) *ModelCache {
	if reg == nil {
		return nil
	}
	return NewModelCache(reg, 12*time.Hour)
}

// chatServiceToolRegistry extracts the ToolRegistry from a ChatService, or nil.
func chatServiceToolRegistry(cs *ChatService) *tooling.Registry {
	if cs == nil {
		return nil
	}
	return cs.ToolRegistry
}

// chatServiceSkillCatalog extracts the skills.Store from a ChatService, or nil.
func chatServiceSkillCatalog(cs *ChatService) skills.Store {
	if cs == nil {
		return nil
	}
	return cs.SkillStore
}
