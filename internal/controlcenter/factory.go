package controlcenter

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/firewall"
	"github.com/alamparelli/alf/internal/marketplace"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/scheduler"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
	"github.com/alamparelli/alf/internal/vault"
)

// unifiedPermChecker wraps the marketplace PermissionChecker with a fallback
// to the local manifest.json for apps not tracked by the marketplace.
// This ensures both local and marketplace apps use the same permission model.
type unifiedPermChecker struct {
	marketplace marketplace.PermissionChecker
	dataDir     string
}

func (u *unifiedPermChecker) HasPermission(slug, perm string) bool {
	if u.marketplace.IsTracked(slug) {
		return u.marketplace.HasPermission(slug, perm)
	}
	// Local app — read permissions from manifest.json.
	return u.localManifestHasPerm(slug, perm)
}

func (u *unifiedPermChecker) IsTracked(slug string) bool {
	return u.marketplace.IsTracked(slug)
}

func (u *unifiedPermChecker) localManifestHasPerm(slug, perm string) bool {
	data, err := os.ReadFile(filepath.Join(u.dataDir, "apps", slug, "manifest.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false
	}
	for _, p := range manifest.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// Deps holds all dependencies needed to build the control center.
type Deps struct {
	ConfigStore    ConfigStore
	TierStore      TierStore
	ContextStore    ResourceStore
	ToolStore      ResourceStore
	SkillStore     ResourceStore
	SkillCatalog   skills.Store // runtime skill catalog (all dirs)
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
	FirewallStore  *firewall.Store      // nil if firewall unavailable
	FirewallProxy  *firewall.Proxy     // nil if firewall unavailable
	NetTracker     *firewall.NetTracker // nil if nettrack unavailable
	VaultManager     *vault.Manager      // nil if vault unavailable
	EventBroker      *EventBroker           // global SSE event bus
	ScheduleEvents   *ScheduleEventBroker // nil if scheduler unavailable (deprecated, use EventBroker)
	ToolRegistry     *tooling.Registry    // nil if tool registry unavailable
	ProviderRegistry *provider.Registry   // nil if provider registry unavailable
	ModelCache       *ModelCache           // nil if model cache unavailable
	Marketplace      *marketplace.Manager   // nil if marketplace unavailable
	OnVaultUnlock    func()                // called after vault unlock (e.g. secret migration)
	OnTaskEvent      func(taskID, status, summary string) // called when a task completes or needs attention
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
	cfg, err := cs.Load()
	if err != nil {
		cfg = DefaultConfig()
	}
	ts := NewFileTierStore(TiersPathFromConfig(configDir, cfg))
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

// Handlers holds the HTTP handlers returned by HandlerFactory.
type Handlers struct {
	// Main is the full handler for TCP connections (includes stripToolsSocketHeader).
	Main http.Handler
	// Internal is the handler WITHOUT stripToolsSocketHeader, for the Unix tools proxy.
	// ToolsProxy injects X-Tools-Socket which the auth middleware trusts.
	Internal http.Handler
}

// HandlerFactory builds the HTTP mux with all routes and middleware.
func HandlerFactory(deps Deps) Handlers {
	mux := http.NewServeMux()

	// API routes.
	mux.Handle("/api/config", &ConfigHandler{
		Store:       deps.ConfigStore,
		Notifier:    deps.Notifier,
		Event:       ReloadConfig,
		EventBroker: deps.EventBroker,
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
		TierStore:    deps.TierStore,
		Notifier:     deps.Notifier,
		DataDir:      deps.DataDir,
		ToolRegistry: deps.ToolRegistry,
		ModelCache:   deps.ModelCache,
		EventBroker:  deps.EventBroker,
	})
	mux.Handle("/api/tiers/configs", &TierConfigsHandler{
		ConfigDir:   deps.ConfigDir,
		TierStore:   deps.TierStore,
		ConfigStore: deps.ConfigStore,
		Notifier:    deps.Notifier,
		EventBroker: deps.EventBroker,
	})
	mux.Handle("/api/tiers/configs/", &TierConfigsHandler{
		ConfigDir:   deps.ConfigDir,
		TierStore:   deps.TierStore,
		ConfigStore: deps.ConfigStore,
		Notifier:    deps.Notifier,
		EventBroker: deps.EventBroker,
	})

	// Backend models discovery.
	mux.Handle("/api/backends/", &BackendsModelsHandler{
		Registry: deps.ProviderRegistry,
		Cache:    deps.ModelCache,
	})

	// Resource CRUD routes.
	mux.Handle("/api/context/", &ResourceHandler{
		Store: deps.ContextStore,
	})
	mux.Handle("/api/tools/", &ResourceHandler{
		Store:       deps.ToolStore,
		Notifier:    deps.Notifier,
		Event:       ReloadTools,
		EventBroker: deps.EventBroker,
	})
	// Skill catalog (all runtime skills from all directories).
	if deps.SkillCatalog != nil {
		mux.Handle("/api/skills/catalog", &SkillsCatalogHandler{
			SkillStore: deps.SkillCatalog,
			SkillsDir:  deps.SkillsDir,
			DataDir:    deps.DataDir,
		})
	}
	mux.Handle("/api/skills/import", &SkillImportHandler{
		DataDir:          deps.DataDir,
		ProviderRegistry: deps.ProviderRegistry,
		ModelCache:       deps.ModelCache,
		Notifier:         deps.Notifier,
	})
	mux.Handle("/api/skills/", &ResourceHandler{
		Store:       deps.SkillStore,
		Notifier:    deps.Notifier,
		Event:       ReloadSkills,
		EventBroker: deps.EventBroker,
	})

	// Permission checker: wraps marketplace manager with local manifest fallback.
	// Marketplace-tracked apps use the permission system; local apps check manifest.json.
	var permChecker marketplace.PermissionChecker
	if deps.Marketplace != nil {
		permChecker = &unifiedPermChecker{
			marketplace: deps.Marketplace,
			dataDir:     deps.DataDir,
		}
	} else {
		log.Println("[security] marketplace not configured — app permission enforcement disabled")
	}

	// Apps: directory-based apps with index.html + assets.
	if deps.AppStore != nil {
		appStorage := &AppStorageHandler{DataDir: deps.DataDir, Perms: permChecker}
		appUpload := &AppUploadHandler{DataDir: deps.DataDir, Perms: permChecker}
		appErrors := &AppErrorHandler{DataDir: deps.DataDir}
		mux.Handle("/api/apps/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "/storage") {
				appStorage.ServeHTTP(w, r)
				return
			}
			if strings.Contains(r.URL.Path, "/upload") {
				appUpload.ServeHTTP(w, r)
				return
			}
			if strings.Contains(r.URL.Path, "/errors") {
				appErrors.ServeHTTP(w, r)
				return
			}
			if strings.Contains(r.URL.Path, "/permissions") {
				handleAppPermissions(w, r, permChecker)
				return
			}
			if strings.Contains(r.URL.Path, "/restart") {
				handleAppRestart(w, r)
				return
			}
			(&AppListHandler{Store: deps.AppStore}).ServeHTTP(w, r)
		}))
		mux.Handle("/apps/", &AppHandler{
			Store: deps.AppStore,
		})
	}

	// Marketplace: app lifecycle management.
	if deps.Marketplace != nil {
		mpHandler := &MarketplaceHandler{Manager: deps.Marketplace, EventBroker: deps.EventBroker, VaultManager: deps.VaultManager}
		mux.Handle("/api/marketplace", mpHandler)
		mux.Handle("/api/marketplace/", mpHandler)
	}

	// Chat API (mobile app).
	if deps.ChatService != nil {
		mux.Handle("/api/chat", &ChatHandler{Service: deps.ChatService})
		mux.Handle("/api/chat/conversations", &ChatConversationsHandler{Service: deps.ChatService})
		mux.Handle("/api/chat/conversations/", &ChatConversationHandler{Service: deps.ChatService, ConfigStore: deps.ConfigStore})
		mux.Handle("/api/chat/job", &ChatJobHandler{Service: deps.ChatService})
		mux.Handle("/api/chat/upload", &ChatMediaHandler{Service: deps.ChatService})
		mux.Handle("/api/chat/media/", &ChatMediaHandler{Service: deps.ChatService})
		mux.Handle("/api/chat/react", &ChatReactHandler{Service: deps.ChatService})
		mux.Handle("/api/chat/skills", &ChatSkillsHandler{Service: deps.ChatService})
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
	mux.Handle("/api/schedules/run", &ScheduleRunHandler{
		Engine: deps.Scheduler,
	})
	mux.Handle("/api/schedules/logs", &ScheduleLogsHandler{
		RunLog: deps.ScheduleRunLog,
	})
	// Global SSE event bus (replaces per-feature SSE endpoints).
	if deps.EventBroker != nil {
		mux.Handle("/api/events", deps.EventBroker)
	}
	// Backward compat: keep /api/schedules/events for now.
	if deps.ScheduleEvents != nil {
		mux.Handle("/api/schedules/events", deps.ScheduleEvents)
	}

	// Orchestrator tasks (independent of chat pipeline - no mutex contention).
	taskHandler := &TasksHandler{
		Orchestrator: deps.Orchestrator,
		DataDir:      deps.DataDir,
		ContextDir:   filepath.Join(deps.DataDir, "context"),
	}
	if deps.ChatService != nil {
		taskHandler.TierStore = deps.ChatService.TierStore
		taskHandler.SkillStore = deps.ChatService.SkillStore
		taskHandler.Recaller = deps.ChatService.Recaller
		taskHandler.ResolveModel = deps.ChatService.ResolveModel
	}
	taskHandler.OnTaskEvent = deps.OnTaskEvent
	taskHandler.EventBroker = deps.EventBroker
	mux.Handle("/api/tasks", taskHandler)
	mux.Handle("/api/tasks/approve", &TaskApproveHandler{Orchestrator: deps.Orchestrator})

	// Agent teams management.
	mux.Handle("/api/teams", &TeamsHandler{
		AgentStore:  deps.AgentStore,
		DataDir:     deps.DataDir,
		Notifier:    deps.Notifier,
		EventBroker: deps.EventBroker,
	})

	// Firewall.
	mux.Handle("/api/firewall", &FirewallHandler{
		Store:       deps.FirewallStore,
		Proxy:       deps.FirewallProxy,
		NetTracker:  deps.NetTracker,
		Notifier:    deps.Notifier,
		EventBroker: deps.EventBroker,
	})
	mux.Handle("/api/firewall/lookup", &FirewallLookupHandler{})
	mux.Handle("/api/firewall/killswitch", &FirewallKillSwitchHandler{
		NetTracker:  deps.NetTracker,
		EventBroker: deps.EventBroker,
	})

	// Vault (secrets proxy).
	vaultH := &VaultHandler{
		Manager:     deps.VaultManager,
		ContextDir:  filepath.Join(deps.DataDir, "context"),
		DataDir:     deps.DataDir,
		OnUnlock:    deps.OnVaultUnlock,
		EventBroker: deps.EventBroker,
	}
	mux.Handle("/api/vault/", vaultH)
	mux.Handle("/api/vault", vaultH)

	// Activity monitor (active operations).
	mux.Handle("/api/activity", &ActivityHandler{
		ChatService:  deps.ChatService,
		Scheduler:    deps.Scheduler,
		Orchestrator: deps.Orchestrator,
	})

	// Setup wizard (onboarding).
	mux.Handle("/api/setup/", &SetupHandler{
		ConfigStore:   deps.ConfigStore,
		TierStore:     deps.TierStore,
		Vault:         deps.VaultManager,
		PresetsDir:    filepath.Join(deps.ConfigDir, "setup-presets"),
		Notifier:      deps.Notifier,
		ConfigDir:     deps.ConfigDir,
		OnVaultUnlock: deps.OnVaultUnlock,
		DataDir:       deps.DataDir,
	})

	// Telegram integration.
	mux.Handle("/api/telegram", &TelegramHandler{
		Vault: deps.VaultManager,
	})

	// Docs (embedded markdown).
	mux.Handle("/api/docs/", &DocsHandler{})

	// Search (apps, files, docs).
	mux.Handle("/api/search", &SearchHandler{
		AppStore:    deps.AppStore,
		Marketplace: deps.Marketplace,
		DataDir:     deps.DataDir,
		ConfigDir:   deps.ConfigDir,
		SkillsDir:   deps.SkillsDir,
	})

	// Debug: tier prompt inspector API + tool tester page.
	if deps.ChatService != nil {
		mux.Handle("/api/debug/prompt", &DebugPromptHandler{ChatService: deps.ChatService})
	}
	mux.Handle("/debug/tools", &DebugToolsPageHandler{})

	// LLM invocation (used by system-tools CLI).
	if deps.ToolRegistry != nil {
		mux.Handle("/api/llm/invoke", &LLMInvokeHandler{ToolRegistry: deps.ToolRegistry, TierStore: deps.TierStore})
	}

	// Developer tools (publish to marketplace, validate, etc.)
	mux.Handle("/api/developer/", &DeveloperHandler{
		DataDir:      deps.DataDir,
		VaultManager: deps.VaultManager,
	})

	// Bash command execution.
	bashHandler := &BashHandler{
		Perms:        permChecker,
		DataDir:      deps.DataDir,
		VaultManager: deps.VaultManager,
	}
	mux.Handle("/api/bash", bashHandler)

	// Restart.
	mux.Handle("/api/restart", &RestartHandler{})

	// Health (exempt from auth).
	mux.Handle("/health", &HealthHandler{})

	// Magic link auth (exempt from auth - does its own validation).
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

	// Static assets (CSS, JS) - served from embedded web/ directory.
	if deps.WebFS != nil {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(deps.WebFS))))
	}

	// Dashboard.
	mux.Handle("/", &DashboardHandler{
		HTML: deps.DashboardHTML,
	})

	// Apply middleware stack (outermost first).
	exempt := map[string]bool{"/health": true, "/auth": true, "/api/vault/oauth2/callback": true}
	var handler http.Handler = mux
	handler = jsonMiddleware(handler)
	handler = appIsolationMiddleware(deps.AllowedOrigin)(handler)
	handler = csrfMiddleware(deps.AllowedOrigin)(handler)
	handler = authMiddleware(deps.AuthToken, deps.Sessions, exempt, func() string {
		return GetMobileToken(deps.VaultManager)
	})(handler)
	handler = corsMiddleware(deps.AllowedOrigin)(handler)
	handler = securityHeadersMiddleware(handler)
	handler = newRateLimiter(15).withAuthLimit(600, deps.Sessions).withToken(deps.AuthToken).withExtraTokens(func() string {
		return GetMobileToken(deps.VaultManager)
	}).middleware(handler) // 15/min anonymous, no limit authenticated (session, bearer, or mobile token)
	handler = loggingMiddleware(handler)

	// Shared extra token providers for handlers outside the middleware stack.
	extraTokenFns := []func() string{func() string { return GetMobileToken(deps.VaultManager) }}

	// Terminal WebSocket: registered outside the main middleware stack so the
	// ResponseWriter keeps its http.Hijacker interface for the upgrade.
	// Auth is checked inline. Rate limiting and logging applied separately.
	termRL := newRateLimiter(30) // 30 req/min for terminal connections
	var termHandler http.Handler = &TerminalHandler{
		AuthToken:     deps.AuthToken,
		Sessions:      deps.Sessions,
		ExtraTokenFns: extraTokenFns,
		AllowedOrigin: deps.AllowedOrigin,
	}
	termHandler = termRL.middleware(termHandler)
	termHandler = loggingMiddleware(termHandler)

	// SSH WebSocket proxy: same pattern as terminal — outside middleware, inline auth.
	sshRL := newRateLimiter(30)
	var sshHandler http.Handler = &SSHHandler{
		Manager:       deps.VaultManager,
		AuthToken:     deps.AuthToken,
		Sessions:      deps.Sessions,
		ExtraTokenFns: extraTokenFns,
		AllowedOrigin: deps.AllowedOrigin,
	}
	sshHandler = sshRL.middleware(sshHandler)
	sshHandler = loggingMiddleware(sshHandler)

	// Internal handler: used by ToolsProxy (Unix socket). Does NOT strip
	// X-Tools-Socket header, so the auth bypass works correctly.
	internalOuter := http.NewServeMux()
	internalOuter.Handle("/api/terminal", termHandler)
	internalOuter.Handle("/api/ssh/", sshHandler)
	internalOuter.Handle("/", handler)

	// Main handler: for TCP connections. Strips X-Tools-Socket to prevent
	// external clients from forging the header to bypass auth.
	tcpHandler := stripToolsSocketHeader(internalOuter)

	return Handlers{Main: tcpHandler, Internal: internalOuter}
}

