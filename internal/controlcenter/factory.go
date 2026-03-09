package controlcenter

import (
	"io/fs"
	"net/http"
	"path/filepath"
	"time"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/firewall"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/scheduler"
	"github.com/alamparelli/alf/internal/vault"
)

// Deps holds all dependencies needed to build the control center.
type Deps struct {
	ConfigStore    ConfigStore
	TierStore      TierStore
	ContextStore    ResourceStore
	ToolStore      ResourceStore
	SkillStore     ResourceStore
	AppStore       AppStore
	LogReader      LogReader
	StatusProvider StatusProvider
	Notifier       Notifier
	Magic          *MagicStore
	Sessions       *SessionStore
	ChatService    *ChatService // nil if chat API disabled
	AgentStore     agents.Store        // nil if agents disabled
	Orchestrator   *agents.Orchestrator // nil if orchestrator not available
	MemStore       MemoryStorer       // nil if memory unavailable
	MemProvider    provider.Provider  // nil if memory unavailable
	Scheduler      ScheduleEngine     // nil if scheduler unavailable
	ScheduleRunLog *scheduler.RunLog  // nil if scheduler unavailable
	FirewallStore  *firewall.Store     // nil if firewall unavailable
	FirewallProxy  *firewall.Proxy     // nil if firewall unavailable
	VaultManager   *vault.Manager      // nil if vault unavailable
	AuthToken        string
	AllowedOrigin    string // CORS origin allowlist (from externalURL)
	SecureCookies    bool   // true when CC is behind HTTPS
	AuthBanThreshold int    // failed /auth attempts before IP ban (0 = default 10)
	AuthBanDuration  int    // IP ban duration in minutes (0 = default 15)
	DataDir          string
	ConfigDir      string
	SkillsDir      string
	ExternalURL    string // public URL (e.g. https://cc.lamparelli.eu)
	DashboardHTML  string
	WebFS          fs.FS // embedded web assets (style.css, app.js)
}

// StoreFactory creates concrete store implementations from data and config directories.
func StoreFactory(dataDir, configDir string) (ConfigStore, TierStore, ResourceStore, ResourceStore, ResourceStore, AppStore) {
	cs := NewFileConfigStore(ConfigPath(configDir))
	ts := NewFileTierStore(TiersPath(configDir))
	ms := NewFileResourceStore(filepath.Join(dataDir, "context"), ".md")
	tools := NewFileResourceStore(filepath.Join(dataDir, "tools"), ".json")
	skills := NewFileResourceStore(filepath.Join(dataDir, "skills"), ".json")
	apps := NewFileAppStore(filepath.Join(dataDir, "apps"))
	return cs, ts, ms, tools, skills, apps
}

// LogReaderFactory creates a LogReader from a data directory.
func LogReaderFactory(dataDir string) LogReader {
	logDir := filepath.Join(dataDir, "logs")
	return NewFileLogReader(logDir, nil)
}

