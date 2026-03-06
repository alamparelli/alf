package controlcenter

import (
	"io/fs"
	"net/http"
	"path/filepath"
	"time"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/provider"
)

// Deps holds all dependencies needed to build the control center.
type Deps struct {
	ConfigStore    ConfigStore
	TierStore      TierStore
	ContextStore    ResourceStore
	ToolStore      ResourceStore
	SkillStore     ResourceStore
	PageStore      ResourceStore
	LogReader      LogReader
	StatusProvider StatusProvider
	Notifier       Notifier
	Magic          *MagicStore
	Sessions       *SessionStore
	ChatService    *ChatService // nil if chat API disabled
	AgentStore     agents.Store  // nil if agents disabled
	MemStore       MemoryStorer       // nil if memory unavailable
	MemProvider    provider.Provider  // nil if memory unavailable
	AuthToken        string
	AllowedOrigin    string // CORS origin allowlist (from externalURL)
	SecureCookies    bool   // true when CC is behind HTTPS
	AuthBanThreshold int    // failed /auth attempts before IP ban (0 = default 10)
	AuthBanDuration  int    // IP ban duration in minutes (0 = default 15)
	DataDir          string
	ConfigDir      string
	SkillsDir      string
	DashboardHTML  string
	WebFS          fs.FS // embedded web assets (style.css, app.js)
}

// maxPageSize is the maximum size for a page file (5 MB).
const maxPageSize = 5 << 20

// StoreFactory creates concrete store implementations from data and config directories.
func StoreFactory(dataDir, configDir string) (ConfigStore, TierStore, ResourceStore, ResourceStore, ResourceStore, ResourceStore) {
	cs := NewFileConfigStore(ConfigPath(configDir))
	ts := NewFileTierStore(TiersPath(configDir))
	ms := NewFileResourceStore(filepath.Join(dataDir, "context"), ".md")
	tools := NewFileResourceStore(filepath.Join(dataDir, "tools"), ".json")
	skills := NewFileResourceStore(filepath.Join(dataDir, "skills"), ".json")
	pages := NewFileResourceStoreWithLimit(filepath.Join(dataDir, "pages"), ".html", maxPageSize)
	return cs, ts, ms, tools, skills, pages
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

	// Pages: API listing/CRUD + raw HTML serving.
	if deps.PageStore != nil {
		mux.Handle("/api/pages/", &ResourceHandler{
			Store: deps.PageStore,
		})
		mux.Handle("/pages/", &PageHandler{
			Store: deps.PageStore,
		})
	}

	// Chat API (mobile app).
	if deps.ChatService != nil {
		mux.Handle("/api/chat", &ChatHandler{Service: deps.ChatService})
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
	handler = authMiddleware(deps.AuthToken, deps.Sessions, exempt)(handler)
	handler = corsMiddleware(deps.AllowedOrigin)(handler)
	handler = newRateLimiter(180).middleware(handler)
	handler = loggingMiddleware(handler)

	return handler
}