// handleAppPermissions returns the permissions for an app.
// GET /api/apps/{slug}/permissions → {"permissions": [...] or null}
func handleAppPermissions(w http.ResponseWriter, r *http.Request, perms marketplace.PermissionChecker) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 1 {
		http.NotFound(w, r)
		return
	}
	slug := parts[0]

	if perms == nil {
		respondJSON(w, http.StatusOK, map[string]any{"permissions": nil})
		return
	}

	// Use GetPermissions if available (Manager implements it)
	type permGetter interface {
		GetPermissions(slug string) []string
	}
	if pg, ok := perms.(permGetter); ok {
		p := pg.GetPermissions(slug)
		respondJSON(w, http.StatusOK, map[string]any{"permissions": p})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"permissions": nil})
}

// AppRestarter can restart an app's background service.
type AppRestarter interface {
	RestartApp(slug string)
}

// appRestarterHolder is set after the supervisor is created.
// Accessed from the /api/apps/{slug}/restart handler.
var appRestarterHolder struct {
	sync.Mutex
	r AppRestarter
}

// SetAppRestarter registers the supervisor for app restart requests.
func SetAppRestarter(r AppRestarter) {
	appRestarterHolder.Lock()
	appRestarterHolder.r = r
	appRestarterHolder.Unlock()
}

// handleAppRestart restarts an app's background service.
// POST /api/apps/{slug}/restart → {"ok": true}
func handleAppRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	slug := parts[0]

	appRestarterHolder.Lock()
	restarter := appRestarterHolder.r
	appRestarterHolder.Unlock()

	if restarter == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "supervisor not available"})
		return
	}

	restarter.RestartApp(slug)
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "slug": slug})
}
