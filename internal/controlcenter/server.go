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

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/firewall"
	"github.com/alamparelli/alf/internal/provider"
)

//go:embed web/*
var webFS embed.FS

// Server is the Control Center HTTP server.
type Server struct {
	httpServer *http.Server
	addr       string
}

// New creates a Control Center server.
// dataDir is the path to data directory, configDir is the RW config path.
// stats, version, authToken, and reloadCh are provided by the daemon.
// magic and sessions enable magic link authentication (may be nil to disable).
func New(dataDir, configDir, skillsDir string, stats *Stats, version string, authToken string, externalURL string, cfg *Config, reloadCh chan ReloadEvent, magic *MagicStore, sessions *SessionStore, chatService *ChatService, memStore MemoryStorer, memProvider provider.Provider, orchestrator *agents.Orchestrator, scheduler ScheduleEngine, fwStore *firewall.Store, fwProxy *firewall.Proxy) (*Server, error) {
	configStore, tierStore, contextStore, toolStore, skillStore, pageStore := StoreFactory(dataDir, configDir)
	logReader := LogReaderFactory(dataDir)
	var chatStore *ChatStore
	if chatService != nil {
		chatStore = chatService.ChatStore
	}
	statusProvider := NewStatusProvider(stats, version, chatStore)
	notifier := NewChannelNotifier(reloadCh)

	// Load initial tiers into memory.
	if err := tierStore.Reload(); err != nil {
		log.Printf("[CC] warning: failed to load tiers: %v", err)
	}

	htmlBytes, err := webFS.ReadFile("web/index.html")
	if err != nil {
		return nil, fmt.Errorf("read dashboard HTML: %w", err)
	}

	// Sub-filesystem rooted at web/ for static asset serving.
	webSub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, fmt.Errorf("create web sub-fs: %w", err)
	}

	handler := HandlerFactory(Deps{
		ConfigStore:    configStore,
		TierStore:      tierStore,
		ContextStore:    contextStore,
		ToolStore:      toolStore,
		SkillStore:     skillStore,
		PageStore:      pageStore,
		LogReader:      logReader,
		StatusProvider: statusProvider,
		Notifier:       notifier,
		Magic:          magic,
		Sessions:       sessions,
		ChatService:    chatService,
		MemStore:       memStore,
		MemProvider:    memProvider,
		Orchestrator:   orchestrator,
		Scheduler:      scheduler,
		FirewallStore:  fwStore,
		FirewallProxy:  fwProxy,
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

	addr := "0.0.0.0:8080"
	return &Server{
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      handler,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 10 * time.Minute, // long for SSE streaming
		},
		addr: addr,
	}, nil
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