// HandlerFactory builds the HTTP mux with all routes and middleware.
func HandlerFactory(deps Deps) http.Handler {
	mux := http.NewServeMux()

	// API routes.
	mux.Handle("/api/config", &ConfigHandler{
		Store:    deps.ConfigStore,
		Notifier: deps.Notifier,
		Event:    ReloadConfig,
	})
	mux.Handle("/api/workspace", &WorkspaceHandler{
		DataDir:   deps.DataDir,
		ConfigDir: deps.ConfigDir,
		SkillsDir: deps.SkillsDir,
		Notifier:  deps.Notifier,
	})
	mux.Handle("/api/workspace/upload", &UploadHandler{
		DataDir:   deps.DataDir,
		ConfigDir: deps.ConfigDir,
		SkillsDir: deps.SkillsDir,
		Notifier:  deps.Notifier,
	})
	mux.Handle("/api/status", &StatusHandler{
		Provider: deps.StatusProvider,
	})
	mux.Handle("/api/logs", &LogsHandler{
		Reader: deps.LogReader,
	})

	mux.Handle("/api/tiers", &TiersHandler{
		TierStore: deps.TierStore,
		Notifier:  deps.Notifier,
	})

	// Resource CRUD routes.
	mux.Handle("/api/context/", &ResourceHandler{
		Store: deps.ContextStore,
	})
	mux.Handle("/api/tools/", &ResourceHandler{
		Store:    deps.ToolStore,
		Notifier: deps.Notifier,
		Event:    ReloadTools,
	})
	mux.Handle("/api/skills/", &ResourceHandler{
		Store:    deps.SkillStore,
		Notifier: deps.Notifier,
		Event:    ReloadSkills,
	})

	// Apps: directory-based apps with index.html + assets.
	if deps.AppStore != nil {
		mux.Handle("/api/apps/", &AppListHandler{
			Store: deps.AppStore,
		})
		mux.Handle("/apps/", &AppHandler{
			Store: deps.AppStore,
		})
	}

	// Chat API (mobile app).
	if deps.ChatService != nil {
		mux.Handle("/api/chat", &ChatHandler{Service: deps.ChatService})
		mux.Handle("/api/chat/job", &ChatJobHandler{Service: deps.ChatService})
		mux.Handle("/api/chat/upload", &ChatMediaHandler{Service: deps.ChatService})
		mux.Handle("/api/chat/media/", &ChatMediaHandler{Service: deps.ChatService})
		mux.Handle("/api/chat/react", &ChatReactHandler{Service: deps.ChatService})
	}

	// Memory ingest (Teach).
	if deps.MemStore != nil && deps.MemProvider != nil {
		mux.Handle("/api/memory/ingest", &MemoryIngestHandler{
			Store:        deps.MemStore,
			Provider:     deps.MemProvider,
			TierStore:    deps.TierStore,
			ContextStore: deps.ContextStore,
		})
		mux.Handle("/api/memory/tiers", &MemoryTiersHandler{
			TierStore: deps.TierStore,
		})
	}

	// Scheduled jobs.
	mux.Handle("/api/schedules", &SchedulesHandler{
		Engine: deps.Scheduler,
	})
	mux.Handle("/api/schedules/logs", &ScheduleLogsHandler{
		RunLog: deps.ScheduleRunLog,
	})

	// Orchestrator tasks.
	mux.Handle("/api/tasks", &TasksHandler{
		Orchestrator: deps.Orchestrator,
		DataDir:      deps.DataDir,
	})

	// Firewall.
	mux.Handle("/api/firewall", &FirewallHandler{
		Store:    deps.FirewallStore,
		Proxy:    deps.FirewallProxy,
		Notifier: deps.Notifier,
	})

	// Vault (secrets proxy).
	vaultH := &VaultHandler{Manager: deps.VaultManager}
	mux.Handle("/api/vault/", vaultH)
	mux.Handle("/api/vault", vaultH)

	// Docs (embedded markdown).
	mux.Handle("/api/docs/", &DocsHandler{})

	// Bash command execution.
	mux.Handle("/api/bash", &BashHandler{})

	// Restart.
	mux.Handle("/api/restart", &RestartHandler{})

	// Health (exempt from auth).
	mux.Handle("/health", &HealthHandler{})

	// Magic link auth (exempt from auth — does its own validation).
	// Strict rate limit (5/min per IP) + IP ban after repeated failures.
	if deps.Magic != nil && deps.Sessions != nil {
		authRL := newRateLimiter(5)
		authBan := newIPBan(deps.AuthBanThreshold, time.Duration(deps.AuthBanDuration)*time.Minute)
		authHandler := authBan.middleware(authRL.middleware(&AuthHandler{
			Magic:    deps.Magic,
			Sessions: deps.Sessions,
			Secure:   deps.SecureCookies,
		}))
		mux.Handle("/auth", authHandler)
	}

	// Magic link generator (auth-token protected, for CLI usage).
	if deps.Magic != nil && deps.ExternalURL != "" {
		mux.Handle("/api/magic-link", &MagicLinkHandler{
			Magic:       deps.Magic,
			ExternalURL: deps.ExternalURL,
		})
	}

	// Static assets (CSS, JS) — served from embedded web/ directory.
	if deps.WebFS != nil {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(deps.WebFS))))
	}

	// Dashboard.
	mux.Handle("/", &DashboardHandler{
		HTML: deps.DashboardHTML,
	})

	// Apply middleware stack (outermost first).
	exempt := map[string]bool{"/health": true, "/auth": true}
	var handler http.Handler = mux
	handler = jsonMiddleware(handler)
	handler = csrfMiddleware(deps.AllowedOrigin)(handler)
	handler = authMiddleware(deps.AuthToken, deps.Sessions, exempt)(handler)
	handler = corsMiddleware(deps.AllowedOrigin)(handler)
	handler = newRateLimiter(180).middleware(handler)
	handler = loggingMiddleware(handler)

	// Terminal WebSocket: registered outside the main middleware stack so the
	// ResponseWriter keeps its http.Hijacker interface for the upgrade.
	// Auth is checked inline. Rate limiting and logging applied separately.
	termRL := newRateLimiter(30) // 30 req/min for terminal connections
	var termHandler http.Handler = &TerminalHandler{
		AuthToken:     deps.AuthToken,
		Sessions:      deps.Sessions,
		AllowedOrigin: deps.AllowedOrigin,
	}
	termHandler = termRL.middleware(termHandler)
	termHandler = loggingMiddleware(termHandler)

	outer := http.NewServeMux()
	outer.Handle("/api/terminal", termHandler)
	outer.Handle("/", handler)

	return outer
}
