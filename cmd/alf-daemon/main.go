package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/conversation"
	"github.com/alamparelli/alf/internal/firewall"
	"github.com/alamparelli/alf/internal/secrets"
	"github.com/alamparelli/alf/internal/vault"
	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/eventlog"
	"github.com/alamparelli/alf/internal/gittrack"
	"github.com/alamparelli/alf/internal/media"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memstore"
	"github.com/alamparelli/alf/internal/mood"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/router"
	"github.com/alamparelli/alf/internal/scheduler"
	"github.com/alamparelli/alf/internal/session"
	"github.com/alamparelli/alf/internal/signal"
	"github.com/alamparelli/alf/internal/supervisor"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
	tgclient "github.com/alamparelli/alf/internal/telegram"
	"github.com/alamparelli/alf/internal/updater"
	"github.com/alamparelli/alf/internal/voice"
)

var version = "dev"

func main() {
	// Ensure daemon-created files are group-writable (umask 002 = rwxrwxr-x).
	syscall.Umask(0o002)

	var token, chatID string // resolved from vault after unlock
	authToken := secrets.ReadSecret("CC_AUTH_TOKEN")

	// Set Claude OAuth token as env var if available (picked up by safeEnv for subprocesses).
	if oauthToken := secrets.ReadSecret("CLAUDE_OAUTH_TOKEN"); oauthToken != "" {
		os.Setenv("CLAUDE_CODE_OAUTH_TOKEN", oauthToken)
		log.Println("Claude OAuth token loaded from secret")
	}

	// Verify claude CLI is available.
	if _, err := exec.LookPath("claude"); err != nil {
		log.Fatal("claude CLI not found in PATH")
	}

	// Data directory for logs, sessions, context, etc.
	dataDir := "/home/alf/data"
	if d := os.Getenv("ALF_DATA_DIR"); d != "" {
		dataDir = d
	}

	// Config directory (RW for CC, separate from data volume).
	configDir := "/opt/alf/config.d"
	if d := os.Getenv("ALF_CONFIG_DIR"); d != "" {
		configDir = d
	}

	// telegramEnabled is finalized after vault credentials are loaded.
	telegramEnabled := false

	// Skills directory (RW for CC, separate from data volume).
	skillsDir := "/opt/alf/skills.d"
	if d := os.Getenv("ALF_SKILLS_DIR"); d != "" {
		skillsDir = d
	}

	// Home directory (parent of data). Used for HOME env, symlinks, bashrc.
	homeDir := "/home/alf"
	if d := os.Getenv("ALF_HOME_DIR"); d != "" {
		homeDir = d
	}

	// Clean up stale signal sockets from previous runs (e.g. killed during redeploy).
	if matches, _ := filepath.Glob(filepath.Join(dataDir, "signal-*.sock")); len(matches) > 0 {
		for _, s := range matches {
			os.Remove(s)
		}
		log.Printf("cleaned up %d stale signal sockets", len(matches))
	}

	// allowedChatIDs is resolved after vault loads Telegram credentials.
	var allowedChatIDs map[int64]bool

	// Shared stats for CC status endpoint.
	stats := cc.NewStats()

	// Reload channel: CC writes, daemon reads.
	reloadCh := make(chan cc.ReloadEvent, 4)

	// Magic link auth stores (shared between CC and daemon).
	magic := cc.NewMagicStore(nil)
	magic.StartCleanup()
	sessions := cc.NewFileSessionStore(filepath.Join(configDir, "sessions.json"), nil)
	sessions.StartCleanup()

	// CC external URL for magic link generation.
	ccExternalURL := os.Getenv("CC_EXTERNAL_URL")
	if ccExternalURL == "" {
		ccExternalURL = "http://localhost:8080"
	}

	// Ensure log directory exists before setting up file logging.
	os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755)

	// Tee log output to both stdout and a file so CC and Claude can read logs.
	logPath := filepath.Join(dataDir, "logs", "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
		defer logFile.Close()
		// Rotate: truncate if over 2MB.
		if info, err := logFile.Stat(); err == nil && info.Size() > 2<<20 {
			logFile.Truncate(0)
			logFile.Seek(0, 0)
		}
	}

	log.Printf("alf-daemon %s starting...", version)

	// Write version file so Claude -p can read it.
	os.WriteFile(filepath.Join(dataDir, ".version"), []byte(version), 0o644)

	// Ensure directories exist.
	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(skillsDir, 0o755)
	os.MkdirAll(filepath.Join(dataDir, "logs", "events"), 0o755)
	os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o755)
	for _, sub := range []string{"config", "tools", "skills", "context"} {
		os.MkdirAll(filepath.Join(dataDir, sub), 0o755)
	}
	for _, sub := range []string{"agents", "apps"} {
		os.MkdirAll(filepath.Join(dataDir, sub), 0o775)
	}
	os.MkdirAll(filepath.Join(dataDir, "agents", "teams"), 0o775)

	// Populate tools.d/ with symlinks to each system tool in /opt/alf/tools/.
	// The host volume mount overwrites any Dockerfile-created symlinks,
	// so we link individual tools at runtime instead.
	linkSystemTools(filepath.Join(dataDir, "tools.d"), "/opt/alf/tools.d")

	// Ensure Claude Code finds its native binary at $HOME/.local/bin/claude.
	// The volume mount overwrites any Dockerfile-created structure.
	if claudePath, err := exec.LookPath("claude"); err == nil {
		localBin := filepath.Join(homeDir, ".local", "bin")
		os.MkdirAll(localBin, 0o755)
		link := filepath.Join(localBin, "claude")
		os.Remove(link)
		if err := os.Symlink(claudePath, link); err == nil {
			log.Printf("linked %s → %s", link, claudePath)
		}
	}

	// Ensure ~/.local/bin is in PATH for interactive shells (terminal, docker exec).
	ensureBashrcPath(homeDir)

	// Persist/restore .claude.json via the .claude/ volume mount.
	// Claude CLI replaces symlinks with real files, so we use copy-based persistence.
	syncClaudeJSON(homeDir)

	// Create data directory symlinks for config and skills.
	setupDataSymlinks(dataDir, configDir, skillsDir)

	// Seed bundled skills from Docker image defaults (no-op if already present).
	seedBundledSkills(skillsDir)

	// Set up user-packages paths.
	setupUserPackagesPaths()

	// Permissions are fixed by the entrypoint (Phase 2.5) before dropping to uid 1000.

	// Migrate config from old data/config/ to configDir (before loading).
	migrateConfig(dataDir, configDir)

	// Generate llms.txt index of available documentation.
	writeLLMSIndex(dataDir)

	// Load initial config.
	configStore := cc.NewFileConfigStore(cc.ConfigPath(configDir))
	cfg, err := configStore.Load()
	if err != nil {
		log.Printf("warning: failed to load config: %v", err)
		cfg = cc.DefaultConfig()
	}
	if cfg.MaxSessions > 0 {
		sessions.SetMaxSessions(cfg.MaxSessions)
	}
	// Seed default tiers.json if not present (from image-embedded copy).
	seedDefaultTiers(configDir)
	// Remove stale Claude settings that may restrict tool permissions.
	cleanClaudeSettings(homeDir)

	// Start outbound traffic firewall proxy.
	fwStore := firewall.NewStore(configDir)
	fwCfg, err := fwStore.Load()
	if err != nil {
		log.Printf("warning: failed to load firewall config: %v - using defaults", err)
		fwCfg = firewall.DefaultConfig()
	}
	fwProxy := firewall.NewProxy(fwCfg)
	go func() {
		addr := fmt.Sprintf("127.0.0.1:%d", fwCfg.Port)
		log.Printf("[firewall] proxy starting on %s (mode=%s, %d rules)", addr, fwCfg.Mode, len(fwCfg.Rules))
		if err := http.ListenAndServe(addr, fwProxy.Handler()); err != nil {
			log.Printf("[firewall] proxy error: %v", err)
		}
	}()
	proxyURL := fmt.Sprintf("http://127.0.0.1:%d", fwCfg.Port)
	os.Setenv("HTTP_PROXY", proxyURL)
	os.Setenv("HTTPS_PROXY", proxyURL)
	os.Setenv("NO_PROXY", "127.0.0.1,localhost")

	// Start vault-server if binary is available.
	var vaultMgr *vault.Manager
	if _, err := exec.LookPath("vault-server"); err == nil {
		vaultMgr = vault.NewManager("/opt/alf/vault-data")
		if err := vaultMgr.Start(context.Background()); err != nil {
			log.Printf("warning: vault-server failed to start: %v", err)
			vaultMgr = nil
		} else if pw := vaultPassword(vaultMgr); pw != "" {
			if err := vaultMgr.AutoUnlock(pw); err != nil {
				log.Printf("warning: vault auto-unlock failed: %v", err)
			} else if _, err := vaultMgr.CreateProxyToken(); err != nil {
				log.Printf("warning: vault proxy token failed: %v", err)
			} else {
				os.Setenv("VAULT_ADDR", vaultMgr.Addr())
				os.Setenv("VAULT_TOKEN", vaultMgr.ProxyToken())
				log.Println("vault: unlocked, proxy token created")
			}
		} else {
			log.Println("vault: started (locked - set vault_master_password or unlock via Control Center)")
		}
	}

	// Load Telegram credentials from vault.
	if vaultMgr != nil && vaultMgr.AdminToken() != "" {
		if v, err := vaultMgr.GetSecret("telegram_bot_token"); err == nil && v != "" {
			token = v
		}
		if v, err := vaultMgr.GetSecret("telegram_chat_id"); err == nil && v != "" {
			chatID = v
		}
		if token != "" && chatID != "" {
			log.Println("Telegram config loaded from vault")
		}
	}

	telegramEnabled = token != "" && chatID != ""
	if telegramEnabled {
		allowedChatIDs = parseAllowedChatIDs(chatID)
	} else {
		log.Println("Telegram not configured - running in Control Center-only mode")
	}

	// Load initial tiers config. Honour optional tiers_file override in config.
	tiersPath := cc.TiersPathFromConfig(configDir, cfg)
	tierStore := cc.NewFileTierStore(tiersPath)
	if err := tierStore.Reload(); err != nil {
		log.Printf("ERROR: failed to load tiers: %v - using defaults (your tiers.json edits are IGNORED)", err)
	}

	// Load skill catalog: system → bundled copy → user (later overrides earlier).
	skillStore := skills.NewFileSkillStore(skillsDir, filepath.Join(dataDir, "skills.d"), filepath.Join(dataDir, "skills"))

	// Watch config files for changes and auto-reload.
	// The tiers path function is a closure so the watcher always tracks the live path.
	go watchConfigFiles(configDir, dataDir, func() string { return tierStore.Path() }, reloadCh)

	// Load agent team configurations.
	agentStore := agents.NewFileAgentStore(filepath.Join(dataDir, "agents", "teams"))

	// Auto-enable the agent tier when teams are configured.
	if teams := agentStore.All(); len(teams) > 0 {
		autoEnableAgentTier(tierStore)
	}

	// Set process-wide timezone from config so log timestamps are correct.
	time.Local = resolveTimezone(cfg.Timezone)

	// Apply DNS servers from config (gVisor compatibility).
	applyDNS(cfg)

	// Bootstrap default memory files (soul.md, mood.md, index.md).
	contextDir := filepath.Join(dataDir, "context")
	memory.Bootstrap(contextDir)

	// Seed default heartbeat.md if missing.
	seedHeartbeatFile(contextDir)

	// Generate toolbox.md - explicit list of all available CLI tools.
	memory.GenerateToolbox(contextDir, dataDir)

	// Generate daily mood (overwrites mood.md if date changed).
	mood.GenerateDaily(contextDir)

	// Session store for Claude --resume support.
	// SessionTimeout: >0 = minutes, 0 = no timeout, <0 (absent) = default 30m.
	sessionTimeout := time.Duration(cfg.SessionTimeout) * time.Minute
	if cfg.SessionTimeout < 0 {
		sessionTimeout = 30 * time.Minute
	}
	chatSessions := session.New(dataDir, sessionTimeout)

	// JSONL event logger.
	eventLog := eventlog.New(dataDir)
	defer eventLog.Close()

	// Git tracker for data directory version history.
	var git *gittrack.Tracker
	if cfg.GitTrack {
		git = gittrack.New(dataDir)
		if err := git.Init(); err != nil {
			log.Printf("warning: git tracker init failed: %v", err)
			git = nil
		} else {
			defer git.Stop()
			log.Printf("git tracker initialized")
		}
	}

	// Voice transcriber (HTTP client to whisper-service container).
	whisperURL := os.Getenv("WHISPER_URL")
	whisperSecret := secrets.ReadSecret("WHISPER_SHARED_SECRET")
	var transcriber *voice.Transcriber
	if whisperURL != "" && whisperSecret != "" {
		instanceID, _ := os.Hostname()
		if instanceID == "" {
			instanceID = "alf-default"
		}
		var err error
		transcriber, err = voice.New(whisperURL, instanceID, whisperSecret, 120*time.Second)
		if err != nil {
			log.Printf("voice transcription disabled: %v", err)
		} else {
			go func() {
				for attempt := 1; attempt <= 30; attempt++ {
					if err := transcriber.Start(); err == nil {
						return
					} else {
						log.Printf("voice: registration attempt %d/30 failed: %v", attempt, err)
					}
					time.Sleep(10 * time.Second)
				}
				log.Println("voice: gave up registering with whisper service after 30 attempts")
			}()
		}
	} else {
		log.Println("voice transcription disabled (WHISPER_URL or WHISPER_SHARED_SECRET not set)")
	}

	// ONNX embedding engine (Go native, no Python sidecar).
	modelDir := "/opt/alf/models/multilingual-e5-small"
	var memDB *memstore.Store
	if !cfg.EffectiveMemoryEnabled() {
		log.Println("memstore: disabled by config (memory_enabled=false)")
	} else if memstore.IsAvailable(modelDir) {
		embedder, err := memstore.NewEmbedder(modelDir)
		if err != nil {
			log.Printf("memstore: embedder disabled: %v", err)
		} else {
			if err := embedder.Start(); err != nil {
				log.Printf("memstore: embedder start failed: %v", err)
			} else {
				defer embedder.Stop()
			}

			dedupCfg := memstore.DedupConfig{
				TextThreshold:   cfg.EffectiveMemoryDedupTextThreshold(),
				CosineThreshold: cfg.EffectiveMemoryDedupCosineThreshold(),
			}
			memDB, err = memstore.New(filepath.Join(contextDir, "memory.db"), embedder, dedupCfg)
			if err != nil {
				log.Printf("warning: memory store init failed: %v", err)
			} else {
				defer memDB.Close()
				sockPath := filepath.Join(contextDir, "memstore.sock")
				go memDB.ServeUnix(sockPath)
				log.Printf("memstore: ready (db=%s, socket=%s)", filepath.Join(contextDir, "memory.db"), sockPath)
			}
		}
	} else {
		log.Println("memstore: embedder disabled (model files not found)")
	}

	// Ring buffer tracking Alf's sent message IDs for reaction matching.
	alfMsgIDs := newRingBuffer(200)
	chatHistory := newChatHistoryBuffer(10) // last 10 exchanges per chat

	// Chat message store for mobile app API.
	chatStore := cc.NewChatStore(dataDir)
	// Unified conversation store (rich messages with content blocks).
	convStore := conversation.NewStore(dataDir)

	// Provider: spawn-per-call Claude CLI for responses.
	// Credential is nil — daemon already runs as uid 1000 (dropped by entrypoint).
	tiersTimeout := time.Duration(cfg.TiersTimeout) * time.Second // 0 → default 5m inside NewCLIProvider
	cliProvider := provider.NewCLIProvider(homeDir, dataDir, tiersTimeout, nil)

	// API backends: config-driven registration.
	apiHistory := provider.NewHistory(dataDir, 100, sessionTimeout)
	registry := provider.NewRegistry(cliProvider)
	registerBackends(registry, cfg, apiHistory, vaultMgr)

	// Multi-agent coordinator.
	resolveTier := func(tierName string) (agents.TierParams, bool) {
		for _, t := range tierStore.Current().Tiers {
			if t.Name == tierName {
				model := t.Model
				backend := t.Backend
				// Auto-detect API backend from model name.
				if (backend == "" || backend == "cli") && strings.Contains(model, "/") {
					names := registry.BackendNames()
					if len(names) > 0 {
						backend = names[0]
					}
				}
				if backend == "" || backend == "cli" {
					model = router.ResolveModel(t.Model)
				}
				return agents.TierParams{
					Model:        model,
					Backend:      backend,
					Tools:        t.Tools,
					Effort:       t.Effort,
					WriteCapable: t.WriteCapable,
					MaxTurns:     t.MaxTurns,
					SystemPrompt: t.SystemPrompt,
				}, true
			}
		}
		return agents.TierParams{}, false
	}
	orch := agents.NewOrchestrator(cliProvider, agentStore, dataDir, router.ResolveModel, resolveTier)
	orch.SetResolveProvider(func(backend string) provider.Provider {
		return registry.ForBackend(backend)
	})

	// Router model for message classification.
	routerBackend := tierStore.Current().RouterBackend
	routerModel := tierStore.Current().RouterModel
	isAPIRouter := routerBackend != "" && routerBackend != "cli"
	if isAPIRouter {
		// For API backends, use the model string as-is.
		if routerModel == "" {
			if ap := registry.GetAPIBackend(routerBackend); ap != nil {
				routerModel = ap.Name() // will get default from provider
			}
			if routerModel == "" {
				routerModel = "anthropic/claude-haiku-4-5"
			}
		}
	} else {
		routerModel = router.ResolveModel(routerModel)
		if routerModel == "" {
			routerModel = router.ResolveModel("haiku")
		}
	}

	// agentTeamsForRouter converts the agent store into router-friendly team info.
	agentTeamsForRouter := func() []router.AgentTeamInfo {
		teams := agentStore.All()
		infos := make([]router.AgentTeamInfo, 0, len(teams))
		for _, t := range teams {
			names := make([]string, len(t.Agents))
			for i, a := range t.Agents {
				names[i] = a.Name
			}
			infos = append(infos, router.AgentTeamInfo{
				Name:        t.Name,
				Description: t.Description,
				Agents:      names,
			})
		}
		return infos
	}

	// classifyMessageFull includes session context for continuity routing.
	classifyMessageFull := func(message string, tiers *cc.TiersConfig, lastTier string, msgCount int, recentContext string) router.Result {
		prompt := router.BuildClassifyPrompt(router.ClassifyInput{
			Message:       message,
			Tiers:         tiers,
			DataDir:       dataDir,
			ConfigDir:     configDir,
			AgentTeams:    agentTeamsForRouter(),
			LastTier:      lastTier,
			MessageCount:  msgCount,
			RecentContext: recentContext,
		})
		routerProv := registry.ForBackend(routerBackend)
		params := provider.Params{
			Model:    routerModel,
			MaxTurns: 2,
			DataDir:  dataDir,
		}
		start := time.Now()
		result, err := routerProv.Invoke(context.Background(), prompt, params, nil)
		if err != nil {
			log.Printf("router: classify error: %v", err)
			return router.FallbackResult(tiers)
		}
		log.Printf("router: classify took %dms (backend=%s)", time.Since(start).Milliseconds(), routerBackend)
		return router.InterpretRaw(result.Text, tiers, message)
	}

	// Chat service for mobile app API (shares Claude invocation with Telegram bot).
	classifyFn := func(message, lastTier string, msgCount int) cc.RouteResult {
		// Build recent context from conversation history (cross-session for continuity).
		recentCtx := conversation.BuildRouterContext(convStore.RecentAll(6), 3)
		rr := classifyMessageFull(message, tierStore.Current(), lastTier, msgCount, recentCtx)
		return cc.RouteResult{
			Tier:     rr.Tier,
			Response: rr.Response,
			Reason:   rr.Reason,
			React:    rr.React,
		}
	}
	chatService := cc.NewChatService(dataDir, configDir, contextDir, tierStore, chatSessions, eventLog, chatStore, transcriber, classifyFn, router.ResolveModel, cliProvider)
	chatService.Registry = registry
	chatService.SkillStore = skillStore
	chatService.Orchestrator = orch
	chatService.BackendConfigs = func() map[string]cc.BackendConfig { return cfg.Backends }
	chatService.ConvStore = convStore
	toolRegistry := tooling.NewRegistry(dataDir)
	nativeTools := []tooling.NativeTool{
		tooling.BashNativeTool{DataDir: dataDir},
		tooling.GrepNativeTool{DataDir: dataDir},
		tooling.GlobNativeTool{DataDir: dataDir},
		tooling.ReadFileNativeTool{DataDir: dataDir},
		tooling.WriteFileNativeTool{DataDir: dataDir},
		tooling.RemoveNativeTool{DataDir: dataDir},
	}
	toolExecutor := &tooling.Executor{
		DataDir:  dataDir,
		HomeDir:  homeDir,
		Registry: toolRegistry,
		Timeout:  30 * time.Second,
	}
	for _, t := range nativeTools {
		toolRegistry.RegisterNative(t)
		toolExecutor.RegisterNative(t)
	}
	chatService.ToolRegistry = toolRegistry
	chatService.ToolExecutor = toolExecutor
	orch.SetTooling(toolRegistry, toolExecutor)
	if memDB != nil {
		chatService.Recaller = &memStoreRecaller{store: memDB}
		chatService.MemStore = memDB
	}

	// Schedule adapter (engine set later after scheduler is created).
	schedAdapter := &ccScheduleAdapter{}
	var schedEventBroker *cc.ScheduleEventBroker

	// Start Control Center HTTP server.
	if authToken != "" || len(allowedChatIDs) > 0 {
		// On vault unlock, re-register backends and load Telegram credentials.
		onVaultUnlock := func() {
			if vaultMgr == nil || vaultMgr.AdminToken() == "" {
				return
			}
			// Re-register backends now that vault is unlocked and API keys are accessible.
			registerBackends(registry, cfg, apiHistory, vaultMgr)
			// Load Telegram credentials from vault if not already set.
			if token == "" {
				if v, err := vaultMgr.GetSecret("telegram_bot_token"); err == nil && v != "" {
					token = v
				}
			}
			if chatID == "" {
				if v, err := vaultMgr.GetSecret("telegram_chat_id"); err == nil && v != "" {
					chatID = v
				}
			}
			if token != "" && chatID != "" && !telegramEnabled {
				telegramEnabled = true
				log.Println("Telegram config loaded from vault (post-unlock)")
			}
		}
		// Task event callback: notify via CC chat store (system message).
		onTaskEvent := func(taskID, status, summary string) {
			var text string
			switch status {
			case "completed":
				text = "Task #" + taskID[:min(8, len(taskID))] + " completed"
				if summary != "" {
					text += "\n" + summary
				}
			case "failed", "timeout":
				text = "Task #" + taskID[:min(8, len(taskID))] + " " + status
			case "awaiting_arbitration":
				text = "Task #" + taskID[:min(8, len(taskID))] + " needs your input - check the Tasks tab"
			case "awaiting_approval":
				text = "Task #" + taskID[:min(8, len(taskID))] + " plan ready for review - check the Tasks tab"
			default:
				return
			}
			chatStore.Append(cc.ChatMessage{
				ID:   cc.NewMessageID(),
				Role: "system",
				Text: text,
			})
			log.Printf("[tasks] event: task=%s status=%s", taskID[:min(8, len(taskID))], status)
		}
		server, broker, err := cc.New(dataDir, configDir, skillsDir, stats, version, authToken, ccExternalURL, cfg, reloadCh, magic, sessions, chatService, memDB, cliProvider, orch, agentStore, schedAdapter, fwStore, fwProxy, vaultMgr, registry, onVaultUnlock, onTaskEvent)
		if err != nil {
			log.Printf("warning: failed to start Control Center: %v", err)
		} else {
			schedEventBroker = broker
			go func() {
				if err := server.Start(); err != nil && err != http.ErrServerClosed {
					log.Printf("Control Center error: %v", err)
				}
			}()
			log.Printf("Control Center started on :8080 (allowed_chat_ids=%d, external_url=%s)", len(allowedChatIDs), ccExternalURL)
		}
	} else {
		log.Println("CC_AUTH_TOKEN and ALLOWED_CHAT_IDS not set - Control Center disabled")
	}

	var offset int64
	client := &http.Client{Timeout: 35 * time.Second}

	// Telegram client for sending formatted messages (nil if TG disabled).
	var tg *tgclient.Client
	if telegramEnabled {
		tg = tgclient.NewClient(token)
		tg.HTTP = client
		tg.OnRateLimit = func(wait time.Duration) {
			eventLog.Log("telegram_rate_limit", map[string]any{
				"wait_seconds": wait.Seconds(),
			})
			log.Printf("[telegram] rate limited - waiting %v before retry", wait)
		}
	}

	// Auto-update checker (initialized here, scheduled via unified scheduler below).
	var uc *updater.Checker
	if cfg.AutoUpdateCheck {
		image := os.Getenv("ALF_IMAGE")
		if image == "" {
			image = "ghcr.io/alamparelli/alf"
		}
		notifyFn := func(current, latest string) {
			log.Printf("update available: %s → %s", current, latest)
			if cfg.AutoUpdateNotify && telegramEnabled && tg != nil {
				cid, _ := strconv.ParseInt(chatID, 10, 64)
				if cid != 0 {
					tg.SendHTML(cid, fmt.Sprintf("Update available: %s → %s\nRun <code>alf upgrade</code> on the host to update.", current, latest))
				}
			}
		}
		updateInterval := time.Duration(cfg.AutoUpdateCheckInterval) * time.Second
		if updateInterval <= 0 {
			updateInterval = 21600 * time.Second
		}
		uc = updater.New(image, version, updateInterval, notifyFn)
	}

	// --- Unified Scheduler ---
	parsedChatID, _ := strconv.ParseInt(chatID, 10, 64)
	schedLocation := resolveTimezone(cfg.Timezone)

	sched := scheduler.New(scheduler.Config{
		DataDir:      dataDir,
		ContextDir:   contextDir,
		ChatID:       parsedChatID,
		TG:           tg,
		CC:           &schedulerCCNotifier{store: chatStore},
		Provider:     &schedulerProvider{p: cliProvider},
		TierStore:    &schedulerTierStore{ts: tierStore},
		SkillStore:   &schedulerSkillStore{s: skillStore},
		Orchestrator: &schedulerOrchestrator{o: orch},
		ChatLogger:   &schedulerChatLogger{store: chatStore},
		EventLog:     eventLog,
		CronPath:     filepath.Join(configDir, "cron.json"),
		Location:     schedLocation,
	})

	// Register system jobs (replaces individual goroutine patterns).
	if git != nil && cfg.GitSweepInterval > 0 {
		sched.RegisterSystem("git-sweep", "Git Sweep",
			fmt.Sprintf("@every %dm", cfg.GitSweepInterval),
			func() error { return git.Commit("auto: periodic sweep") },
		)
	}
	if uc != nil {
		updateInterval := cfg.AutoUpdateCheckInterval
		if updateInterval <= 0 {
			updateInterval = 21600
		}
		sched.RegisterSystem("update-check", "Update Check",
			fmt.Sprintf("@every %ds", updateInterval),
			uc.CheckOnce,
		)
	}
	if memDB != nil {
		extractorTierResolver := func() string {
			tierName := firstFallbackTier(tierStore)
			for _, t := range tierStore.Current().Tiers {
				if t.Name == tierName {
					if m := router.ResolveModel(t.Model); m != "" {
						return m
					}
					return t.Model
				}
			}
			return ""
		}
		extractInterval := time.Duration(cfg.EffectiveMemoryExtractInterval()) * time.Minute
		extractTimeout := time.Duration(cfg.EffectiveMemoryExtractTimeout()) * time.Second
		extractBootDelay := time.Duration(cfg.EffectiveMemoryExtractBootDelay()) * time.Second
		extractMinMsg := cfg.EffectiveMemoryExtractMinMessages()

		extractor := memstore.NewExtractor(memDB, dataDir, contextDir, memstore.ExtractorConfig{
			Interval:    extractInterval,
			Timeout:     extractTimeout,
			BootDelay:   extractBootDelay,
			MinMessages: extractMinMsg,
		}, &extractorAdapter{prov: cliProvider, registry: registry}, extractorTierResolver)
		cronExpr := fmt.Sprintf("@every %dm", cfg.EffectiveMemoryExtractInterval())
		sched.RegisterSystem("mem-extract", "Memory Extraction", cronExpr, func() error {
			state := extractor.LoadState()
			return extractor.RunOnce(state.LastRun)
		})
		// Run initial extraction after a delay (avoids competing with other
		// startup processes for resources on constrained hosts).
		go func() {
			time.Sleep(extractBootDelay)
			state := extractor.LoadState()
			if time.Since(state.LastRun) >= extractInterval {
				log.Println("memstore: running initial extraction (overdue)")
				if err := extractor.RunOnce(state.LastRun); err != nil {
					log.Printf("memstore: initial extraction failed: %v", err)
				}
			}
		}()
	}

	// Daily schedule digest - runs at 08:00 local time.
	sched.RegisterSystem("sched-digest", "Schedule Digest", "0 0 8 * * *", sched.SendDailyDigest)

	schedAdapter.engine = sched
	if schedEventBroker != nil {
		sched.OnChange = schedEventBroker.Notify
	}

	if err := sched.Start(filepath.Join(contextDir, "scheduler.sock")); err != nil {
		log.Printf("warning: scheduler start failed: %v", err)
	}
	defer sched.Stop()

	// Seed security audit job if the skill exists (disabled by default).
	if _, ok := skillStore.Get("security-audit"); ok {
		if _, err := sched.EnsureManaged(
			"security-audit",
			"Security Audit",
			"0 0 9 * * *", // daily at 09:00
			firstFallbackTier(tierStore),
			"Execute the bash commands from the security-audit skill using the Bash tool to discover files, then use the Read tool to analyze each one. Output your security report.",
			"chat",
			[]string{"security-audit"},
			false, // disabled by default
		); err != nil {
			log.Printf("warning: failed to seed security-audit job: %v", err)
		}
	}

	// Seed health check job - two-phase: runs bash command deterministically,
	// only invokes LLM if error patterns are detected in the output.
	if _, ok := skillStore.Get("health-check"); ok {
		if _, err := sched.EnsureManagedFull(
			"health-check",
			"Health Check",
			"0 0 */2 * * *", // every 2 hours
			firstFallbackTier(tierStore),
			"Analyze the command output below. If no real issues, respond with empty string. Only output a concise report if real problems are found (under 500 chars). Format: severity, description, recommended action.",
			`echo "=== ERRORS ===" && tail -500 /home/alf/data/logs/daemon.log 2>/dev/null | grep -iE "error|panic|fatal|failed|timeout|killed" | tail -30; echo "=== EVENTS ===" && find /home/alf/data/logs/events/ -name "*.jsonl" -newer /tmp/.health-last 2>/dev/null -exec tail -50 {} \; | tail -100; touch /tmp/.health-last; echo "=== SCHEDULER ===" && find /home/alf/data/logs/scheduler/ -name "*.jsonl" -newer /tmp/.health-last-sched 2>/dev/null -exec tail -20 {} \;; touch /tmp/.health-last-sched; echo "=== DISK ===" && df -h /home/alf/data/ | tail -1; echo "=== PROCS ===" && ps aux | grep -c "[c]laude" || true`,
			"chat",
			[]string{"health-check"},
			false, // disabled by default
		); err != nil {
			log.Printf("warning: failed to seed health-check job: %v", err)
		}
	}

	// Seed heartbeat job - reads context/heartbeat.md, skips if empty body.
	if _, ok := skillStore.Get("heartbeat"); ok {
		hbTier, hbSchedule := scheduler.ParseHeartbeatMeta(contextDir)
		if hbTier == "" {
			hbTier = firstFallbackTier(tierStore)
		}
		if hbSchedule == "" {
			hbSchedule = "0 0 */6 * * *" // every 6 hours
		}
		if _, err := sched.EnsureManaged(
			"heartbeat",
			"Heartbeat",
			hbSchedule,
			hbTier,
			"__heartbeat__", // sentinel - executor reads context/heartbeat.md at runtime
			"chat",
			[]string{"heartbeat"},
			true, // enabled by default
		); err != nil {
			log.Printf("warning: failed to seed heartbeat job: %v", err)
		}
	}

	// --- App Service Supervisor ---
	appsSupervisor := supervisor.New(filepath.Join(dataDir, "apps"))
	appsSupervisor.Start()
	defer appsSupervisor.Stop()

	// When Telegram is not configured, run a CC-only event loop.
	if !telegramEnabled {
		log.Println("Running in Control Center-only mode (no Telegram polling)")
		for event := range reloadCh {
			switch event {
			case cc.ReloadConfig:
				if newCfg, err := configStore.Load(); err == nil {
					oldTZ := cfg.Timezone
					oldTiersFile := cfg.TiersFile
					cfg = newCfg
					chatSessions.SetTimeout(time.Duration(cfg.SessionTimeout) * time.Minute)
					if cfg.MaxSessions > 0 {
						sessions.SetMaxSessions(cfg.MaxSessions)
					}
					if cfg.Timezone != oldTZ {
						time.Local = resolveTimezone(cfg.Timezone)
					}
					// Switch tiers file if tiers_file changed.
					if cfg.TiersFile != oldTiersFile {
						newTiersPath := cc.TiersPathFromConfig(configDir, cfg)
						if err := tierStore.SetPath(newTiersPath); err != nil {
							log.Printf("ERROR: tiers reload from new path %q failed: %v - keeping previous tiers", newTiersPath, err)
						} else {
							log.Printf("config: tiers_file changed to %q", newTiersPath)
						}
					}
					applyDNS(cfg)
					registerBackends(registry, cfg, apiHistory, vaultMgr)
					log.Printf("config reloaded: log_level=%s session_timeout=%dm timezone=%s backends=%d", cfg.LogLevel, cfg.SessionTimeout, cfg.Timezone, len(cfg.Backends))
				}
				if git != nil {
					git.Commit("config updated via CC")
				}
			case cc.ReloadTiers:
				if err := tierStore.Reload(); err != nil {
					log.Printf("ERROR: tiers reload failed: %v", err)
				} else {
					log.Println("tiers reloaded")
				}
				routerBackend = tierStore.Current().RouterBackend
				isAPIR := routerBackend != "" && routerBackend != "cli"
				if isAPIR {
					newModel := tierStore.Current().RouterModel
					if newModel == "" {
						newModel = "anthropic/claude-haiku-4-5"
					}
					routerModel = newModel
				} else {
					newModel := router.ResolveModel(tierStore.Current().RouterModel)
					if newModel == "" {
						newModel = router.ResolveModel("haiku")
					}
					routerModel = newModel
				}
				if git != nil {
					git.Commit("tiers updated via CC")
				}
			case cc.ReloadSkills:
				if err := skillStore.Reload(); err != nil {
					log.Printf("skills reload error: %v", err)
				} else {
					log.Println("skills reloaded")
				}
				if git != nil {
					git.Commit("skills updated via CC")
				}
			case cc.ReloadAgents:
				if err := agentStore.Reload(); err != nil {
					log.Printf("agents reload error: %v", err)
				} else {
					teams := agentStore.All()
					log.Printf("agents reloaded (%d teams)", len(teams))
					if len(teams) > 0 {
						autoEnableAgentTier(tierStore)
					}
				}
			case cc.ReloadFirewall:
				if newFWCfg, err := fwStore.Load(); err == nil {
					fwProxy.Reload(newFWCfg)
				} else {
					log.Printf("firewall reload error: %v", err)
				}
			case cc.ReloadTools:
				log.Println("tools reloaded")
				if git != nil {
					git.Commit("tools updated via CC")
				}
			}
		}
	}

	for {
		// Check for reload events (non-blocking).
		select {
		case event := <-reloadCh:
			switch event {
			case cc.ReloadConfig:
				if newCfg, err := configStore.Load(); err == nil {
					oldTZ := cfg.Timezone
					oldTiersFile := cfg.TiersFile
					cfg = newCfg
					chatSessions.SetTimeout(time.Duration(cfg.SessionTimeout) * time.Minute)
					if cfg.MaxSessions > 0 {
						sessions.SetMaxSessions(cfg.MaxSessions)
					}
					if cfg.Timezone != oldTZ {
						time.Local = resolveTimezone(cfg.Timezone)
						log.Printf("config: timezone changed to %q (logs updated, scheduler needs restart)", cfg.Timezone)
					}
					// Switch tiers file if tiers_file changed.
					if cfg.TiersFile != oldTiersFile {
						newTiersPath := cc.TiersPathFromConfig(configDir, cfg)
						if err := tierStore.SetPath(newTiersPath); err != nil {
							log.Printf("ERROR: tiers reload from new path %q failed: %v - keeping previous tiers", newTiersPath, err)
						} else {
							log.Printf("config: tiers_file changed to %q", newTiersPath)
						}
					}
					// Re-register backends if config changed.
					applyDNS(cfg)
					registerBackends(registry, cfg, apiHistory, vaultMgr)
					log.Printf("config reloaded: log_level=%s session_timeout=%dm timezone=%s backends=%d", cfg.LogLevel, cfg.SessionTimeout, cfg.Timezone, len(cfg.Backends))
				}
				if git != nil {
					git.Commit("config updated via CC")
				}
			case cc.ReloadTiers:
				if err := tierStore.Reload(); err != nil {
					log.Printf("ERROR: tiers reload failed: %v - keeping previous config", err)
				} else {
					log.Println("tiers reloaded")
				}
				routerBackend = tierStore.Current().RouterBackend
				isAPIR := routerBackend != "" && routerBackend != "cli"
				if isAPIR {
					newModel := tierStore.Current().RouterModel
					if newModel == "" {
						newModel = "anthropic/claude-haiku-4-5"
					}
					routerModel = newModel
				} else {
					newModel := router.ResolveModel(tierStore.Current().RouterModel)
					if newModel == "" {
						newModel = router.ResolveModel("haiku")
					}
					routerModel = newModel
				}
				if git != nil {
					git.Commit("tiers updated via CC")
				}
			case cc.ReloadTools:
				log.Println("tools reloaded")
				if git != nil {
					git.Commit("tools updated via CC")
				}
			case cc.ReloadSkills:
				if err := skillStore.Reload(); err != nil {
					log.Printf("skills reload error: %v", err)
				} else {
					log.Println("skills reloaded")
				}
				if git != nil {
					git.Commit("skills updated via CC")
				}
			case cc.ReloadAgents:
				if err := agentStore.Reload(); err != nil {
					log.Printf("agents reload error: %v", err)
				} else {
					teams := agentStore.All()
					log.Printf("agents reloaded (%d teams)", len(teams))
					if len(teams) > 0 {
						autoEnableAgentTier(tierStore)
					}
				}
			case cc.ReloadFirewall:
				if newFWCfg, err := fwStore.Load(); err == nil {
					fwProxy.Reload(newFWCfg)
				} else {
					log.Printf("firewall reload error: %v", err)
				}
			}
		default:
		}

		updates, err := getUpdates(client, token, offset)
		if err != nil {
			log.Printf("getUpdates error: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Merge album messages (same media_group_id) into single updates.
		updates = mergeMediaGroups(updates)

		for _, u := range updates {
			offset = u.UpdateID + 1

			// Handle callback queries (inline keyboard button presses).
			if u.CallbackQuery != nil {
				handleCallbackQuery(tg, client, token, u.CallbackQuery, magic, ccExternalURL, allowedChatIDs)
				continue
			}

			// Handle emoji reactions.
			if u.MessageReaction != nil {
				mr := u.MessageReaction
				if len(allowedChatIDs) > 0 && !allowedChatIDs[mr.Chat.ID] {
					continue
				}
				if len(mr.NewReaction) == 0 {
					continue
				}
				emoji := mr.NewReaction[0].Emoji
				log.Printf("← reaction %s on msg %d", emoji, mr.MessageID)
				go handleReaction(tg, mr.Chat.ID, mr.MessageID, emoji, contextDir, dataDir, chatSessions, tierStore, alfMsgIDs, eventLog, cliProvider, memDB, convStore)
				continue
			}

			// Check for message with text or media
			if u.Message == nil {
				continue
			}

			// Authorize sender - reject anyone not in allowedChatIDs.
			if len(allowedChatIDs) > 0 && !allowedChatIDs[u.Message.Chat.ID] {
				log.Printf("unauthorized message from chat_id=%d user=%s - dropped", u.Message.Chat.ID, u.Message.From.Username)
				continue
			}

			// Check for message content: text, media, or voice
			hasText := u.Message.Text != ""
			hasVoice := u.Message.Voice != nil || u.Message.Audio != nil
			hasVideo := u.Message.Video != nil || u.Message.VideoNote != nil || u.Message.Animation != nil
			hasMedia := len(u.Message.Photo) > 0 || u.Message.Document != nil || hasVideo

			if !hasText && !hasMedia && !hasVoice {
				continue
			}

			log.Printf("← %s: %s", u.Message.From.Username, u.Message.Text)
			stats.RecordMessage()

			// Record user message in chat history buffer (for GIF/media context).
			userText := u.Message.Text
			if userText == "" {
				userText = u.Message.Caption
			}
			if userText != "" {
				chatHistory.Add(u.Message.Chat.ID, "user", userText)
			}

			// Write user message to unified conversation store.
			tgUserMsgID := conversation.NewMessageID()
			tgConvID := convStore.ConvID(conversation.ChannelTelegram)
			convStore.Append(conversation.Message{
				ID:        tgUserMsgID,
				ConvID:    tgConvID,
				Channel:   conversation.ChannelTelegram,
				Role:      "user",
				Blocks:    []conversation.ContentBlock{{Type: conversation.BlockText, Text: userText}},
				Timestamp: time.Now(),
			})

			// Extract reply context if this is a quoted reply.
			isReply := u.Message.ReplyToMessage != nil
			repliedToID := int64(0)
			if isReply {
				repliedToID = u.Message.ReplyToMessage.MessageID
			}

			// Note: hasText, hasMedia, hasVoice already determined above

			truncated := userText // includes caption for media messages
			if len(truncated) > 200 {
				truncated = truncated[:200]
			}
			eventLog.Log("message_in", map[string]any{
				"chat_id":       u.Message.Chat.ID,
				"username":      u.Message.From.Username,
				"text":          truncated,
				"is_reply":      isReply,
				"replied_to_id": repliedToID,
				"has_media":     hasMedia,
				"has_voice":     hasVoice,
				"session_id":    chatSessions.Get(u.Message.Chat.ID),
			})

			// Handle voice messages: transcribe and treat as text.
			if hasVoice && transcriber != nil && !transcriber.IsReady() {
				tg.SendHTML(u.Message.Chat.ID, "Voice model is still loading. Please try again in a moment.")
				continue
			}
			if hasVoice && transcriber != nil {
				fileID := ""
				duration := 0
				if u.Message.Voice != nil {
					fileID = u.Message.Voice.FileID
					duration = u.Message.Voice.Duration
				} else if u.Message.Audio != nil {
					fileID = u.Message.Audio.FileID
					duration = u.Message.Audio.Duration
				}

				if fileID != "" {
					log.Printf("voice: transcribing %s (%ds)", fileID, duration)
					tg.SendChatAction(u.Message.Chat.ID, "typing")

					result, err := transcriber.DownloadAndTranscribe(client, token, fileID)
					if err != nil {
						log.Printf("voice transcription failed: %v", err)
						tg.SendHTML(u.Message.Chat.ID, "Could not transcribe voice message.")
						eventLog.Log("voice_error", map[string]any{
							"chat_id":    u.Message.Chat.ID,
							"error":      err.Error(),
							"duration_s": duration,
						})
						continue
					}

					// Inject transcription as message text
					u.Message.Text = result.Text
					eventLog.Log("voice_in", map[string]any{
						"chat_id":    u.Message.Chat.ID,
						"username":   u.Message.From.Username,
						"transcript": result.Text,
						"duration_s": duration,
						"language":   result.Language,
					})
					log.Printf("voice: %q (%s)", result.Text, result.Language)
				}
			} else if hasVoice && transcriber == nil {
				tg.SendHTML(u.Message.Chat.ID, "Voice messages are not supported yet. Please send text.")
				continue
			}

			// Handle media messages: download and save for Claude to read.
			var mediaCleanup func()
			if hasMedia && !hasVoice {
				// Collect all files to download (supports albums via mergeMediaGroups).
				type fileRef struct {
					FileID   string
					FileName string
					Duration int
					IsAnim   bool
					IsVNote  bool
				}
				var files []fileRef

				if len(u.Message.Photo) > 0 {
					// Albums: each photo pair (sizes) in Photo slice - pick largest per photo.
					// After mergeMediaGroups, multiple photos from an album are concatenated.
					// Telegram sends multiple sizes per photo; pick the largest of each.
					// For a single photo: last element. For albums: every N-th element.
					// Simple approach: deduplicate by file_id prefix (sizes share prefix).
					seen := make(map[string]bool)
					for i := len(u.Message.Photo) - 1; i >= 0; i-- {
						p := u.Message.Photo[i]
						// Use first 20 chars of FileID as group key (sizes share prefix).
						key := p.FileID
						if len(key) > 20 {
							key = key[:20]
						}
						if !seen[key] {
							seen[key] = true
							files = append(files, fileRef{
								FileID:   p.FileID,
								FileName: fmt.Sprintf("photo_%d.jpg", len(files)+1),
							})
						}
					}
				} else if u.Message.Document != nil {
					fn := u.Message.Document.FileName
					if fn == "" {
						fn = "document"
					}
					files = append(files, fileRef{FileID: u.Message.Document.FileID, FileName: fn})
				} else if u.Message.Video != nil {
					fn := u.Message.Video.FileName
					if fn == "" {
						fn = "video.mp4"
					}
					files = append(files, fileRef{FileID: u.Message.Video.FileID, FileName: fn, Duration: u.Message.Video.Duration})
				} else if u.Message.Animation != nil {
					fn := u.Message.Animation.FileName
					if fn == "" {
						fn = "animation.gif"
					}
					files = append(files, fileRef{FileID: u.Message.Animation.FileID, FileName: fn, Duration: u.Message.Animation.Duration, IsAnim: true})
				} else if u.Message.VideoNote != nil {
					files = append(files, fileRef{FileID: u.Message.VideoNote.FileID, FileName: "videonote.mp4", Duration: u.Message.VideoNote.Duration, IsVNote: true})
				}
				// Add extra files from album merging.
				for _, ef := range u.Message.extraFiles {
					fn := ef.FileName
					if fn == "" {
						fn = fmt.Sprintf("file_%d", len(files)+1)
					}
					files = append(files, fileRef{FileID: ef.FileID, FileName: fn})
				}

				if len(files) > 0 {
					tg.SendChatAction(u.Message.Chat.ID, "typing")
					var cleanupPaths []string
					var allParts []string

					caption := u.Message.Caption
					if caption == "" {
						caption = u.Message.Text
					}

					for fi, f := range files {
						data, err := media.DownloadFile(client, token, f.FileID)
						if err != nil {
							log.Printf("media download failed (%s): %v", f.FileName, err)
							continue
						}

						mimeType := media.DetectMimeType(data, f.FileName)
						ext := extFromMime(mimeType, f.FileName)
						tmpFile, err := os.CreateTemp("", "alf-media-*"+ext)
						if err != nil {
							log.Printf("media temp file failed: %v", err)
							continue
						}
						tmpFile.Write(data)
						tmpFile.Close()
						os.Chmod(tmpFile.Name(), 0o644)
						tmpPath := tmpFile.Name()
						cleanupPaths = append(cleanupPaths, tmpPath)

						// Video/GIF/VideoNote handling.
						isVideoDoc := !hasVideo && media.IsVideoContent(mimeType, f.FileName)
						if hasVideo || isVideoDoc || f.IsAnim || f.IsVNote {
							mediaType := "VIDEO"
							if f.IsAnim {
								mediaType = "GIF/Animation"
							} else if f.IsVNote {
								mediaType = "VIDEO NOTE (round video)"
							}

							frames, err := media.ExtractFrames(tmpPath, 16)
							if err != nil {
								log.Printf("frame extraction failed: %v", err)
								allParts = append(allParts, fmt.Sprintf("[%s from Telegram, %ds - frame extraction failed]", mediaType, f.Duration))
							} else {
								cleanupPaths = append(cleanupPaths, frames...)

								var transcript string
								if !f.IsAnim && transcriber != nil && transcriber.IsReady() {
									audioPath, err := media.ExtractAudio(tmpPath)
									if err != nil {
										log.Printf("video audio extraction failed: %v", err)
									} else if audioPath != "" {
										cleanupPaths = append(cleanupPaths, audioPath)
										result, err := transcriber.Transcribe(audioPath)
										if err != nil {
											log.Printf("video audio transcription failed: %v", err)
										} else if result.Text != "" {
											transcript = result.Text
											log.Printf("video audio: %q (%s)", transcript, result.Language)
										}
									}
								}

								if len(frames) == 1 {
									allParts = append(allParts, fmt.Sprintf("[%s \"%s\" from Telegram (%ds) - contact sheet with key frames. Use Read tool to view: %s]", mediaType, f.FileName, f.Duration, frames[0]))
								} else {
									allParts = append(allParts, fmt.Sprintf("[%s \"%s\" from Telegram (%ds) - %d frames extracted. Use Read tool to view: %s]", mediaType, f.FileName, f.Duration, len(frames), strings.Join(frames, ", ")))
								}
								if transcript != "" {
									allParts = append(allParts, fmt.Sprintf("[Audio transcript: %s]", transcript))
								}
							}

							log.Printf("media: video %s (%ds) → frames", f.FileName, f.Duration)
						} else if media.IsImageContent(mimeType) {
							label := "PHOTO"
							if len(files) > 1 {
								label = fmt.Sprintf("PHOTO %d/%d", fi+1, len(files))
							}
							allParts = append(allParts, fmt.Sprintf("[%s from Telegram chat - use Read tool to view: %s]", label, tmpPath))
						} else if media.IsTextContent(mimeType) || mimeType == "application/pdf" {
							textContent := media.ExtractTextFromDocument(data, mimeType)
							allParts = append(allParts, fmt.Sprintf("[FILE from Telegram chat: %s]\nContent:\n%s", f.FileName, textContent))
						} else {
							allParts = append(allParts, fmt.Sprintf("[FILE from Telegram chat: %s - use Read tool to view: %s]", f.FileName, tmpPath))
						}

						log.Printf("media: saved %s (%s, %d bytes) → %s", f.FileName, mimeType, len(data), tmpPath)
						eventLog.Log("media_in", map[string]any{
							"chat_id":   u.Message.Chat.ID,
							"username":  u.Message.From.Username,
							"file_name": f.FileName,
							"mime_type": mimeType,
							"size":      len(data),
							"tmp_path":  tmpPath,
							"is_video":  hasVideo || f.IsAnim || f.IsVNote,
						})
					}

					// Add caption or contextual instruction.
					if caption != "" {
						allParts = append(allParts, caption)
					} else if u.Message.Animation != nil {
						// GIF reaction: inject recent conversation context.
						recent := chatHistory.Recent(u.Message.Chat.ID, 6)
						if len(recent) > 0 {
							var ctxLines []string
							ctxLines = append(ctxLines, "[Recent conversation for context:")
							for _, e := range recent {
								role := "User"
								if e.Role == "alf" {
									role = "Alf"
								}
								ctxLines = append(ctxLines, fmt.Sprintf("- %s: %s", role, e.Text))
							}
							ctxLines = append(ctxLines, "]")
							allParts = append(allParts, strings.Join(ctxLines, "\n"))
						}
						allParts = append(allParts, "The user sent this GIF as a reaction to the conversation. GIFs express emotions, humor, or reactions - don't describe the GIF literally. Instead, understand the feeling/mood it conveys and respond to that emotion naturally, matching the vibe. Keep it short.")
					} else if len(files) > 1 {
						allParts = append(allParts, fmt.Sprintf("The user sent %d files/photos together as an album. Analyze all of them and respond naturally.", len(files)))
					} else if hasVideo {
						allParts = append(allParts, "The user shared this video in chat. Describe what you see in the frames and the audio context. React naturally.")
					} else {
						allParts = append(allParts, "The user shared this in chat. React naturally as you would in a personal conversation - comment on what you see, the mood, the context.")
					}

					u.Message.Text = strings.Join(allParts, "\n")

					mediaCleanup = func() {
						// Delay cleanup by 5 minutes to keep audio files for debugging.
						go func(paths []string) {
							time.Sleep(5 * time.Minute)
							for _, p := range paths {
								os.Remove(p)
							}
						}(cleanupPaths)
					}
				}
			}

			// Command routing: handle /commands before passing to Claude.
			// For media messages, use original caption for command detection since
			// u.Message.Text has been replaced with [PHOTO...]\ncaption.
			var forcedTierName string
			cmdSource := u.Message.Text
			if hasMedia && u.Message.Caption != "" {
				cmdSource = u.Message.Caption
			}
			if strings.HasPrefix(cmdSource, "/") {
				if handleCommand(tg, u.Message, chatSessions, eventLog, magic, ccExternalURL, allowedChatIDs, contextDir, orch, convStore) {
					continue
				}
				// Check for force command: /<tier_name> <message>
				parts := strings.SplitN(cmdSource, " ", 2)
				cmdName := strings.TrimPrefix(parts[0], "/")
				for _, t := range tierStore.Current().Tiers {
					if t.ForceCommand && t.Name == cmdName {
						// Persist tier override for the session.
						chatSessions.SetForcedTier(u.Message.Chat.ID, t.Name)
						if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
							// Bare /<tier> — lock session only, no message to process.
							tg.SendHTML(u.Message.Chat.ID, fmt.Sprintf("⚡ Session locked to <b>%s</b>. Use /new to reset.", t.Name))
							forcedTierName = "_skip"
						} else {
							forcedTierName = t.Name
							tg.SendHTML(u.Message.Chat.ID, fmt.Sprintf("⚡ Session locked to <b>%s</b>. Use /new to reset.", t.Name))
							if hasMedia {
								// Strip command prefix from the built media text.
								u.Message.Text = strings.Replace(u.Message.Text, cmdSource, strings.TrimSpace(parts[1]), 1)
							} else {
								u.Message.Text = strings.TrimSpace(parts[1])
							}
						}
						break
					}
				}
				if forcedTierName == "_skip" {
					continue
				}
				// Unknown /commands fall through to Claude.
			}

			// Check for persistent tier override from a previous force command.
			if forcedTierName == "" {
				if ft := chatSessions.GetForcedTier(u.Message.Chat.ID); ft != "" {
					forcedTierName = ft
				}
			}

			chatID := u.Message.Chat.ID
			resumeID := chatSessions.Get(chatID)

			// Show typing indicator while classifying.
			tg.SendChatAction(chatID, "typing")
			routingAnim := newTypingIndicator(tg, chatID, "typing")

			// Build complete message content including media captions and reply context.
			msgWithReplyContext := buildMessageContent(u.Message)
			// Build a short version for the router (user text + brief quote hint, no full quoted text).
			routerMsg := buildRouterMessage(u.Message)

			// Pre-route memory recall: check long-term store BEFORE routing
			// so direct responses also have personal context.
			var preRecallBlock string
			recallBestDist := 2.0
			if memDB != nil {
				preRecallBlock, recallBestDist = autoRecall(memDB, u.Message.Text)
			}

			// Route message to appropriate tier.
			var tp tierParams
			var routeResult router.Result

			// Build conversation context for router (last 3 exchanges, cross-session).
			lastTier, msgCount := chatSessions.Context(chatID)
			recentCtx := conversation.BuildRouterContext(convStore.RecentAll(6), 3)

			// Force command bypasses routing entirely.
			if forcedTierName != "" {
				routingAnim.Stop()
				routeResult = router.Result{Tier: forcedTierName, Reason: "force_command"}
				log.Printf("→ force command → tier %q", forcedTierName)
			} else if hasMedia {
				// Media needs a tier with Read tool access.
				// If caption present, classify to pick the right tier then
				// ensure it can view images; otherwise cheapest with Read.
				if routerMsg != "" {
					routeResult = classifyMessageFull(routerMsg, tierStore.Current(), lastTier, msgCount, recentCtx)
					log.Printf("→ media+caption: router chose tier=%q reason=%q", routeResult.Tier, routeResult.Reason)

					needsUpgrade := false
					if routeResult.Tier == "" || routeResult.Response != "" {
						needsUpgrade = true
					} else {
						for _, t := range tierStore.Current().Tiers {
							if t.Name == routeResult.Tier && !tierHasRead(t) {
								needsUpgrade = true
								break
							}
						}
					}
					if needsUpgrade {
						upgraded := lowestMediaTier(tierStore.Current())
						log.Printf("→ media upgrade: %q → %q (needs Read tool)", routeResult.Tier, upgraded)
						routeResult = router.Result{Tier: upgraded, Reason: fmt.Sprintf("media-upgrade: %s→%s", routeResult.Tier, upgraded)}
					}
				} else {
					tierName := lowestMediaTier(tierStore.Current())
					routeResult = router.Result{Tier: tierName, Reason: "media bypass (no caption)"}
					log.Printf("→ media (no caption), bypassing router → tier %q", tierName)
				}
				routingAnim.Stop()
			} else {
				routeResult = classifyMessageFull(routerMsg, tierStore.Current(), lastTier, msgCount, recentCtx)
			}

			// Router answered directly - no second LLM call needed.
			if forcedTierName == "" && !hasMedia {
				routingAnim.Stop()
			}

			// Quote-reply upgrade: replies carry important context. If the router
			// gave a direct response, re-classify with full context.
			if isReply && forcedTierName == "" && routeResult.Response != "" && routeResult.Tier == "" {
				originalResult := routeResult
				replyHint := msgWithReplyContext + "\n[CONTEXT: This is a reply to a previous assistant message. Route to an appropriate tier - do not respond directly.]"
				reclassified := classifyMessageFull(replyHint, tierStore.Current(), lastTier, msgCount, recentCtx)
				if reclassified.Tier != "" {
					routeResult = reclassified
					routeResult.Reason = "reply-reclassify: " + reclassified.Reason
				} else {
					fallback := firstFallbackTier(tierStore)
					routeResult = router.Result{Tier: fallback, Reason: fmt.Sprintf("reply-fallback: %s→%s", originalResult.Tier, fallback)}
				}
				log.Printf("→ reply re-routed: %s → %s (%s)", originalResult.Tier, routeResult.Tier, routeResult.Reason)
			}

			// During onboarding, always force a capable conversational tier.
			if memory.OnboardingPrompt(contextDir) != "" {
				fallback := onboardingTier(tierStore)
				log.Printf("→ onboarding override: %q → tier %q", routeResult.Tier, fallback)
				routeResult = router.Result{Tier: fallback, Reason: "onboarding-override: " + fallback}
			}

			// If highly relevant memories were recalled (distance < 0.6), override
			// direct responses - the user is asking about something personal.
			if preRecallBlock != "" && recallBestDist < 0.6 && routeResult.Response != "" && routeResult.Tier == "" {
				log.Printf("→ memory override: direct response upgraded to tier (best_dist=%.2f)", recallBestDist)
				fallback := firstFallbackTier(tierStore)
				routeResult = router.Result{Tier: fallback, Reason: "memory-override: direct→" + fallback}
			}
			// Register trigger-matched skills in the session BEFORE tier override,
			// so the first message in a session can also benefit from skill tier requirements.
			if triggerMatched := skills.MatchTriggers(skillStore, u.Message.Text); len(triggerMatched) > 0 {
				triggerNames := make([]string, len(triggerMatched))
				for i, sk := range triggerMatched {
					triggerNames[i] = sk.Name
				}
				log.Printf("skills: trigger-matched %v", triggerNames)
				chatSessions.AddSkills(chatID, triggerNames)
			}

			// Skill tier override: if an active skill requires a specific tier,
			// force routing to that tier (overrides direct responses and lower tiers).
			if activeSkills := chatSessions.GetSkills(chatID); len(activeSkills) > 0 {
				if minTier := skills.ResolveMinTier(skillStore, activeSkills); minTier != "" {
					// Direct response → force to skill tier.
					if routeResult.Response != "" && routeResult.Tier == "" {
						routeResult = router.Result{Tier: minTier, Reason: "skill-tier: " + minTier}
						log.Printf("→ skill tier override: direct→%s", minTier)
					} else if routeResult.Tier != "" && routeResult.Tier != minTier {
						// Check if current tier has lower priority than required tier.
						currentPri, requiredPri := -1, -1
						for _, t := range tierStore.Current().Tiers {
							if t.Name == routeResult.Tier {
								currentPri = t.Priority
							}
							if t.Name == minTier {
								requiredPri = t.Priority
							}
						}
						if requiredPri >= 0 && (currentPri < requiredPri) {
							old := routeResult.Tier
							routeResult.Tier = minTier
							routeResult.Reason = fmt.Sprintf("skill-tier: %s→%s", old, minTier)
							log.Printf("→ skill tier override: %s→%s", old, minTier)
						}
					}
				}
			}

			if routeResult.Response != "" && routeResult.Tier == "" {
				log.Printf("→ router direct response")
				eventLog.Log("router_direct", map[string]any{
					"chat_id":          chatID,
					"reason":           routeResult.Reason,
					"project_context":  filepath.Join(".claude/projects", fmt.Sprintf("%d", chatID)),
				})
				chatSessions.TouchContext(chatID, "router")
				// Write router response to conversation store.
				convStore.Append(conversation.Message{
					ID:        conversation.NewMessageID(),
					ConvID:    tgConvID,
					Channel:   conversation.ChannelTelegram,
					Role:      "assistant",
					Blocks:    []conversation.ContentBlock{{Type: conversation.BlockText, Text: routeResult.Response}},
					Timestamp: time.Now(),
					Model:     "router",
					Tier:      "router",
				})
				// React to the user's message before sending the reply (more natural).
				maybeSpontaneousReact(tg, u.Message.Chat.ID, u.Message.MessageID, routeResult.React, contextDir)
				if mid, err := tg.SendMessageReturnID(chatID, routeResult.Response); err == nil && mid != 0 {
					alfMsgIDs.Add(mid)
					chatHistory.Add(chatID, "alf", routeResult.Response)
					log.Printf("tracking alf msg %d (buffer=%d)", mid, alfMsgIDs.Size())
					// Log outgoing message
					eventLog.Log("message_out", map[string]any{
						"chat_id":          chatID,
						"route":            "router_direct",
						"text":             routeResult.Response,
						"text_length":      len(routeResult.Response),
						"message_id":       mid,
						"project_context":  filepath.Join(".claude/projects", fmt.Sprintf("%d", chatID)),
					})
				}
				continue
			}

			// Validate selected tier is still enabled and routable.
			if routeResult.Tier != "" && forcedTierName == "" {
				valid := false
				for _, t := range tierStore.Current().Tiers {
					if t.Name == routeResult.Tier && t.Enabled && (t.Routable || t.ForceCommand) {
						valid = true
						break
					}
				}
				if !valid {
					fallback := firstFallbackTier(tierStore)
					log.Printf("→ tier %q not routable/enabled, falling back → %s", routeResult.Tier, fallback)
					routeResult = router.Result{Tier: fallback, Reason: fmt.Sprintf("tier-invalid: %s→%s", routeResult.Tier, fallback)}
				}
			}

			// Resolve tier to params.
			tp = resolveTierParams(routeResult.Tier, tierStore.Current(), dataDir, toolRegistry, registry)

			eventLog.Log("router_classify", map[string]any{
				"chat_id":          chatID,
				"tier":             routeResult.Tier,
				"reason":           routeResult.Reason,
				"model":            tp.Model,
				"project_context":  filepath.Join(".claude/projects", fmt.Sprintf("%d", chatID)),
			})

			// Agent dispatch: delegate to multi-agent coordinator (non-blocking).
			// The orchestrator brain is delegation-only - skip core/soul/toolbox
			// prompts which contain file-writing instructions for conversational mode.
			if routeResult.Tier == "agent" && len(agentStore.All()) > 0 {
				var orchSysPrompts []string
				if preRecallBlock != "" {
					orchSysPrompts = append(orchSysPrompts, preRecallBlock)
				}
				if catalog := skills.BuildCatalog(skillStore); catalog != "" {
					orchSysPrompts = append(orchSysPrompts, catalog)
				}
				// Match skills for sub-agent injection.
				var skillInjections []string
				if matched := skills.MatchTriggers(skillStore, msgWithReplyContext); len(matched) > 0 {
					names := make([]string, len(matched))
					for i, sk := range matched {
						names[i] = sk.Name
						if sk.Prompt != "" {
							skillInjections = append(skillInjections, sk.Prompt)
						}
					}
					log.Printf("[chat:%d] agent: matched skills %v (%d prompts)", chatID, names, len(skillInjections))
				}

				// Enrich agent with workspace awareness and chat history.
				orchSysPrompts = append(orchSysPrompts, memory.WorkspaceSummary(dataDir))
				if recent := chatHistory.Recent(chatID, 5); len(recent) > 0 {
					var histBuf strings.Builder
					histBuf.WriteString("=== [Recent conversation] ===\n")
					for _, e := range recent {
						if e.Role == "user" {
							histBuf.WriteString("User: " + e.Text + "\n")
						} else {
							histBuf.WriteString("Alf: " + e.Text + "\n")
						}
					}
					orchSysPrompts = append(orchSysPrompts, histBuf.String())
				}

				// Capture loop variables for the goroutine.
				orchChatID := chatID
				orchMsg := msgWithReplyContext
				orchMediaCleanup := mediaCleanup
				orchRC := agents.RunConfig{
					Model:                tp.Model,
					Backend:              tp.Backend,
					Effort:               tp.Effort,
					MaxTurns:             tp.MaxTurns,
					OrchestratorMaxTurns: tp.OrchestratorMaxTurns,
					Source:               "telegram",
					MaxIterations:        tp.MaxIterations,
					TimeoutMin:           tp.TimeoutMin,
					SkillPrompts:         skillInjections,
					MemoryContext:        memory.CollectAgentContext(contextDir),
				}

				go func() {
					// Typing indicator for agent orchestration.
					orchAnim := newTypingIndicator(tg, orchChatID, "choose_sticker")

					orchProgress := func(phase, detail string) {
						switch phase {
						case "thinking":
							orchAnim.SetAction("choose_sticker")
						case "planning", "agent", "agent_done":
							orchAnim.SetAction("upload_document")
						case "synthesizing":
							orchAnim.SetAction("typing")
						}
					}

					start := time.Now()
					orchResult, orchMeta, orchErr := orch.Run(context.Background(), orchMsg, orchSysPrompts, orchRC, orchProgress)
					duration := time.Since(start)

					orchAnim.Stop()

					if orchErr != nil {
						log.Printf("agent error: %v", orchErr)
						tg.SendHTML(orchChatID, fmt.Sprintf("Orchestrator error: %v", orchErr))
						eventLog.Log("agent_error", map[string]any{
							"chat_id":     orchChatID,
							"error":       orchErr.Error(),
							"iterations":  orchMeta.Iterations,
							"total_cost":  orchMeta.TotalCost,
							"duration_ms": duration.Milliseconds(),
						})
						return
					}

					orchSessShort := orchMeta.ID
				if len(orchSessShort) > 8 {
					orchSessShort = orchSessShort[:8]
				}
				log.Printf("→ agent %dms %d iterations $%.4f sid:%s", duration.Milliseconds(), orchMeta.Iterations, orchMeta.TotalCost, orchSessShort)

					eventLog.Log("agent_out", map[string]any{
						"chat_id":      orchChatID,
						"iterations":   orchMeta.Iterations,
						"total_cost":   orchMeta.TotalCost,
						"agent_calls":  len(orchMeta.AgentCalls),
						"duration_ms":  duration.Milliseconds(),
						"text":         orchResult,
						"text_length":  len(orchResult),
						"task_id":      orchMeta.ID,
					})

					if msgID, err := tg.SendMessageReturnID(orchChatID, orchResult); err == nil && msgID != 0 {
						alfMsgIDs.Add(msgID)
						chatHistory.Add(orchChatID, "alf", orchResult)
						convStore.Append(conversation.Message{
							ID:        conversation.NewMessageID(),
							ConvID:    tgConvID,
							Channel:   conversation.ChannelTelegram,
							Role:      "assistant",
							Blocks:    []conversation.ContentBlock{{Type: conversation.BlockText, Text: orchResult}},
							Timestamp: time.Now(),
							Model:     "agent",
							Tier:      "agent",
							CostUSD:   orchMeta.TotalCost,
						})
					}
					if orchMediaCleanup != nil {
						time.Sleep(10 * time.Minute)
						orchMediaCleanup()
					}
				}()
				continue
			}

			// Typing indicator during processing.
			statusAnim := newTypingIndicator(tg, chatID, "choose_sticker")

			lastPhase := ""
			rawOnProgress := func(event provider.StreamEvent) {
				if event.Type == lastPhase {
					return
				}
				lastPhase = event.Type
				switch event.Type {
				case "thinking":
					statusAnim.SetAction("choose_sticker")
				case "tool_use":
					statusAnim.SetAction("upload_document")
				case "text_delta":
					statusAnim.SetAction("typing")
				}
			}
			// Wrap with accumulator to capture content blocks.
			tgAcc := conversation.NewAccumulator()
			onProgress := tgAcc.OnProgress(rawOnProgress)

			// Build system prompts with backend/channel-aware filtering.
			isAPITier := tp.Backend != "" && tp.Backend != "cli"
			backend := "cli"
			if isAPITier {
				backend = "api"
			}
			tgCtxWeight := tp.EffectiveContextWeight()
			promptCfg := memory.PromptConfig{Backend: backend, Channel: "tg", Weight: tgCtxWeight}
			sysPromptTexts := memory.CollectPrompts(contextDir, promptCfg)
			// Inject per-tier system prompt first so it has high priority.
			if tp.SystemPrompt != "" {
				sysPromptTexts = append([]string{tp.SystemPrompt}, sysPromptTexts...)
			}
			// Inject onboarding prompt so Claude follows the onboarding instructions.
			onboarding := memory.OnboardingPrompt(contextDir)
			if onboarding != "" {
				sysPromptTexts = append(sysPromptTexts, onboarding)
			}
			// Inject pre-recalled memories (computed before routing).
			if preRecallBlock != "" {
				sysPromptTexts = append(sysPromptTexts, preRecallBlock)
			}
			// Inject skill catalog and skills (skip for light tiers).
			if tgCtxWeight != "light" {
				if catalog := skills.BuildCatalog(skillStore); catalog != "" {
					sysPromptTexts = append(sysPromptTexts, catalog)
				}
				if activeSkills := chatSessions.GetSkills(chatID); len(activeSkills) > 0 {
					if injection := skills.BuildInjectionByName(skillStore, activeSkills); injection != "" {
						log.Printf("skills: session-active %v", activeSkills)
						sysPromptTexts = append(sysPromptTexts, injection)
					}
				}
			}
			// Reaction instruction (skip for light tiers).
			if tgCtxWeight != "light" {
				sysPromptTexts = append(sysPromptTexts, fmt.Sprintf(memory.ReactionMD, mood.AllowedReactionList()))
			}

			// Documentation index - lets the model discover and read docs.
			if _, err := os.Stat(filepath.Join(dataDir, "llms.txt")); err == nil {
				sysPromptTexts = append(sysPromptTexts, "Documentation is available in ~/data/docs/. Read ~/data/llms.txt for the index. When you install packages, read the container-packages doc first.")
			}

			// Inject session/conversation ID so the LLM can provide it when asked.
			sysPromptTexts = append(sysPromptTexts, fmt.Sprintf("Current session ID: %s (channel: tg)", tgConvID))

			// Select provider based on tier backend.
			var tierProv provider.Provider = registry.ForBackend(tp.Backend)

			// Wrap API provider with agentic tool loop when tier has tools.
			if isAPITier && chatService.ToolRegistry != nil && chatService.ToolExecutor != nil && len(tp.Tools) > 0 {
				chatService.ToolRegistry.Rescan() // pick up new tool schemas created since startup
				if apiProv, ok := tierProv.(*provider.APIProvider); ok {
					schemas := chatService.ToolRegistry.ForToolsStrict(tp.Tools)
					if len(schemas) > 0 {
						tools := tooling.ToOpenAI(schemas)
						maxTurns := tp.MaxTurns
						if maxTurns <= 0 {
							maxTurns = 10
						}
						tierProv = provider.NewToolLoop(apiProv, &tgToolExecutorAdapter{exec: chatService.ToolExecutor}, tools, maxTurns)
						log.Printf("[chat:%d] tool loop enabled: %d tools, max_turns=%d", chatID, len(schemas), maxTurns)
						toolNames := make([]string, len(schemas))
						for i, s := range schemas {
							toolNames[i] = s.Name
						}
						sysPromptTexts = append([]string{memory.ToolInstruction(toolNames)}, sysPromptTexts...)
					}
				}
			}

			// Detect backend switch for context continuity.
			_, lastBackend, _ := chatSessions.ContextFull(chatID)
			backendChanged := lastBackend != "" && lastBackend != tp.Backend

			invokeParams := provider.Params{
				Model:         tp.Model,
				Tools:         tp.Tools,
				WriteCapable:  tp.WriteCapable,
				Effort:        tp.Effort,
				MaxTurns:      tp.MaxTurns,
				SystemPrompts: sysPromptTexts,
				ResumeID:      resumeID,
				DataDir:       dataDir,
			}
			if isAPITier {
				invokeParams.ResumeID = "" // API tiers use ConvMessages, not --resume
			}
			if backendChanged {
				log.Printf("[chat:%d] backend switch %s→%s, dropping resume", chatID, lastBackend, tp.Backend)
				invokeParams.ResumeID = ""
			}

			// Inject conversation context from unified store.
			tgConvMsgs := conversation.BuildContext(convStore.Recent(conversation.ChannelTelegram, 0), conversation.DefaultMaxMessages)
			if isAPITier || invokeParams.ResumeID == "" {
				if isAPITier {
					oaiMsgs := conversation.FlattenForOpenAI(tgConvMsgs)
					ctxMsgs := make([]provider.ContextMessage, len(oaiMsgs))
					for i, m := range oaiMsgs {
						cm := provider.ContextMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
						for _, tc := range m.ToolCalls {
							cm.ToolCalls = append(cm.ToolCalls, provider.ContextToolCall{
								ID:        tc.ID,
								Name:      tc.Name,
								Arguments: tc.Arguments,
							})
						}
						ctxMsgs[i] = cm
					}
					invokeParams.ConvMessages = ctxMsgs
				} else {
					if histPrompt := conversation.FormatAsSystemPrompt(tgConvMsgs, tgCtxWeight); histPrompt != "" {
						invokeParams.SystemPrompts = append(invokeParams.SystemPrompts, histPrompt)
					}
				}
			}

			// Signal server: per-invocation socket for react/status from Claude subprocess.
			sigSockPath := filepath.Join(dataDir, fmt.Sprintf("signal-%d.sock", u.Message.MessageID))
			sigServer := &signal.Server{TG: tg, ChatID: chatID, MessageID: u.Message.MessageID}
			var sigLn net.Listener
			if ln, err := sigServer.ListenUnix(sigSockPath); err != nil {
				log.Printf("signal: listen error: %v", err)
			} else {
				sigLn = ln
				go sigServer.Serve(sigLn)
				invokeParams.Env = append(invokeParams.Env, "ALF_SIGNAL_SOCK="+sigSockPath)
			}

			start := time.Now()
			result, err := tierProv.Invoke(context.Background(), msgWithReplyContext, invokeParams, onProgress)
			// Retry without resume if session failed (CLI only).
			if err != nil && resumeID != "" && !isAPITier {
				log.Printf("session %s failed (%v), starting fresh", resumeID, err)
				chatSessions.Archive(chatID)
				invokeParams.ResumeID = ""
				result, err = tierProv.Invoke(context.Background(), msgWithReplyContext, invokeParams, onProgress)
			}
			duration := time.Since(start)

			// Cleanup signal socket immediately (defer won't fire in this loop).
			if sigLn != nil {
				sigLn.Close()
				os.Remove(sigSockPath)
			}

			// Stop typing indicator.
			statusAnim.Stop()

			if err != nil {
				log.Printf("claude error: %v", err)
				reply := fmt.Sprintf("Error: %v", err)
				eventLog.Log("bot_error", map[string]any{
					"context": "askClaude",
					"error":   err.Error(),
					"chat_id": chatID,
				})
				tg.SendHTML(chatID, reply)
				continue
			}

			// Clear onboarding flag after first successful response.
			// Don't clear immediately - wait until next /new so system prompts
			// stay consistent within a resumed session.
			if onboarding != "" {
				onboarding = "" // prevent re-clearing on subsequent messages
			}

			// Store the session ID returned by Claude for future --resume.
			if result.SessionID != "" {
				isNew := resumeID == ""
				chatSessions.SetWithBackend(chatID, result.SessionID, routeResult.Tier, tp.Backend)
				if isNew {
					reason := "first"
					if resumeID == "" && len(chatSessions.Get(chatID)) > 0 {
						reason = "timeout"
					}
					eventLog.Log("session_new", map[string]any{
						"chat_id":    chatID,
						"session_id": result.SessionID,
						"reason":     reason,
					})
				}
			} else if isAPITier {
				// API tiers don't return session IDs - just track context.
				chatSessions.TouchContext(chatID, routeResult.Tier)
			}
			chatSessions.Touch(chatID)

			// Schedule temp media cleanup after a delay so follow-up messages
			// in the same session can still reference the file.
			if mediaCleanup != nil {
				cleanup := mediaCleanup
				go func() {
					time.Sleep(10 * time.Minute)
					cleanup()
				}()
			}

			// Extract inline reaction suggestion from Claude's response.
			suggestedEmoji, cleanText := extractReaction(result.Text)
			reply := cleanText

			// Detect Claude not logged in.
			lower := strings.ToLower(reply)
			if strings.Contains(lower, "not logged in") || strings.Contains(lower, "authenticate") || strings.Contains(lower, "login required") {
				reply = "Not logged in \u00b7 Please run /login on the host with: alf login"
			}

			// Compute cost from tokens for API backends.
			if result.CostUSD == 0 && result.InputTokens > 0 {
				if bc, ok := cfg.Backends[tp.Backend]; ok && (bc.InputPrice > 0 || bc.OutputPrice > 0) {
					result.CostUSD = float64(result.InputTokens)/1e6*bc.InputPrice +
						float64(result.OutputTokens)/1e6*bc.OutputPrice
				}
			}

			sessShort := result.SessionID
			if len(sessShort) > 8 {
				sessShort = sessShort[:8]
			}
			log.Printf("→ %s %dms %dt/%dt $%.4f sid:%s", result.Model, duration.Milliseconds(), result.InputTokens, result.OutputTokens, result.CostUSD, sessShort)

			eventLog.Log("message_out", map[string]any{
				"chat_id":          chatID,
				"model":            result.Model,
				"duration_ms":      duration.Milliseconds(),
				"cost_usd":         result.CostUSD,
				"text":             reply,
				"text_length":      len(reply),
				"session_id":       result.SessionID,
				"session_path":     filepath.Join(".claude/projects", fmt.Sprintf("%d", chatID), "sessions", result.SessionID+".json"),
				"tier":             routeResult.Tier,
				"project_context":  filepath.Join(".claude/projects", fmt.Sprintf("%d", chatID)),
			})

			// React to the user's message before sending the reply (more natural).
			maybeSpontaneousReact(tg, u.Message.Chat.ID, u.Message.MessageID, suggestedEmoji, contextDir)

			// Suppress internal fallback messages that aren't useful to the user.
			if reply == "Done (no text output)." {
				log.Printf("suppressing empty response for chat %d", chatID)
				continue
			}

			// Append footer with tier and active skills (if enabled in config).
			if cfg.ShowSkillFooter == nil || *cfg.ShowSkillFooter {
				var footerParts []string
				if routeResult.Tier != "" {
					footerParts = append(footerParts, "["+routeResult.Tier+"]")
				}
				if activeSkills := chatSessions.GetSkills(chatID); len(activeSkills) > 0 {
					footerParts = append(footerParts, strings.Join(activeSkills, ", "))
				}
				if len(footerParts) > 0 {
					reply += "\n\n\u2699\ufe0f " + strings.Join(footerParts, " · ")
				}
			}

			if msgID, err := tg.SendMessageReturnID(chatID, reply); err == nil && msgID != 0 {
				alfMsgIDs.Add(msgID)
				chatHistory.Add(chatID, "alf", reply)

				// Write assistant message to unified conversation store.
				var tgBlocks []conversation.ContentBlock
				if tgAcc != nil {
					tgBlocks = tgAcc.Blocks()
				}
				if len(tgBlocks) == 0 {
					tgBlocks = []conversation.ContentBlock{{Type: conversation.BlockText, Text: cleanText}}
				}
				convStore.Append(conversation.Message{
					ID:        conversation.NewMessageID(),
					ConvID:    tgConvID,
					Channel:   conversation.ChannelTelegram,
					Role:      "assistant",
					Blocks:    tgBlocks,
					Timestamp: time.Now(),
					Model:     result.Model,
					Tier:      routeResult.Tier,
					Backend:   tp.Backend,
					CostUSD:   result.CostUSD,
					SessionID: result.SessionID,
				})

				log.Printf("tracking alf msg %d (buffer=%d)", msgID, alfMsgIDs.Size())
				// Log sent message ID
				eventLog.Log("message_sent", map[string]any{
					"chat_id":         chatID,
					"message_id":      msgID,
					"session_id":      result.SessionID,
					"project_context": filepath.Join(".claude/projects", fmt.Sprintf("%d", chatID)),
				})
			}
		}
	}
}
