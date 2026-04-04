package controlcenter

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
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
	httpServer      *http.Server
	internalHandler http.Handler // handler without stripToolsSocketHeader (for Unix tools proxy)
	addr            string
	statusProvider  *daemonStatusProvider
	tlsCertFile     string // if set, serve HTTPS (local self-signed)
	tlsKeyFile      string
	stopWatcher     func()
}

// New creates a Control Center server.
// dataDir is the path to data directory, configDir is the RW config path.
// stats, version, authToken, and reloadCh are provided by the daemon.
// magic and sessions enable magic link authentication (may be nil to disable).
func New(dataDir, configDir, skillsDir string, stats *Stats, version string, authToken string, externalURL string, cfg *Config, reloadCh chan ReloadEvent, magic *MagicStore, sessions *SessionStore, chatService *ChatService, memStore MemoryStorer, memProvider provider.Provider, orchestrator *agents.Orchestrator, agentStore agents.Store, scheduler ScheduleEngine, fwStore *firewall.Store, fwProxy *firewall.Proxy, netTracker *firewall.NetTracker, vaultMgr *vault.Manager, providerRegistry *provider.Registry, onVaultUnlock func(), onTaskEvent func(taskID, status, summary string), mp *marketplace.Manager) (*Server, *EventBroker, error) {
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

	handlers := HandlerFactory(Deps{
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
		NetTracker:     netTracker,
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

	// Detect self-signed TLS cert for local installs (no Traefik).
	var tlsCert, tlsKey string
	certPath := filepath.Join(configDir, "tls", "cert.pem")
	keyPath := filepath.Join(configDir, "tls", "key.pem")
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			tlsCert = certPath
			tlsKey = keyPath
			log.Printf("[CC] TLS enabled (self-signed): %s", certPath)
		}
	}

	stopWatcher := watchAppsDir(filepath.Join(dataDir, "apps"), eventBroker, 3*time.Second)

	return &Server{
		stopWatcher: stopWatcher,
		httpServer: &http.Server{
			Addr:              addr,
			Handler:           handlers.Main,
			ReadTimeout:       30 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
			WriteTimeout:      10 * time.Minute, // long for SSE streaming
			MaxHeaderBytes:    1 << 20,           // 1MB
		},
		internalHandler: handlers.Internal,
		addr:            addr,
		statusProvider:  statusProvider,
		tlsCertFile:     tlsCert,
		tlsKeyFile:      tlsKey,
	}, eventBroker, nil
}

// SetUpdater attaches the update checker so /api/status can report available updates.
func (s *Server) SetUpdater(u UpdateChecker) {
	if s.statusProvider != nil {
		s.statusProvider.SetUpdater(u)
	}
}

// InternalHandler returns the handler without stripToolsSocketHeader,
// for use by the Unix tools proxy socket. The tools proxy injects
// X-Tools-Socket which the auth middleware trusts as authentication.
func (s *Server) InternalHandler() http.Handler {
	return s.internalHandler
}

// Start begins listening. Blocks until the server stops.
// If TLS cert/key files are present (local self-signed), serves HTTPS.
func (s *Server) Start() error {
	if s.tlsCertFile != "" && s.tlsKeyFile != "" {
		log.Printf("[CC] listening on %s (TLS)", s.addr)
		return s.httpServer.ListenAndServeTLS(s.tlsCertFile, s.tlsKeyFile)
	}
	log.Printf("[CC] listening on %s", s.addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.stopWatcher != nil {
		s.stopWatcher()
	}
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
