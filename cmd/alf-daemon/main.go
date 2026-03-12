package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alamparelli/alf/internal/agents"
	"github.com/alamparelli/alf/internal/conversation"
	"github.com/alamparelli/alf/internal/firewall"
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

	token := readSecret("TELEGRAM_BOT_TOKEN")
	chatID := readSecret("TELEGRAM_CHAT_ID")
	authToken := readSecret("CC_AUTH_TOKEN")

	// Set Claude OAuth token as env var if available (picked up by safeEnv for subprocesses).
	if oauthToken := readSecret("CLAUDE_OAUTH_TOKEN"); oauthToken != "" {
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

	// telegramEnabled is finalized after vault is available (see below).
	telegramEnabled := token != "" && chatID != ""

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

	// Parse allowed chat IDs for login authorization.
	// Default to TELEGRAM_CHAT_ID if ALLOWED_CHAT_IDS not explicitly set.
	var allowedChatIDs map[int64]bool
	if telegramEnabled {
		allowedRaw := readSecret("ALLOWED_CHAT_IDS")
		if allowedRaw == "" {
			allowedRaw = chatID
		}
		allowedChatIDs = parseAllowedChatIDs(allowedRaw)
	}

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
	// Directories where claude (uid 1001, gid 1000) needs write access.
	for _, sub := range []string{"agents", "apps"} {
		os.MkdirAll(filepath.Join(dataDir, sub), 0o775)
		os.Chmod(filepath.Join(dataDir, sub), 0o775)
		os.Chown(filepath.Join(dataDir, sub), 1000, 1000)
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

	// Fix directory permissions so the claude subprocess (uid 1001, gid 1000)
	// can read/write files created before the permission refactoring.
	fixDataPermissions(dataDir)
	lockDirReadOnly(configDir)
	lockDirReadOnly(skillsDir)
	// Fix HOME subdirectories that root may have written to (claude CLI
	// config, npm/pip local installs). Without this, claude -p hangs with
	// EACCES because it cannot write to .claude/ or .local/.
	// Use explicit uid:gid (alf=1001, alf-group=1000) because these dirs
	// may be owned by root, and fixDataPermissions infers from the dir owner.
	fixHomeDirPermissions(homeDir, 1001, 1000)

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
		log.Printf("warning: failed to load firewall config: %v — using defaults", err)
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
			log.Println("vault: started (locked — set vault_master_password or unlock via Control Center)")
		}
	}

	// Load Telegram config: vault is authoritative when unlocked.
	// If vault is unlocked and credentials are absent, do NOT fall back to Docker secrets
	// (the user may have deliberately removed them). Fallback sources are only used
	// when the vault is locked or not yet set up.
	vaultChecked := false
	if vaultMgr != nil && vaultMgr.AdminToken() != "" {
		vaultChecked = true
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
	if !vaultChecked && (token == "" || chatID == "") {
		// Vault not available — fall back to legacy sources.
		if tgCfg := readTelegramConfig(configDir); tgCfg != nil {
			if tgCfg.BotToken != "" && tgCfg.ChatID != "" {
				token = tgCfg.BotToken
				chatID = tgCfg.ChatID
				log.Println("Telegram config loaded from config.d/telegram.json")
			}
		}
	}
	// Migrate: if vault is unlocked and credentials came from Docker secrets, persist them.
	if token != "" && chatID != "" && vaultChecked {
		existing, _ := vaultMgr.GetSecret("telegram_bot_token")
		if existing == "" {
			if err := vaultMgr.SetSecret("telegram_bot_token", token); err == nil {
				vaultMgr.SetSecret("telegram_chat_id", chatID)
				log.Println("Telegram credentials migrated to vault")
			}
		}
	}

	telegramEnabled = token != "" && chatID != ""
	if !telegramEnabled {
		log.Println("Telegram not configured — running in Control Center-only mode")
	}

	// Load initial tiers config.
	tierStore := cc.NewFileTierStore(cc.TiersPath(configDir))
	if err := tierStore.Reload(); err != nil {
		log.Printf("ERROR: failed to load tiers: %v — using defaults (your tiers.json edits are IGNORED)", err)
	}

	// Load skill catalog: system → bundled copy → user (later overrides earlier).
	skillStore := skills.NewFileSkillStore(skillsDir, filepath.Join(dataDir, "skills.d"), filepath.Join(dataDir, "skills"))

	// Watch config files for changes and auto-reload.
	go watchConfigFiles(configDir, reloadCh)

	// Load agent team configurations.
	agentStore := agents.NewFileAgentStore(filepath.Join(dataDir, "agents", "teams"))

	// Auto-enable the agent tier when teams are configured.
	if teams := agentStore.All(); len(teams) > 0 {
		autoEnableAgentTier(tierStore)
	}

	// Set process-wide timezone from config so log timestamps are correct.
	time.Local = resolveTimezone(cfg.Timezone)

	// Bootstrap default memory files (soul.md, mood.md, index.md).
	contextDir := filepath.Join(dataDir, "context")
	memory.Bootstrap(contextDir)

	// Seed default heartbeat.md if missing.
	seedHeartbeatFile(contextDir)

	// Generate toolbox.md — explicit list of all available CLI tools.
	memory.GenerateToolbox(contextDir, dataDir)

	// Generate daily mood (overwrites mood.md if date changed).
	mood.GenerateDaily(contextDir)

	// Session store for Claude --resume support.
	sessionTimeout := time.Duration(cfg.SessionTimeout) * time.Minute
	if sessionTimeout <= 0 {
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

	// Voice transcriber (faster-whisper on amd64, whisper.cpp on arm64).
	transcriptScriptPath := "/opt/alf/transcribe.py"
	if p := os.Getenv("ALF_TRANSCRIBE_SCRIPT"); p != "" {
		transcriptScriptPath = p
	}
	whisperModel := "small"
	if m := os.Getenv("WHISPER_MODEL"); m != "" {
		whisperModel = m
	}
	var transcriber *voice.Transcriber
	if voice.IsAvailable(transcriptScriptPath) {
		var err error
		transcriber, err = voice.New(transcriptScriptPath, whisperModel, filepath.Join(dataDir, "models"), 120*time.Second)
		if err != nil {
			log.Printf("voice transcription disabled: %v", err)
		} else {
			go func() {
				if err := transcriber.Start(); err != nil {
					log.Printf("voice: failed to start: %v", err)
				}
			}()
		}
	} else {
		log.Println("voice transcription disabled (prerequisites not found)")
	}

	// ONNX embedding engine (Go native, no Python sidecar).
	modelDir := "/opt/alf/models/all-MiniLM-L6-v2"
	var memDB *memstore.Store
	if memstore.IsAvailable(modelDir) {
		embedder, err := memstore.NewEmbedder(modelDir)
		if err != nil {
			log.Printf("memstore: embedder disabled: %v", err)
		} else {
			if err := embedder.Start(); err != nil {
				log.Printf("memstore: embedder start failed: %v", err)
			} else {
				defer embedder.Stop()
			}

			memDB, err = memstore.New(filepath.Join(contextDir, "memory.db"), embedder)
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

	// Claude subprocess credential (run as claude user uid 1001, gid 1000/alf).
	claudeCred := &syscall.Credential{Uid: 1001, Gid: 1000}

	// Provider: spawn-per-call Claude CLI for responses.
	tiersTimeout := time.Duration(cfg.TiersTimeout) * time.Second // 0 → default 5m inside NewCLIProvider
	cliProvider := provider.NewCLIProvider(homeDir, dataDir, tiersTimeout, claudeCred)

	// API backends: config-driven registration.
	apiHistory := provider.NewHistory(dataDir, 100, sessionTimeout)
	registry := provider.NewRegistry(cliProvider)
	registerBackends(registry, cfg, apiHistory, vaultMgr)

	// Multi-agent coordinator.
	orch := agents.NewOrchestrator(cliProvider, agentStore, dataDir, router.ResolveModel)

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

	// classifyMessage uses the configured router backend for classification.
	classifyMessage := func(message string, tiers *cc.TiersConfig) router.Result {
		prompt := router.BuildClassifyPrompt(router.ClassifyInput{
			Message:    message,
			Tiers:      tiers,
			DataDir:    dataDir,
			ConfigDir:  configDir,
			AgentTeams: agentTeamsForRouter(),
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
		rr := classifyMessage(message, tierStore.Current())
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
	chatService.ConvStore = convStore
	chatService.ToolRegistry = tooling.NewRegistry(dataDir)
	chatService.ToolExecutor = &tooling.Executor{
		DataDir: dataDir,
		HomeDir: homeDir,
		Timeout: 30 * time.Second,
	}
	if memDB != nil {
		chatService.Recaller = &memStoreRecaller{store: memDB}
	}

	// Schedule adapter (engine set later after scheduler is created).
	schedAdapter := &ccScheduleAdapter{}
	var schedEventBroker *cc.ScheduleEventBroker

	// Start Control Center HTTP server.
	if authToken != "" || len(allowedChatIDs) > 0 {
		// On vault unlock, migrate Telegram credentials from Docker secrets into vault.
		onVaultUnlock := func() {
			if vaultMgr == nil || vaultMgr.AdminToken() == "" {
				return
			}
			existing, _ := vaultMgr.GetSecret("telegram_bot_token")
			if existing != "" {
				return // already in vault
			}
			if token == "" || chatID == "" {
				return // nothing to migrate
			}
			if err := vaultMgr.SetSecret("telegram_bot_token", token); err == nil {
				vaultMgr.SetSecret("telegram_chat_id", chatID)
				log.Println("Telegram credentials migrated to vault on first unlock")
			}
		}
		server, broker, err := cc.New(dataDir, configDir, skillsDir, stats, version, authToken, ccExternalURL, cfg, reloadCh, magic, sessions, chatService, memDB, cliProvider, orch, agentStore, schedAdapter, fwStore, fwProxy, vaultMgr, registry, onVaultUnlock)
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
		log.Println("CC_AUTH_TOKEN and ALLOWED_CHAT_IDS not set — Control Center disabled")
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
			log.Printf("[telegram] rate limited — waiting %v before retry", wait)
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
		extractor := memstore.NewExtractor(memDB, dataDir, contextDir, 3*time.Hour, tiersTimeout, &extractorAdapter{prov: cliProvider})
		sched.RegisterSystem("mem-extract", "Memory Extraction", "@every 3h", func() error {
			state := extractor.LoadState()
			return extractor.RunOnce(state.LastRun)
		})
		// Run initial extraction after a delay (avoids competing with other
		// startup processes for resources on constrained hosts).
		go func() {
			time.Sleep(10 * time.Minute)
			state := extractor.LoadState()
			if time.Since(state.LastRun) >= 3*time.Hour {
				log.Println("memstore: running initial extraction (overdue)")
				if err := extractor.RunOnce(state.LastRun); err != nil {
					log.Printf("memstore: initial extraction failed: %v", err)
				}
			}
		}()
	}

	// Daily schedule digest — runs at 08:00 local time.
	sched.RegisterSystem("sched-digest", "Schedule Digest", "0 0 8 * * *", sched.SendDailyDigest)

	schedAdapter.engine = sched
	if schedEventBroker != nil {
		sched.OnChange = schedEventBroker.Notify
	}

	if err := sched.Start(filepath.Join(contextDir, "scheduler.sock")); err != nil {
		log.Printf("warning: scheduler start failed: %v", err)
	}
	defer sched.Stop()

	// Seed security audit job if the skill exists (managed = protected from tool modifications).
	if _, ok := skillStore.Get("security-audit"); ok {
		if _, err := sched.EnsureManaged(
			"security-audit",
			"Security Audit",
			"0 0 9 * * *", // daily at 09:00
			firstFallbackTier(tierStore),
			"Execute the bash commands from the security-audit skill using the Bash tool to discover files, then use the Read tool to analyze each one. Output your security report.",
			"telegram",
			[]string{"security-audit"},
		); err != nil {
			log.Printf("warning: failed to seed security-audit job: %v", err)
		}
	}

	// Seed health check job — two-phase: runs bash command deterministically,
	// only invokes LLM if error patterns are detected in the output.
	if _, ok := skillStore.Get("health-check"); ok {
		if _, err := sched.EnsureManagedFull(
			"health-check",
			"Health Check",
			"0 0 */2 * * *", // every 2 hours
			firstFallbackTier(tierStore),
			"Analyze the command output below. If no real issues, respond with empty string. Only output a concise report if real problems are found (under 500 chars). Format: severity, description, recommended action.",
			`echo "=== ERRORS ===" && tail -500 /home/alf/data/logs/daemon.log 2>/dev/null | grep -iE "error|panic|fatal|failed|timeout|killed" | tail -30; echo "=== EVENTS ===" && find /home/alf/data/logs/events/ -name "*.jsonl" -newer /tmp/.health-last 2>/dev/null -exec tail -50 {} \; | tail -100; touch /tmp/.health-last; echo "=== SCHEDULER ===" && find /home/alf/data/logs/scheduler/ -name "*.jsonl" -newer /tmp/.health-last-sched 2>/dev/null -exec tail -20 {} \;; touch /tmp/.health-last-sched; echo "=== DISK ===" && df -h /home/alf/data/ | tail -1; echo "=== PROCS ===" && ps aux | grep -c "[c]laude" || true`,
			"telegram",
			[]string{"health-check"},
		); err != nil {
			log.Printf("warning: failed to seed health-check job: %v", err)
		}
	}

	// Seed heartbeat job — reads context/heartbeat.md, skips if empty body.
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
			"__heartbeat__", // sentinel — executor reads context/heartbeat.md at runtime
			"telegram",
			[]string{"heartbeat"},
		); err != nil {
			log.Printf("warning: failed to seed heartbeat job: %v", err)
		}
	}

	// When Telegram is not configured, run a CC-only event loop.
	if !telegramEnabled {
		log.Println("Running in Control Center-only mode (no Telegram polling)")
		for event := range reloadCh {
			switch event {
			case cc.ReloadConfig:
				if newCfg, err := configStore.Load(); err == nil {
					oldTZ := cfg.Timezone
					cfg = newCfg
					if cfg.SessionTimeout > 0 {
						chatSessions.SetTimeout(time.Duration(cfg.SessionTimeout) * time.Minute)
					}
					if cfg.MaxSessions > 0 {
						sessions.SetMaxSessions(cfg.MaxSessions)
					}
					if cfg.Timezone != oldTZ {
						time.Local = resolveTimezone(cfg.Timezone)
					}
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
					cfg = newCfg
					if cfg.SessionTimeout > 0 {
						chatSessions.SetTimeout(time.Duration(cfg.SessionTimeout) * time.Minute)
					}
					if cfg.MaxSessions > 0 {
						sessions.SetMaxSessions(cfg.MaxSessions)
					}
					if cfg.Timezone != oldTZ {
						time.Local = resolveTimezone(cfg.Timezone)
						log.Printf("config: timezone changed to %q (logs updated, scheduler needs restart)", cfg.Timezone)
					}
					// Re-register backends if config changed.
					registerBackends(registry, cfg, apiHistory, vaultMgr)
					log.Printf("config reloaded: log_level=%s session_timeout=%dm timezone=%s backends=%d", cfg.LogLevel, cfg.SessionTimeout, cfg.Timezone, len(cfg.Backends))
				}
				if git != nil {
					git.Commit("config updated via CC")
				}
			case cc.ReloadTiers:
				if err := tierStore.Reload(); err != nil {
					log.Printf("ERROR: tiers reload failed: %v — keeping previous config", err)
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
				go handleReaction(tg, mr.Chat.ID, mr.MessageID, emoji, contextDir, dataDir, chatSessions, tierStore, alfMsgIDs, eventLog, cliProvider)
				continue
			}

			// Check for message with text or media
			if u.Message == nil {
				continue
			}

			// Authorize sender — reject anyone not in allowedChatIDs.
			if len(allowedChatIDs) > 0 && !allowedChatIDs[u.Message.Chat.ID] {
				log.Printf("unauthorized message from chat_id=%d user=%s — dropped", u.Message.Chat.ID, u.Message.From.Username)
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
					// Albums: each photo pair (sizes) in Photo slice — pick largest per photo.
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
								allParts = append(allParts, fmt.Sprintf("[%s from Telegram, %ds — frame extraction failed]", mediaType, f.Duration))
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
									allParts = append(allParts, fmt.Sprintf("[%s \"%s\" from Telegram (%ds) — contact sheet with key frames. Use Read tool to view: %s]", mediaType, f.FileName, f.Duration, frames[0]))
								} else {
									allParts = append(allParts, fmt.Sprintf("[%s \"%s\" from Telegram (%ds) — %d frames extracted. Use Read tool to view: %s]", mediaType, f.FileName, f.Duration, len(frames), strings.Join(frames, ", ")))
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
							allParts = append(allParts, fmt.Sprintf("[%s from Telegram chat — use Read tool to view: %s]", label, tmpPath))
						} else if media.IsTextContent(mimeType) || mimeType == "application/pdf" {
							textContent := media.ExtractTextFromDocument(data, mimeType)
							allParts = append(allParts, fmt.Sprintf("[FILE from Telegram chat: %s]\nContent:\n%s", f.FileName, textContent))
						} else {
							allParts = append(allParts, fmt.Sprintf("[FILE from Telegram chat: %s — use Read tool to view: %s]", f.FileName, tmpPath))
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
						allParts = append(allParts, "The user sent this GIF as a reaction to the conversation. GIFs express emotions, humor, or reactions — don't describe the GIF literally. Instead, understand the feeling/mood it conveys and respond to that emotion naturally, matching the vibe. Keep it short.")
					} else if len(files) > 1 {
						allParts = append(allParts, fmt.Sprintf("The user sent %d files/photos together as an album. Analyze all of them and respond naturally.", len(files)))
					} else if hasVideo {
						allParts = append(allParts, "The user shared this video in chat. Describe what you see in the frames and the audio context. React naturally.")
					} else {
						allParts = append(allParts, "The user shared this in chat. React naturally as you would in a personal conversation — comment on what you see, the mood, the context.")
					}

					u.Message.Text = strings.Join(allParts, "\n")

					mediaCleanup = func() {
						for _, p := range cleanupPaths {
							os.Remove(p)
						}
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
						if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
							tg.SendHTML(u.Message.Chat.ID, fmt.Sprintf("Usage: <code>/%s &lt;message&gt;</code>", t.Name))
							forcedTierName = "_skip" // signal to skip this update
						} else {
							forcedTierName = t.Name
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
					routeResult = classifyMessage(routerMsg, tierStore.Current())
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
				routeResult = classifyMessage(routerMsg, tierStore.Current())
			}

			// Router answered directly — no second LLM call needed.
			if forcedTierName == "" && !hasMedia {
				routingAnim.Stop()
			}

			// Quote-reply upgrade: replies carry important context. If the router
			// gave a direct response, re-classify with full context.
			if isReply && forcedTierName == "" && routeResult.Response != "" && routeResult.Tier == "" {
				originalResult := routeResult
				replyHint := msgWithReplyContext + "\n[CONTEXT: This is a reply to a previous assistant message. Route to an appropriate tier — do not respond directly.]"
				reclassified := classifyMessage(replyHint, tierStore.Current())
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
			// direct responses — the user is asking about something personal.
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
			tp = resolveTierParams(routeResult.Tier, tierStore.Current(), dataDir)

			eventLog.Log("router_classify", map[string]any{
				"chat_id":          chatID,
				"tier":             routeResult.Tier,
				"reason":           routeResult.Reason,
				"model":            tp.Model,
				"project_context":  filepath.Join(".claude/projects", fmt.Sprintf("%d", chatID)),
			})

			// Agent dispatch: delegate to multi-agent coordinator (non-blocking).
			// The orchestrator brain is delegation-only — skip core/soul/toolbox
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
					Effort:               tp.Effort,
					MaxTurns:             tp.MaxTurns,
					OrchestratorMaxTurns: tp.OrchestratorMaxTurns,
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

			// Build system prompts (context files + reaction instruction).
			sysPrompts := memory.CollectPrompts(contextDir)
			var sysPromptTexts []string
			// Inject per-tier system prompt first so it has high priority.
			if tp.SystemPrompt != "" {
				sysPromptTexts = append(sysPromptTexts, tp.SystemPrompt)
			}
			// Inject onboarding prompt FIRST so it becomes the primary --system-prompt.
			onboarding := memory.OnboardingPrompt(contextDir)
			if onboarding != "" {
				sysPromptTexts = append(sysPromptTexts, onboarding)
			}
			for i := 0; i < len(sysPrompts)-1; i += 2 {
				if sysPrompts[i] == "--append-system-prompt" {
					sysPromptTexts = append(sysPromptTexts, sysPrompts[i+1])
				}
			}
			// Inject pre-recalled memories (computed before routing).
			if preRecallBlock != "" {
				sysPromptTexts = append(sysPromptTexts, preRecallBlock)
			}
			// Inject skill catalog so the model knows available skills.
			if catalog := skills.BuildCatalog(skillStore); catalog != "" {
				sysPromptTexts = append(sysPromptTexts, catalog)
			}
			// Inject all session-active skills (trigger-matched earlier + persisted from previous messages).
			if activeSkills := chatSessions.GetSkills(chatID); len(activeSkills) > 0 {
				if injection := skills.BuildInjectionByName(skillStore, activeSkills); injection != "" {
					log.Printf("skills: session-active %v", activeSkills)
					sysPromptTexts = append(sysPromptTexts, injection)
				}
			}
			sysPromptTexts = append(sysPromptTexts, fmt.Sprintf(memory.ReactionMD, mood.AllowedReactionList()))

			// Documentation index — lets the model discover and read docs.
			if _, err := os.Stat(filepath.Join(dataDir, "llms.txt")); err == nil {
				sysPromptTexts = append(sysPromptTexts, "Documentation is available in ~/data/docs/. Read ~/data/llms.txt for the index. When you install packages, read the container-packages doc first.")
			}

			// Inject session/conversation ID so the LLM can provide it when asked.
			sysPromptTexts = append(sysPromptTexts, fmt.Sprintf("Current session ID: %s (channel: tg)", tgConvID))

			// Select provider based on tier backend.
			var tierProv provider.Provider = registry.ForBackend(tp.Backend)
			isAPITier := tp.Backend != "" && tp.Backend != "cli"

			// Wrap API provider with agentic tool loop when tier has tools.
			if isAPITier && chatService.ToolRegistry != nil && chatService.ToolExecutor != nil && len(tp.Tools) > 0 {
				if apiProv, ok := tierProv.(*provider.APIProvider); ok {
					schemas := chatService.ToolRegistry.ForTools(tp.Tools)
					if len(schemas) > 0 {
						tools := tooling.ToOpenAI(schemas)
						maxTurns := tp.MaxTurns
						if maxTurns <= 0 {
							maxTurns = 10
						}
						tierProv = provider.NewToolLoop(apiProv, &tgToolExecutorAdapter{exec: chatService.ToolExecutor}, tools, maxTurns)
						log.Printf("[chat:%d] tool loop enabled: %d tools, max_turns=%d", chatID, len(schemas), maxTurns)
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
					flat := conversation.FlattenForAPI(tgConvMsgs)
					ctxMsgs := make([]provider.ContextMessage, len(flat))
					for i, m := range flat {
						ctxMsgs[i] = provider.ContextMessage{Role: m.Role, Content: m.Content}
					}
					invokeParams.ConvMessages = ctxMsgs
				} else {
					if histPrompt := conversation.FormatAsSystemPrompt(tgConvMsgs); histPrompt != "" {
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
			// Don't clear immediately — wait until next /new so system prompts
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
				// API tiers don't return session IDs — just track context.
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

			sessShort := result.SessionID
			if len(sessShort) > 8 {
				sessShort = sessShort[:8]
			}
			log.Printf("→ %s %dms %dt $%.4f sid:%s", result.Model, duration.Milliseconds(), result.NumTurns, result.CostUSD, sessShort)

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

// reactionSystemPromptTmpl references the centralized prompt in memory/reaction.md.

// tierParams holds per-tier Claude CLI arguments.
type tierParams struct {
	Model                string   // full model name, e.g. "claude-sonnet-4-5"
	Tools                []string // nil = omit flag
	WriteCapable         bool     // if true, grants full tool access; if false, restricts to Tools whitelist
	Effort               string   // "" = omit flag
	MaxTurns             int      // 0 = omit flag (use Claude default)
	OrchestratorMaxTurns int      // turns per orchestrator brain call (0 = default 3)
	MaxIterations        int      // max agent iterations (0 = default)
	TimeoutMin           int      // global timeout in minutes (0 = default)
	Backend              string   // "cli" (default), or registered backend name
	SystemPrompt         string   // extra system prompt for this tier
}

// vaultPassword reads the master password from Docker secret first,
// then falls back to the persisted password file in the vault data directory
// (written by CC unlock handler).
func vaultPassword(mgr *vault.Manager) string {
	if pw := readSecret("VAULT_MASTER_PASSWORD"); pw != "" {
		return pw
	}
	if mgr != nil {
		data, err := os.ReadFile(mgr.PasswordFile())
		if err == nil {
			if pw := strings.TrimSpace(string(data)); pw != "" {
				return pw
			}
		}
	}
	return ""
}

func readSecret(envVar string) string {
	if path := os.Getenv(envVar + "_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return strings.TrimSpace(os.Getenv(envVar))
}

// telegramJSONConfig mirrors controlcenter.TelegramConfig for reading.
type telegramJSONConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// readTelegramConfig reads Telegram settings from config.d/telegram.json (set via CC).
func readTelegramConfig(configDir string) *telegramJSONConfig {
	data, err := os.ReadFile(filepath.Join(configDir, "telegram.json"))
	if err != nil {
		return nil
	}
	var cfg telegramJSONConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}

// registerBackends registers API backends from config.json into the registry.
// Backward compat: if no backends in config, check for OPENROUTER_API_KEY secret.
func registerBackends(registry *provider.Registry, cfg *cc.Config, apiHistory *provider.History, vaultMgr *vault.Manager) {
	if len(cfg.Backends) > 0 {
		for name, bcfg := range cfg.Backends {
			apiKey := resolveBackendAPIKey(name, bcfg, vaultMgr)
			if bcfg.Auth != "none" && apiKey == "" {
				log.Printf("backend %s: skipped (no API key available)", name)
				continue
			}
			auth := bcfg.Auth
			if auth == "" {
				auth = "bearer"
			}
			prov := provider.NewAPIProviderFromConfig(provider.APIProviderConfig{
				Name:         name,
				BaseURL:      bcfg.BaseURL,
				APIKey:       apiKey,
				Headers:      bcfg.Headers,
				DefaultModel: bcfg.DefaultModel,
				MaxTokens:    bcfg.MaxTokens,
				Auth:         auth,
			}, apiHistory)
			registry.Register(name, prov)
		}
	} else {
		// Backward compat: check for legacy OPENROUTER_API_KEY secret.
		if orAPIKey := readSecret("OPENROUTER_API_KEY"); orAPIKey != "" {
			prov := provider.NewAPIProvider(orAPIKey, apiHistory)
			registry.Register("openrouter", prov)
			log.Println("OpenRouter API provider enabled (legacy secret)")
		} else {
			log.Println("No API backends configured")
		}
	}
	// Update AllowedBackends for tier validation.
	cc.SetAllowedBackends(registry.BackendNames())
}

// resolveBackendAPIKey resolves the API key for a backend.
// Priority: vault_service → Docker secret (BACKENDNAME_API_KEY) → empty.
func resolveBackendAPIKey(name string, bcfg cc.BackendConfig, vaultMgr *vault.Manager) string {
	if bcfg.Auth == "none" {
		return ""
	}
	// Try vault first if vault_service is specified.
	if bcfg.VaultService != "" && vaultMgr != nil {
		// Vault proxy doesn't expose raw keys — the proxy approach means
		// requests go through vault. For now, fall through to Docker secret.
		// Future: vault-proxy integration for direct API proxying.
	}
	// Fall back to Docker secret: <UPPERCASE_NAME>_API_KEY
	secretName := strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_API_KEY"
	if key := readSecret(secretName); key != "" {
		return key
	}
	return ""
}

type Update struct {
	UpdateID        int64                   `json:"update_id"`
	Message         *Message                `json:"message"`
	CallbackQuery   *CallbackQuery          `json:"callback_query"`
	MessageReaction *MessageReactionUpdated `json:"message_reaction"`
}

type Message struct {
	MessageID       int64      `json:"message_id"`
	Chat            Chat       `json:"chat"`
	From            User       `json:"from"`
	Text            string     `json:"text"`
	ReplyToMessage  *Message   `json:"reply_to_message"`
	Photo           []*Photo   `json:"photo"`
	Document        *Document  `json:"document"`
	Video           *Video     `json:"video"`
	Animation       *Animation `json:"animation"`
	Audio           *Audio     `json:"audio"`
	Voice           *Voice     `json:"voice"`
	VideoNote       *VideoNote `json:"video_note"`
	Caption         string     `json:"caption"`
	MediaGroupID    string     `json:"media_group_id"`
	extraFiles      []mediaFile // populated by mergeMediaGroups for multi-file albums
}

type mediaFile struct {
	FileID   string
	FileName string
}

type Photo struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type Video struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"`
}

type Audio struct {
	FileID   string `json:"file_id"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"`
}

type Voice struct {
	FileID   string `json:"file_id"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"`
}

type VideoNote struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"`
}

type Animation struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"`
}

type MessageReactionUpdated struct {
	Chat        Chat           `json:"chat"`
	MessageID   int64          `json:"message_id"`
	User        *User          `json:"user"`
	NewReaction []ReactionType `json:"new_reaction"`
}

type ReactionType struct {
	Type  string `json:"type"`
	Emoji string `json:"emoji"`
}

type CallbackQuery struct {
	ID   string  `json:"id"`
	From User    `json:"from"`
	Data string  `json:"data"`
	Message *CBMessage `json:"message"`
}

type CBMessage struct {
	Chat Chat `json:"chat"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

func getUpdates(client *http.Client, token string, offset int64) ([]Update, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30&allowed_updates=%s", token, offset, `["message","callback_query","message_reaction"]`)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	const maxUpdatesBody = 10 * 1024 * 1024 // 10MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpdatesBody))
	if err != nil {
		return nil, fmt.Errorf("read getUpdates body: %w", err)
	}

	var result struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, fmt.Errorf("telegram API error: %s", string(body))
	}
	return result.Result, nil
}

// extractReplyContext extracts the full quoted message text from a reply.
func extractReplyContext(msg *Message) string {
	if msg == nil || msg.ReplyToMessage == nil {
		return ""
	}
	return msg.ReplyToMessage.Text
}

// prependReplyContext adds quoted message context to the user's message.
func prependReplyContext(msg *Message) string {
	quoted := extractReplyContext(msg)
	if quoted == "" {
		return msg.Text
	}
	return fmt.Sprintf("[The user is replying to this previous message:\n---\n%s\n---\n]\n%s", quoted, msg.Text)
}

// buildMessageContent builds the complete message content including media captions
func buildMessageContent(msg *Message) string {
	content := msg.Text

	// Include caption for photo/document messages
	if msg.Caption != "" {
		if content != "" {
			content = msg.Caption + "\n" + content
		} else {
			content = msg.Caption
		}
	}

	// Quote-reply without text: provide a meaningful prompt so Claude responds to the quoted content.
	if content == "" && msg.ReplyToMessage != nil {
		quoted := extractReplyContext(msg)
		if quoted != "" {
			return fmt.Sprintf("[The user is replying to this previous message:\n---\n%s\n---\n]\nThe user quoted this message without adding text. Respond to the quoted content.", quoted)
		}
	}

	// Apply reply context if present
	return prependReplyContext(&Message{
		Text:           content,
		ReplyToMessage: msg.ReplyToMessage,
	})
}

// buildRouterMessage builds a short message for the router classifier.
// Includes the user's text with a brief quote hint (not the full quoted text)
// to keep the router prompt small and focused on classification.
func buildRouterMessage(msg *Message) string {
	text := msg.Text
	if msg.Caption != "" {
		if text != "" {
			text = msg.Caption + "\n" + text
		} else {
			text = msg.Caption
		}
	}
	if msg.ReplyToMessage != nil {
		quoted := msg.ReplyToMessage.Text
		if len(quoted) > 100 {
			quoted = quoted[:100] + "..."
		}
		if text == "" {
			return fmt.Sprintf("[Replying to: \"%s\"] (no additional text)", quoted)
		}
		return fmt.Sprintf("[Replying to: \"%s\"]\n%s", quoted, text)
	}
	return text
}

// extFromMime returns a file extension for a MIME type, falling back to the original filename extension.
func extFromMime(mimeType, fileName string) string {
	mimeToExt := map[string]string{
		"image/jpeg":      ".jpg",
		"image/png":       ".png",
		"image/gif":       ".gif",
		"image/webp":      ".webp",
		"application/pdf": ".pdf",
		"video/mp4":       ".mp4",
		"video/quicktime": ".mov",
		"video/webm":      ".webm",
		"video/x-matroska": ".mkv",
	}
	if ext, ok := mimeToExt[mimeType]; ok {
		return ext
	}
	if ext := filepath.Ext(fileName); ext != "" {
		return ext
	}
	return ""
}

// hasMedia checks if message contains any media attachments
func hasMedia(msg *Message) bool {
	return len(msg.Photo) > 0 || msg.Document != nil || msg.Video != nil ||
		msg.Animation != nil || msg.Audio != nil || msg.Voice != nil || msg.VideoNote != nil
}

// handleCommand processes known /commands. Returns true if handled.
func handleCommand(tg *tgclient.Client, msg *Message, chatSessions *session.Store, eventLog *eventlog.Logger, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool, contextDir string, orch *agents.Orchestrator, convStore *conversation.Store) bool {
	cmd := strings.SplitN(msg.Text, " ", 2)[0]
	switch cmd {
	case "/login":
		handleLogin(tg, msg, magic, ccExternalURL, allowedChatIDs)
		return true
	case "/new":
		old := chatSessions.Archive(msg.Chat.ID)
		convStore.NewConversation(conversation.ChannelTelegram)
		memory.ClearOnboarding(contextDir)
		reply := "New session started."
		if old != "" {
			reply = "Previous session archived. New session started."
			eventLog.Log("session_archived", map[string]any{
				"chat_id":        msg.Chat.ID,
				"old_session_id": old,
			})
		}
		tg.SendHTML(msg.Chat.ID, reply)
		return true
	case "/start":
		memory.SetOnboarding(contextDir)
		chatSessions.Archive(msg.Chat.ID) // fresh session so onboarding prompt takes effect
		convStore.NewConversation(conversation.ChannelTelegram)
		// Auto-trigger onboarding conversation — fall through to normal message processing.
		msg.Text = "hello"
		return false
	case "/restart":
		if !allowedChatIDs[msg.Chat.ID] {
			return true
		}
		tg.SendHTML(msg.Chat.ID, "Restarting ALF daemon...")
		log.Println("restart requested via /restart command")
		go func() {
			time.Sleep(500 * time.Millisecond)
			os.Exit(0)
		}()
		return true
	case "/cancel":
		if !allowedChatIDs[msg.Chat.ID] {
			return true
		}
		running := orch.Running()
		if len(running) == 0 {
			tg.SendHTML(msg.Chat.ID, "No agent jobs running.")
			return true
		}
		n := orch.CancelAll()
		tg.SendHTML(msg.Chat.ID, fmt.Sprintf("Cancelled %d agent job(s).", n))
		return true
	case "/jobs":
		if !allowedChatIDs[msg.Chat.ID] {
			return true
		}
		running := orch.Running()
		if len(running) == 0 {
			tg.SendHTML(msg.Chat.ID, "No agent jobs running.")
			return true
		}
		var lines []string
		for _, rt := range running {
			elapsed := time.Since(rt.StartedAt).Truncate(time.Second)
			iter := 0
			if rt.Meta != nil {
				iter = rt.Meta.Iterations
			}
			lines = append(lines, fmt.Sprintf("• <code>%s</code> — %s, iteration %d", rt.ID, elapsed, iter))
		}
		tg.SendHTML(msg.Chat.ID, "<b>Running agent jobs:</b>\n"+strings.Join(lines, "\n"))
		return true
	case "/help":
		help := "<b>Available commands:</b>\n" +
			"/help — Show this message\n" +
			"/new — Start a new conversation session\n" +
			"/bash — Execute a bash command directly\n" +
			"/jobs — List running agent jobs\n" +
			"/cancel — Cancel all running agent jobs\n" +
			"/restart — Restart the ALF daemon\n" +
			"/login — Get a login link for the Control Center\n" +
			"/start — Re-run onboarding (get to know each other)"
		tg.SendHTML(msg.Chat.ID, help)
		return true
	case "/bash":
		if !allowedChatIDs[msg.Chat.ID] {
			return true
		}
		parts := strings.SplitN(msg.Text, " ", 2)
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			tg.SendHTML(msg.Chat.ID, "Usage: <code>/bash &lt;command&gt;</code>")
			return true
		}
		go execBashCommand(tg, msg.Chat.ID, strings.TrimSpace(parts[1]))
		return true
	}
	return false
}

func handleLogin(tg *tgclient.Client, msg *Message, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool) {
	chatID := msg.Chat.ID

	if len(allowedChatIDs) == 0 {
		tg.SendHTML(chatID, "Login is not configured. Set ALLOWED_CHAT_IDS to enable it.")
		return
	}

	if !allowedChatIDs[chatID] {
		tg.SendHTML(chatID, "You are not authorized to access the Control Center.")
		return
	}

	// Send inline keyboard with session duration options.
	keyboard := map[string]any{
		"inline_keyboard": [][]map[string]string{
			{
				{"text": "24 hours", "callback_data": "login:24h"},
				{"text": "7 days", "callback_data": "login:7d"},
				{"text": "30 days", "callback_data": "login:30d"},
			},
		},
	}
	tg.SendKeyboard(chatID, "Choose session duration:", keyboard)
}

func handleCallbackQuery(tg *tgclient.Client, client *http.Client, token string, cb *CallbackQuery, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool) {
	// Always answer callback to remove the loading indicator.
	defer answerCallbackQuery(client, token, cb.ID)

	if cb.Message == nil {
		return
	}

	chatID := cb.Message.Chat.ID

	if !strings.HasPrefix(cb.Data, "login:") {
		return
	}

	if !allowedChatIDs[chatID] {
		tg.SendHTML(chatID, "You are not authorized to access the Control Center.")
		return
	}

	var ttl time.Duration
	var label string
	switch cb.Data {
	case "login:24h":
		ttl = 24 * time.Hour
		label = "24 hours"
	case "login:7d":
		ttl = 7 * 24 * time.Hour
		label = "7 days"
	case "login:30d":
		ttl = 30 * 24 * time.Hour
		label = "30 days"
	default:
		tg.SendHTML(chatID, "Unknown duration. Send /login to try again.")
		return
	}

	code, err := magic.Issue(chatID, ttl)
	if err != nil {
		log.Printf("magic issue error: %v", err)
		tg.SendHTML(chatID, "Failed to generate login link. Try again.")
		return
	}

	link := fmt.Sprintf("%s/auth?code=%s", strings.TrimRight(ccExternalURL, "/"), code)
	tg.SendHTMLNoPreview(chatID, fmt.Sprintf("Session: %s · Expires in 5 min\n%s", label, link))
}

func answerCallbackQuery(client *http.Client, token string, callbackID string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", token)
	payload, _ := json.Marshal(map[string]any{
		"callback_query_id": callbackID,
	})
	resp, err := http.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		log.Printf("answerCallbackQuery error: %v", err)
		return
	}
	defer resp.Body.Close()
}

func resolveTierParams(tierName string, tiers *cc.TiersConfig, dataDir string) tierParams {
	for _, t := range tiers.Tiers {
		if t.Name == tierName {
			model := t.Model
			// For CLI backend, resolve short names to full model IDs.
			// For API backends, use the model string as-is.
			if t.Backend == "" || t.Backend == "cli" {
				model = router.ResolveModel(t.Model)
			}
			// Resolve ["*"] into all available tool names.
			tools := t.Tools
			if len(tools) == 1 && tools[0] == "*" {
				tools = tooling.DiscoverToolNames(dataDir)
				if len(tools) > 0 {
					log.Printf("[chat] tier %q: wildcard resolved to %d tools", tierName, len(tools))
				}
			}
			return tierParams{
				Model:                model,
				Tools:                tools,
				WriteCapable:         t.WriteCapable,
				Effort:               t.Effort,
				MaxTurns:             t.MaxTurns,
				OrchestratorMaxTurns: t.OrchestratorMaxTurns,
				MaxIterations:        t.MaxIterations,
				TimeoutMin:           t.TimeoutMin,
				Backend:              t.Backend,
				SystemPrompt:         t.SystemPrompt,
			}
		}
	}
	// Tier not found — use defaults.
	return tierParams{Model: "claude-haiku-4-5"}
}

// migrateConfig copies config files from old data/config/ to configDir on first run.
// fixDataPermissions ensures all files and directories under dataDir are
// group-readable/writable so the claude subprocess (uid 1001, gid alf/1000)
// ensureBashrcPath adds ~/.local/bin to PATH in .bashrc if not already present.
// This fixes the "native installation exists but ~/.local/bin is not in your PATH" warning
// for interactive shells (CC terminal, docker exec).
func ensureBashrcPath(home string) {
	if home == "" {
		return
	}
	bashrc := filepath.Join(home, ".bashrc")
	line := `export PATH="$HOME/.local/bin:$PATH"`

	// Check if already present.
	if data, err := os.ReadFile(bashrc); err == nil {
		if strings.Contains(string(data), ".local/bin") {
			return
		}
	}

	f, err := os.OpenFile(bashrc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("bashrc: cannot write %s: %v", bashrc, err)
		return
	}
	defer f.Close()
	f.WriteString("\n" + line + "\n")
	log.Printf("bashrc: added .local/bin to PATH")
}

// fixHomeDirPermissions ensures all files under HOME/.claude and HOME/.local
// are owned by the given uid:gid with correct permissions. The daemon runs as
// root and writes files (syncClaudeJSON, npm install, etc.) that the claude
// subprocess (uid 1001) must be able to read/write.
func fixHomeDirPermissions(homeDir string, uid, gid int) {
	// Fix individual files in HOME that root may have written.
	for _, name := range []string{".claude.json", ".gitconfig"} {
		p := filepath.Join(homeDir, name)
		if fi, err := os.Stat(p); err == nil {
			if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
				if int(sys.Uid) != uid || int(sys.Gid) != gid {
					os.Chown(p, uid, gid)
				}
			}
		}
	}
	dirs := []string{
		filepath.Join(homeDir, ".claude"),
		filepath.Join(homeDir, ".local"),
		filepath.Join(homeDir, ".cache"),
		filepath.Join(homeDir, ".npm"),
	}
	fixed := 0
	for _, dir := range dirs {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		// Fix the directory itself first.
		os.Chown(dir, uid, gid)
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if sys, ok := info.Sys().(*syscall.Stat_t); ok {
				if int(sys.Uid) != uid || int(sys.Gid) != gid {
					os.Chown(path, uid, gid)
					fixed++
				}
			}
			mode := info.Mode()
			if info.IsDir() {
				if mode.Perm()&0o070 != 0o070 {
					os.Chmod(path, mode.Perm()|0o070)
				}
			} else {
				if mode.Perm()&0o060 != 0o060 {
					os.Chmod(path, mode.Perm()|0o060)
				}
			}
			return nil
		})
	}
	if fixed > 0 {
		log.Printf("fixed ownership on %d files in HOME subdirs", fixed)
	}
}

// can access files created by root or node before the permission refactoring.
func fixDataPermissions(dataDir string) {
	// Determine the expected uid:gid from the data dir itself.
	var targetUID, targetGID int
	if stat, err := os.Stat(dataDir); err == nil {
		if sys, ok := stat.Sys().(*syscall.Stat_t); ok {
			targetUID = int(sys.Uid)
			targetGID = int(sys.Gid)
		}
	}

	fixed := 0
	docsDir := filepath.Join(dataDir, "docs")
	llmsFile := filepath.Join(dataDir, "llms.txt")
	filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Skip read-only system files.
		if path == llmsFile {
			return nil
		}
		if path == docsDir || strings.HasPrefix(path, docsDir+string(filepath.Separator)) {
			return nil
		}
		mode := info.Mode()
		if info.IsDir() {
			if mode.Perm()&0o070 != 0o070 {
				os.Chmod(path, mode.Perm()|0o070)
				fixed++
			}
		} else {
			if mode.Perm()&0o060 != 0o060 {
				os.Chmod(path, mode.Perm()|0o060)
				fixed++
			}
		}
		// Fix ownership to match the data dir's owner.
		if targetUID > 0 {
			if sys, ok := info.Sys().(*syscall.Stat_t); ok {
				if int(sys.Uid) != targetUID || int(sys.Gid) != targetGID {
					os.Chown(path, targetUID, targetGID)
					fixed++
				}
			}
		}
		return nil
	})
	if fixed > 0 {
		log.Printf("fixed permissions on %d files/dirs in %s", fixed, dataDir)
	}
}

// lockDirReadOnly ensures a directory under /opt/alf/ is owned by alf (uid 1000)
// and group-readable but NOT group-writable. The claude subprocess (uid 1001,
// gid 1000) can read but not modify files. Used for config.d and skills.d.
func lockDirReadOnly(configDir string) {
	fixed := 0
	filepath.Walk(configDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Ensure owned by alf:alf (1000:1000).
		if sys, ok := info.Sys().(*syscall.Stat_t); ok {
			if int(sys.Uid) != 1000 || int(sys.Gid) != 1000 {
				os.Chown(path, 1000, 1000)
				fixed++
			}
		}
		// Dirs: rwxr-x--- (750), Files: rw-r----- (640).
		mode := info.Mode().Perm()
		var want os.FileMode
		if info.IsDir() {
			want = 0o750
		} else {
			want = 0o640
		}
		if mode != want {
			os.Chmod(path, want)
			fixed++
		}
		return nil
	})
	if fixed > 0 {
		log.Printf("locked %s permissions on %d files/dirs (group=ro)", configDir, fixed)
	}
}

// linkSystemTools recreates symlinks in toolsDir for each binary in srcDir.
// Removes all existing symlinks first to clean up tools removed after an upgrade.
func linkSystemTools(toolsDir, srcDir string) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}
	os.MkdirAll(toolsDir, 0o755)

	// Remove all existing symlinks (stale ones from previous versions).
	if existing, err := os.ReadDir(toolsDir); err == nil {
		for _, e := range existing {
			p := filepath.Join(toolsDir, e.Name())
			if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
				os.Remove(p)
			}
		}
	}

	// Recreate symlinks for current tools.
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		link := filepath.Join(toolsDir, e.Name())
		target := filepath.Join(srcDir, e.Name())
		if err := os.Symlink(target, link); err == nil {
			log.Printf("linked tools.d/%s → %s", e.Name(), target)
		}
	}

	// Lock down: tools.d is system-managed, Claude subprocess must not write here.
	os.Chmod(toolsDir, 0o755)
	os.Chown(toolsDir, 0, 0) // root:root
}

// seedDefaultTiers copies /opt/alf/defaults/tiers.json into the config dir
// if no user tiers.json exists yet.
func seedDefaultTiers(configDir string) {
	dest := cc.TiersPath(configDir)
	if _, err := os.Stat(dest); err == nil {
		return // user file already exists
	}
	const defaultPath = "/opt/alf/defaults/tiers.json"
	data, err := os.ReadFile(defaultPath)
	if err != nil {
		log.Printf("seed-tiers: no default at %s: %v", defaultPath, err)
		return
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		log.Printf("seed-tiers: failed to write %s: %v", dest, err)
		return
	}
	log.Printf("seed-tiers: created %s from defaults", dest)
}

// autoEnableAgentTier enables the agent tier in-memory when agent teams are configured.
// Does NOT modify the tiers.json file — only affects the runtime state.
func autoEnableAgentTier(tierStore cc.TierStore) {
	tiers := tierStore.Current()
	for i := range tiers.Tiers {
		if tiers.Tiers[i].Name == "agent" && !tiers.Tiers[i].Enabled {
			tiers.Tiers[i].Enabled = true
			log.Printf("auto-enabled agent tier (agent teams found)")
			return
		}
	}
}

// syncClaudeJSON persists .claude.json across container rebuilds.
// Claude CLI replaces symlinks with real files, so we can't use a symlink.
// Strategy: on startup, restore from the .claude/ volume if the file is missing;
// after restoring (or if already present), back it up into the volume.
// Also ensures group-readable permissions so the claude subprocess (uid 1001) can read it.
func syncClaudeJSON(homeDir string) {
	realFile := filepath.Join(homeDir, ".claude.json")
	volumeCopy := filepath.Join(homeDir, ".claude", "claude.json")

	// If .claude.json is a symlink (from Dockerfile), remove it — we use copies now.
	if fi, err := os.Lstat(realFile); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		os.Remove(realFile)
	}

	if _, err := os.Stat(realFile); os.IsNotExist(err) {
		// File missing (fresh container or rebuild). Try restoring from volume.
		if data, err := os.ReadFile(volumeCopy); err == nil && len(data) > 0 {
			os.WriteFile(realFile, data, 0o640)
			log.Printf("claude-json: restored from volume backup")
		} else {
			// Check for Claude's own backup files.
			backupDir := filepath.Join(homeDir, ".claude", "backups")
			entries, _ := os.ReadDir(backupDir)
			var newest string
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".claude.json.backup.") {
					newest = filepath.Join(backupDir, e.Name())
				}
			}
			if newest != "" {
				if data, err := os.ReadFile(newest); err == nil {
					os.WriteFile(realFile, data, 0o640)
					log.Printf("claude-json: restored from Claude backup %s", filepath.Base(newest))
				}
			}
		}
	}

	// Back up current file into volume for next rebuild.
	if data, err := os.ReadFile(realFile); err == nil && len(data) > 0 {
		os.WriteFile(volumeCopy, data, 0o640)
	}

	// Owned by alf (uid 1000) so CLI provider can resume sessions.
	// Group alf (gid 1000) gives claude subprocess (uid 1001) read access.
	os.Chown(realFile, 1000, 1000)
	os.Chmod(realFile, 0o640)

	// Fix .claude/ directory permissions: group needs read+traverse.
	claudeDir := filepath.Join(homeDir, ".claude")
	filepath.Walk(claudeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			os.Chmod(path, info.Mode()|0o050) // g+rx
		} else {
			os.Chmod(path, info.Mode()|0o040) // g+r
		}
		return nil
	})
}

// cleanClaudeSettings removes .claude/settings.json at startup.
// Claude Code may persist restrictive allow-lists in this file which then
// block tools (Edit, Write, etc.) even when --dangerously-skip-permissions
// is used. Deleting it on restart ensures a clean slate every time.
func cleanClaudeSettings(homeDir string) {
	p := filepath.Join(homeDir, ".claude", "settings.json")
	if err := os.Remove(p); err != nil {
		if !os.IsNotExist(err) {
			log.Printf("clean-settings: failed to remove %s: %v", p, err)
		}
		return
	}
	log.Printf("clean-settings: removed stale %s", p)
}

func migrateConfig(dataDir, configDir string) {
	oldConfigDir := filepath.Join(dataDir, "config")

	// Config files: copy if missing in configDir.
	for _, name := range []string{"config.json", "tiers.json", "router-prompt.md"} {
		dst := filepath.Join(configDir, name)
		if _, err := os.Stat(dst); err == nil {
			continue // already exists
		}
		src := filepath.Join(oldConfigDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			continue // no old file
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			log.Printf("migrate: failed to copy %s: %v", name, err)
			continue
		}
		log.Printf("migrate: %s → %s", src, dst)
	}

	// Migrate cron.json from context/ to config/ (was exposed to Claude's context injection).
	oldCron := filepath.Join(dataDir, "context", "cron.json")
	newCron := filepath.Join(configDir, "cron.json")
	if _, err := os.Stat(newCron); os.IsNotExist(err) {
		if data, err := os.ReadFile(oldCron); err == nil {
			if err := os.WriteFile(newCron, data, 0o644); err == nil {
				os.Remove(oldCron)
				log.Printf("migrate: cron.json → %s", newCron)
			}
		}
	} else {
		os.Remove(oldCron) // clean up old location even if new exists
	}

	// Clean up orphan directories from old layout.
	for _, orphan := range []string{"tiers", "memory", "state"} {
		p := filepath.Join(dataDir, orphan)
		if _, err := os.Stat(p); err == nil {
			if err := os.RemoveAll(p); err != nil {
				log.Printf("migrate: failed to remove old %s: %v", orphan, err)
			} else {
				log.Printf("migrate: removed orphan %s/", orphan)
			}
		}
	}
}

// extractorAdapter bridges provider.CLIProvider to memstore.ExtractorProvider.
type extractorAdapter struct {
	prov *provider.CLIProvider
}

func (a *extractorAdapter) Invoke(ctx context.Context, prompt string, params memstore.ExtractorParams) (string, error) {
	result, err := a.prov.Invoke(ctx, prompt, provider.Params{
		Model:    params.Model,
		MaxTurns: params.MaxTurns,
		DataDir:  params.DataDir,
		Tools:    []string{""}, // explicit empty to disable all tools
	}, nil)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// typingIndicator sends periodic Telegram chat actions (typing, choose_sticker, etc.)
// without sending or editing any messages.
type typingIndicator struct {
	tg     *tgclient.Client
	chatID int64
	action string
	mu     sync.Mutex
	done   chan struct{}
}

func newTypingIndicator(tg *tgclient.Client, chatID int64, action string) *typingIndicator {
	ti := &typingIndicator{
		tg:     tg,
		chatID: chatID,
		action: action,
		done:   make(chan struct{}),
	}
	go ti.run()
	return ti
}

func (ti *typingIndicator) run() {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ti.done:
			return
		case <-ticker.C:
			ti.mu.Lock()
			action := ti.action
			ti.mu.Unlock()
			ti.tg.SendChatAction(ti.chatID, action)
		}
	}
}

// SetAction changes the chat action type.
func (ti *typingIndicator) SetAction(action string) {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	ti.action = action
}

// Stop halts the typing indicator.
func (ti *typingIndicator) Stop() {
	select {
	case <-ti.done:
	default:
		close(ti.done)
	}
}

// maybeSpontaneousReact validates an emoji (with fallback), applies mood-gate probability,
// and sends the reaction. Runs synchronously so the reaction lands before the reply.
func maybeSpontaneousReact(tg *tgclient.Client, chatID, msgID int64, emoji, contextDir string) {
	emoji = mood.ValidateOrFallback(emoji)
	if emoji == "" {
		return
	}
	state := mood.GetCurrentState(contextDir)
	if !mood.ShouldReact(state) {
		log.Printf("reaction %s suggested but skipped (state=%s)", emoji, state)
		return
	}
	log.Printf("→ spontaneous reaction %s on msg %d (state=%s)", emoji, msgID, state)
	tg.SetMessageReaction(chatID, msgID, emoji)
}

// extractReaction parses a [[react:EMOJI]] marker from the start of text.
// Returns the emoji (or "") and the cleaned text with the marker stripped.
func extractReaction(text string) (string, string) {
	trimmed := strings.TrimLeft(text, " \n\r\t")
	if !strings.HasPrefix(trimmed, "[[react:") {
		return "", text
	}
	end := strings.Index(trimmed, "]]")
	if end == -1 {
		return "", text
	}
	emoji := trimmed[len("[[react:"):end]
	rest := strings.TrimLeft(trimmed[end+2:], " \n\r\t")
	if emoji == "none" || emoji == "" {
		return "", rest
	}
	return emoji, rest
}

// ringBuffer is a fixed-capacity ring buffer for tracking message IDs.
type ringBuffer struct {
	mu   sync.Mutex
	data []int64
	pos  int
	full bool
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{data: make([]int64, capacity)}
}

func (r *ringBuffer) Add(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[r.pos] = id
	r.pos = (r.pos + 1) % len(r.data)
	if r.pos == 0 {
		r.full = true
	}
}

func (r *ringBuffer) Contains(id int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	limit := len(r.data)
	if !r.full {
		limit = r.pos
	}
	for i := 0; i < limit; i++ {
		if r.data[i] == id {
			return true
		}
	}
	return false
}

func (r *ringBuffer) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.full {
		return len(r.data)
	}
	return r.pos
}

// chatHistoryBuffer stores recent message exchanges per chat for context injection.
type chatHistoryBuffer struct {
	mu      sync.Mutex
	history map[int64][]chatEntry
	maxSize int
}

type chatEntry struct {
	Role string // "user" or "alf"
	Text string
}

func newChatHistoryBuffer(maxPerChat int) *chatHistoryBuffer {
	return &chatHistoryBuffer{
		history: make(map[int64][]chatEntry),
		maxSize: maxPerChat,
	}
}

func (h *chatHistoryBuffer) Add(chatID int64, role, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Truncate long messages for context summary.
	if len(text) > 200 {
		text = text[:200] + "..."
	}
	entries := h.history[chatID]
	entries = append(entries, chatEntry{Role: role, Text: text})
	if len(entries) > h.maxSize {
		entries = entries[len(entries)-h.maxSize:]
	}
	h.history[chatID] = entries
}

func (h *chatHistoryBuffer) Recent(chatID int64, n int) []chatEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	entries := h.history[chatID]
	if len(entries) <= n {
		return append([]chatEntry{}, entries...)
	}
	return append([]chatEntry{}, entries[len(entries)-n:]...)
}

func (h *chatHistoryBuffer) Clear(chatID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.history, chatID)
}

// mergeMediaGroups consolidates updates that share the same media_group_id
// into a single update with multiple file references. This ensures albums
// (multiple photos/documents sent together) are processed as one message.
func mergeMediaGroups(updates []Update) []Update {
	var merged []Update
	seen := make(map[string]int) // media_group_id → index in merged

	for _, u := range updates {
		if u.Message == nil || u.Message.MediaGroupID == "" {
			merged = append(merged, u)
			continue
		}

		gid := u.Message.MediaGroupID
		if idx, ok := seen[gid]; ok {
			// Merge into existing: append photos/documents from this message.
			target := merged[idx].Message
			if len(u.Message.Photo) > 0 {
				target.Photo = append(target.Photo, u.Message.Photo...)
			}
			if u.Message.Document != nil {
				// Store additional documents as extra photos workaround:
				// we'll handle multi-doc via extraFiles below.
				if target.extraFiles == nil {
					target.extraFiles = []mediaFile{}
				}
				target.extraFiles = append(target.extraFiles, mediaFile{
					FileID:   u.Message.Document.FileID,
					FileName: u.Message.Document.FileName,
				})
			}
			if u.Message.Video != nil {
				if target.extraFiles == nil {
					target.extraFiles = []mediaFile{}
				}
				target.extraFiles = append(target.extraFiles, mediaFile{
					FileID:   u.Message.Video.FileID,
					FileName: u.Message.Video.FileName,
				})
			}
			// Use caption from whichever message has one.
			if target.Caption == "" && u.Message.Caption != "" {
				target.Caption = u.Message.Caption
			}
		} else {
			seen[gid] = len(merged)
			merged = append(merged, u)
		}
	}
	return merged
}

// handleReaction processes an emoji reaction on an Alf message.
func handleReaction(tg *tgclient.Client, chatID, messageID int64, emoji, contextDir, dataDir string, chatSessions *session.Store, tierStore cc.TierStore, alfMsgIDs *ringBuffer, eventLog *eventlog.Logger, prov *provider.CLIProvider) {
	// Log the reaction and update live feedback.
	mood.LogReaction(dataDir, emoji, messageID)
	mood.UpdateLiveFeedback(contextDir, dataDir)

	score, state := mood.GetTodayScore(dataDir)
	log.Printf("reaction scored: emoji=%s score=%d state=%s", emoji, score, state)

	// Mirror reaction.
	shouldReact := mood.ShouldReact(state)
	log.Printf("reaction decision: should_react=%v (state=%s)", shouldReact, state)
	if shouldReact {
		mirror := mood.ChooseMirror(emoji, state)
		log.Printf("reaction mirror: %s → %s (state=%s)", emoji, mirror, state)
		if mirror != "" {
			// Human-like delay before mirror reacting (1.5–4.5s).
			delay := time.Duration(1500+rand.Intn(3000)) * time.Millisecond
			time.Sleep(delay)

			if err := tg.SetMessageReaction(chatID, messageID, mirror); err != nil {
				log.Printf("mirror reaction error: %v", err)
			} else {
				log.Printf("→ mirror reaction sent: %s on msg %d", mirror, messageID)
			}
		}
	}

	// Negative reaction follow-up: ask what went wrong.
	if !mood.IsNegative(emoji) {
		return
	}

	// Strong negative → always follow up. Mild negative → 50% chance.
	if !mood.IsStrongNegative(emoji) && rand.Float64() > 0.5 {
		log.Printf("mild negative %s — skipping follow-up (coin flip)", emoji)
		return
	}

	log.Printf("negative reaction %s — triggering follow-up", emoji)

	// Small delay so mirror reaction lands first.
	time.Sleep(2 * time.Second)
	tg.SendChatAction(chatID, "typing")

	var prompt string
	langNote := "IMPORTANT: Reply in the same language the user has been using in this conversation."
	if mood.IsStrongNegative(emoji) {
		prompt = fmt.Sprintf("The user just reacted with %s to your last message (strong negative). Something is clearly wrong. Acknowledge the negative feedback briefly, identify what likely went wrong in your previous response, and ask a short direct question to understand what they expected. Keep it to 2-3 sentences max. Don't be defensive. %s", emoji, langNote)
	} else {
		prompt = fmt.Sprintf("The user just reacted with %s to your last message (mild negative). Briefly acknowledge the feedback and ask a short question to understand what could be improved. One or two sentences max. Stay casual. %s", emoji, langNote)
	}

	resumeID := chatSessions.Get(chatID)
	// Use the cheapest tier for fast follow-up.
	model := "claude-haiku-4-5"
	fallback := firstFallbackTier(tierStore)
	for _, t := range tierStore.Current().Tiers {
		if t.Name == fallback {
			if m := router.ResolveModel(t.Model); m != "" {
				model = m
			}
			break
		}
	}

	result, err := prov.Invoke(context.Background(), prompt, provider.Params{
		Model:    model,
		ResumeID: resumeID,
		DataDir:  dataDir,
	}, nil)
	if err != nil {
		log.Printf("negative follow-up error: %v", err)
		return
	}

	if result.SessionID != "" {
		chatSessions.SetWithContext(chatID, result.SessionID, "follow-up")
	}

	eventLog.Log("negative_followup", map[string]any{
		"chat_id": chatID,
		"emoji":   emoji,
		"model":   result.Model,
	})

	if result.Text == "Done (no text output)." {
		return
	}
	if msgID, err := tg.SendMessageReturnID(chatID, result.Text); err == nil && msgID != 0 {
		alfMsgIDs.Add(msgID)
	}
}

func parseAllowedChatIDs(s string) map[int64]bool {
	result := make(map[int64]bool)
	if s == "" {
		return result
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			result[id] = true
		}
	}
	return result
}

// autoRecall searches the memory store for relevant context and returns
// a formatted system prompt block plus the best (lowest) distance score.
// Returns ("", 2.0) if nothing relevant.
func autoRecall(store *memstore.Store, message string) (string, float64) {
	if len(message) < 5 {
		return "", 2.0
	}
	q := message
	if len(q) > 60 {
		q = q[:60] + "..."
	}
	results, err := store.Search(message, 3)
	if err != nil {
		log.Printf("auto-recall: search error for %q: %v", q, err)
		return "", 2.0
	}
	if len(results) == 0 {
		log.Printf("auto-recall: no results for %q", q)
		return "", 2.0
	}
	var sb strings.Builder
	bestDist := 2.0
	filtered := 0
	for _, r := range results {
		if r.Distance >= 1.2 {
			filtered++
			continue
		}
		if r.Distance < bestDist {
			bestDist = r.Distance
		}
		if sb.Len() == 0 {
			sb.WriteString("=== [auto-recall] ===\nRelevant memories about the user (auto-retrieved):\n")
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", r.Type, r.Text))
	}
	if sb.Len() > 0 {
		log.Printf("auto-recall: injected %d memories for %q (best=%.2f, filtered %d by distance)", strings.Count(sb.String(), "\n- "), q, bestDist, filtered)
	} else {
		log.Printf("auto-recall: %d results for %q but all filtered by distance (>=1.2)", len(results), q)
	}
	return sb.String(), bestDist
}

// memStoreRecaller adapts memstore.Store to the cc.MemoryRecaller interface.
type memStoreRecaller struct {
	store *memstore.Store
}

func (r *memStoreRecaller) Search(query string, limit int) ([]cc.MemoryResult, error) {
	results, err := r.store.Search(query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]cc.MemoryResult, len(results))
	for i, m := range results {
		out[i] = cc.MemoryResult{Text: m.Text, Type: m.Type, Distance: m.Distance}
	}
	return out, nil
}

// schedulerProvider adapts provider.CLIProvider to the scheduler.ProviderInvoker interface.
type schedulerProvider struct {
	p *provider.CLIProvider
}

func (s *schedulerProvider) Invoke(ctx context.Context, prompt string, params scheduler.ProviderParams, onProgress interface{}) (*scheduler.ProviderResult, error) {
	pp := provider.Params{
		Model:         params.Model,
		Tools:         params.Tools,
		WriteCapable:  params.WriteCapable,
		Effort:        params.Effort,
		SystemPrompts: params.SystemPrompts,
		MaxTurns:      params.MaxTurns,
		DataDir:       params.DataDir,
	}
	result, err := s.p.Invoke(ctx, prompt, pp, nil)
	if err != nil {
		return nil, err
	}
	return &scheduler.ProviderResult{
		SessionID: result.SessionID,
		Text:      result.Text,
		Model:     result.Model,
		CostUSD:   result.CostUSD,
		NumTurns:  result.NumTurns,
	}, nil
}

// schedulerTierStore adapts cc.TierStore to the scheduler.TierStoreReader interface.
type schedulerTierStore struct {
	ts cc.TierStore
}

func (s *schedulerTierStore) Current() *scheduler.TiersSnapshot {
	tc := s.ts.Current()
	if tc == nil {
		return nil
	}
	snap := &scheduler.TiersSnapshot{
		Tiers: make([]scheduler.TierInfo, len(tc.Tiers)),
	}
	for i, t := range tc.Tiers {
		snap.Tiers[i] = scheduler.TierInfo{
			Name:         t.Name,
			Model:        router.ResolveModel(t.Model),
			Tools:        t.Tools,
			WriteCapable: t.WriteCapable,
			Effort:       t.Effort,
			MaxTurns:     t.MaxTurns,
		}
	}
	return snap
}

// schedulerChatLogger adapts cc.ChatStore to the scheduler.ChatLogger interface.
type schedulerChatLogger struct {
	store *cc.ChatStore
}

func (l *schedulerChatLogger) LogScheduledMessage(text, tier, jobName string) {
	l.store.Append(cc.ChatMessage{
		ID:        cc.NewMessageID(),
		Role:      "assistant",
		Text:      text,
		Tier:      tier,
		Timestamp: time.Now(),
		SessionID: "scheduled:" + jobName,
	})
}

// schedulerSkillStore adapts skills.Store for the scheduler's SkillStoreReader interface.
type schedulerSkillStore struct {
	s skills.Store
}

func (a *schedulerSkillStore) Get(name string) (*scheduler.SkillInfo, bool) {
	sk, ok := a.s.Get(name)
	if !ok {
		return nil, false
	}
	return &scheduler.SkillInfo{Name: sk.Name, Prompt: sk.Prompt}, true
}

// schedulerOrchestrator adapts agents.Orchestrator to the scheduler.OrchestratorRunner interface.
type schedulerOrchestrator struct {
	o *agents.Orchestrator
}

func (s *schedulerOrchestrator) Run(ctx context.Context, userMessage string, systemPrompts []string, rc scheduler.RunConfig, onProgress scheduler.ProgressFunc) (string, *scheduler.TaskMeta, error) {
	var agentProgress agents.ProgressFunc
	if onProgress != nil {
		agentProgress = agents.ProgressFunc(onProgress)
	}

	text, meta, err := s.o.Run(ctx, userMessage, systemPrompts, agents.RunConfig{
		Model:                rc.Model,
		Effort:               rc.Effort,
		MaxIterations:        rc.MaxIterations,
		MaxTurns:             rc.MaxTurns,
		OrchestratorMaxTurns: rc.OrchestratorMaxTurns,
	}, agentProgress)
	if err != nil {
		return "", nil, err
	}

	return text, &scheduler.TaskMeta{
		Iterations: meta.Iterations,
		TotalCost:  meta.TotalCost,
		Status:     meta.Status,
	}, nil
}

// ccScheduleAdapter adapts scheduler.Engine to the cc.ScheduleEngine interface.
type ccScheduleAdapter struct {
	engine *scheduler.Engine
}

func (a *ccScheduleAdapter) List(userOnly bool) []cc.ScheduleJob {
	if a.engine == nil {
		return nil
	}
	jobs := a.engine.List(userOnly)
	out := make([]cc.ScheduleJob, len(jobs))
	for i, j := range jobs {
		out[i] = schedulerJobToCC(j)
	}
	return out
}

func (a *ccScheduleAdapter) Create(name, schedule, tier, prompt, command, output string, timeout time.Duration, skills []string) (*cc.ScheduleJob, error) {
	j, err := a.engine.Create(name, schedule, tier, prompt, command, output, timeout, skills)
	if err != nil {
		return nil, err
	}
	sj := schedulerJobToCC(j)
	return &sj, nil
}

func (a *ccScheduleAdapter) CreateReminder(name, schedule, message, output string, timeout time.Duration) (*cc.ScheduleJob, error) {
	j, err := a.engine.CreateReminder(name, schedule, message, output, timeout)
	if err != nil {
		return nil, err
	}
	sj := schedulerJobToCC(j)
	return &sj, nil
}

func (a *ccScheduleAdapter) Delete(id string) error {
	return a.engine.Delete(id)
}

func (a *ccScheduleAdapter) RunNow(id string) error {
	return a.engine.RunNow(id)
}

func (a *ccScheduleAdapter) Update(id string, fields map[string]string) (*cc.ScheduleJob, error) {
	j, err := a.engine.Update(id, fields)
	if err != nil {
		return nil, err
	}
	sj := schedulerJobToCC(j)
	return &sj, nil
}

func schedulerJobToCC(j *scheduler.Job) cc.ScheduleJob {
	sj := cc.ScheduleJob{
		ID:         j.ID,
		Name:       j.Name,
		Schedule:   j.Schedule,
		Tier:       j.Tier,
		Prompt:     j.Prompt,
		Command:    j.Command,
		Message:    j.Message,
		Output:     j.Output,
		Enabled:    j.Enabled,
		System:     j.System,
		Managed:    j.Managed,
		AutoDelete: j.AutoDelete,
		Skills:     j.Skills,
		CreatedAt:  j.CreatedAt.Format(time.RFC3339),
	}
	if j.Timeout > 0 {
		sj.Timeout = j.Timeout.String()
	}
	if j.LastRun != nil {
		sj.LastRun = j.LastRun.Format(time.RFC3339)
	}
	if j.NextRun != nil {
		sj.NextRun = j.NextRun.Format(time.RFC3339)
	}
	sj.LastError = j.LastError
	sj.Running = j.IsRunning()
	return sj
}

// resolveTimezone loads an IANA timezone from config, falling back to TZ env then UTC.
func resolveTimezone(tz string) *time.Location {
	if tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			log.Printf("warning: invalid timezone %q, falling back to TZ env or UTC: %v", tz, err)
		} else {
			log.Printf("scheduler: using timezone %s", tz)
			return loc
		}
	}
	// time.Local already respects the TZ environment variable.
	return time.Local
}

// execBashCommand runs a bash command and sends the output via Telegram.
func execBashCommand(tg *tgclient.Client, chatID int64, command string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	tg.SendChatAction(chatID, "typing")

	err := cmd.Run()
	result := out.String()
	if len(result) > 4000 {
		result = result[:4000] + "\n... (truncated)"
	}

	var msg string
	if err != nil {
		if result != "" {
			msg = fmt.Sprintf("<pre>%s</pre>\n\nExit: %v", tgclient.EscapeHTML(result), err)
		} else {
			msg = fmt.Sprintf("Error: %v", err)
		}
	} else if result == "" {
		msg = "<i>Command completed (no output)</i>"
	} else {
		msg = fmt.Sprintf("<pre>%s</pre>", tgclient.EscapeHTML(result))
	}

	tg.SendHTML(chatID, msg)
}


// writeLLMSIndex generates a llms.txt file in dataDir with an index of all embedded docs.
// This lets the LLM quickly discover available documentation.
func writeLLMSIndex(dataDir string) {
	entries, err := cc.DocsFS().ReadDir("docs")
	if err != nil {
		return
	}

	var b strings.Builder
	b.WriteString("# ALF Documentation Index\n")
	b.WriteString("# Read any doc: cat ~/data/docs/<id>.md\n\n")

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		data, err := cc.DocsFS().ReadFile("docs/" + e.Name())
		if err != nil {
			continue
		}
		// Extract title from first # heading.
		title := id
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "# ") {
				title = strings.TrimPrefix(strings.TrimSpace(line), "# ")
				break
			}
		}
		b.WriteString(fmt.Sprintf("- %s: %s\n", id, title))
	}

	// Write docs to filesystem so LLM can read them (read-only).
	docsDir := filepath.Join(dataDir, "docs")
	os.MkdirAll(docsDir, 0o555)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, _ := cc.DocsFS().ReadFile("docs/" + e.Name())
		dest := filepath.Join(docsDir, e.Name())
		os.Chmod(dest, 0o644) // make writable before overwrite
		os.WriteFile(dest, data, 0o444)
	}

	llmsPath := filepath.Join(dataDir, "llms.txt")
	os.Chmod(llmsPath, 0o644) // make writable before overwrite
	os.WriteFile(llmsPath, []byte(b.String()), 0o444)
	log.Printf("docs: wrote llms.txt (%d docs)", len(entries))
}

// firstFallbackTier returns DefaultFallback from config, or the first enabled
// tier, or the first tier overall. Never hardcodes a tier name.
func firstFallbackTier(tierStore cc.TierStore) string {
	cur := tierStore.Current()
	if cur.DefaultFallback != "" {
		return cur.DefaultFallback
	}
	for _, t := range cur.Tiers {
		if t.Enabled {
			return t.Name
		}
	}
	if len(cur.Tiers) > 0 {
		return cur.Tiers[0].Name
	}
	return ""
}

// onboardingTier picks a capable tier for onboarding (second priority, e.g. sonnet).
func onboardingTier(tierStore cc.TierStore) string {
	cur := tierStore.Current()
	type candidate struct {
		name     string
		priority int
	}
	var candidates []candidate
	for _, t := range cur.Tiers {
		if t.Enabled && t.Name != "agent" {
			candidates = append(candidates, candidate{t.Name, t.Priority})
		}
	}
	if len(candidates) >= 2 {
		best := candidates[0]
		second := candidates[1]
		if second.priority < best.priority {
			best, second = second, best
		}
		for _, c := range candidates[2:] {
			if c.priority < best.priority {
				second = best
				best = c
			} else if c.priority < second.priority {
				second = c
			}
		}
		return second.name
	}
	return firstFallbackTier(tierStore)
}

// tierHasRead returns true if the tier's tool list includes the Read tool.
func tierHasRead(t cc.Tier) bool {
	if t.WriteCapable {
		return true // write-capable tiers have all tools including Read
	}
	for _, tool := range t.Tools {
		if tool == "Read" {
			return true
		}
	}
	return false
}

// lowestMediaTier returns the cheapest enabled tier that has the Read tool.
// Falls back to any enabled tier, then to the first tier.
func lowestMediaTier(tiers *cc.TiersConfig) string {
	bestName := ""
	bestPriority := int(^uint(0) >> 1)
	// First pass: prefer tiers with Read tool.
	for _, t := range tiers.Tiers {
		if t.Enabled && tierHasRead(t) && t.Priority < bestPriority {
			bestName = t.Name
			bestPriority = t.Priority
		}
	}
	if bestName != "" {
		return bestName
	}
	// Second pass: any enabled tier.
	bestPriority = int(^uint(0) >> 1)
	for _, t := range tiers.Tiers {
		if t.Enabled && t.Priority < bestPriority {
			bestName = t.Name
			bestPriority = t.Priority
		}
	}
	if bestName != "" {
		return bestName
	}
	if len(tiers.Tiers) > 0 {
		return tiers.Tiers[0].Name
	}
	return ""
}

// watchConfigFiles polls config files for changes and sends reload events.
func watchConfigFiles(configDir string, reloadCh chan cc.ReloadEvent) {
	type watchEntry struct {
		path  string
		event cc.ReloadEvent
	}
	entries := []watchEntry{
		{cc.TiersPath(configDir), cc.ReloadTiers},
		{filepath.Join(configDir, "config.json"), cc.ReloadConfig},
		{filepath.Join(configDir, "firewall.json"), cc.ReloadFirewall},
	}

	modTimes := make(map[string]time.Time)
	for _, e := range entries {
		if info, err := os.Stat(e.path); err == nil {
			modTimes[e.path] = info.ModTime()
		}
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		for _, e := range entries {
			info, err := os.Stat(e.path)
			if err != nil {
				if prev, ok := modTimes[e.path]; ok && !prev.IsZero() {
					delete(modTimes, e.path)
				}
				continue
			}
			prev := modTimes[e.path]
			if !info.ModTime().Equal(prev) {
				modTimes[e.path] = info.ModTime()
				if !prev.IsZero() {
					log.Printf("config watcher: %s changed, reloading", filepath.Base(e.path))
					select {
					case reloadCh <- e.event:
					default:
					}
				}
			}
		}
	}
}


// seedHeartbeatFile creates a default context/heartbeat.md if it doesn't exist.
func seedHeartbeatFile(contextDir string) {
	hbPath := filepath.Join(contextDir, "heartbeat.md")
	if _, err := os.Stat(hbPath); err == nil {
		return // already exists
	}
	os.MkdirAll(contextDir, 0o755)
	content := `---
tier: haiku
---

`
	if err := os.WriteFile(hbPath, []byte(content), 0o644); err != nil {
		log.Printf("seed heartbeat.md: %v", err)
	} else {
		log.Printf("seeded default context/heartbeat.md")
	}
}

// setupDataSymlinks creates symlinks inside data/ pointing to config.d and skills.d.
func setupDataSymlinks(dataDir, configDir, skillsDir string) {
	links := map[string]string{
		filepath.Join(dataDir, "config.d"): configDir,
		filepath.Join(dataDir, "skills.d"): skillsDir,
	}
	for link, target := range links {
		if dest, err := os.Readlink(link); err == nil && dest == target {
			continue
		}
		os.RemoveAll(link)
		if err := os.Symlink(target, link); err != nil {
			log.Printf("symlink %s → %s: %v", link, target, err)
		} else {
			log.Printf("symlink %s → %s", filepath.Base(link), target)
		}
	}
}

// seedBundledSkills copies missing skill directories from /opt/alf/defaults/skills.d
// into the active skills directory. Existing skills are never overwritten.
func seedBundledSkills(skillsDir string) {
	const defaultsDir = "/opt/alf/defaults/skills.d"
	entries, err := os.ReadDir(defaultsDir)
	if err != nil {
		return // no defaults directory (e.g. running outside Docker)
	}
	os.MkdirAll(skillsDir, 0o755)
	for _, e := range entries {
		if !e.IsDir() {
			// Copy top-level files (e.g. README.md).
			src := filepath.Join(defaultsDir, e.Name())
			dst := filepath.Join(skillsDir, e.Name())
			if _, err := os.Stat(dst); err == nil {
				continue
			}
			data, err := os.ReadFile(src)
			if err == nil {
				os.WriteFile(dst, data, 0o644)
				log.Printf("seeded skill file: %s", e.Name())
			}
			continue
		}
		dest := filepath.Join(skillsDir, e.Name())
		if _, err := os.Stat(dest); err == nil {
			continue // skill already exists
		}
		// Copy entire skill directory.
		src := filepath.Join(defaultsDir, e.Name())
		if err := copyDir(src, dest); err != nil {
			log.Printf("seed skill %s: %v", e.Name(), err)
		} else {
			log.Printf("seeded bundled skill: %s", e.Name())
		}
	}
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(s)
			if err != nil {
				return err
			}
			if err := os.WriteFile(d, data, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// setupUserPackagesPaths adds /opt/alf/user-packages/bin to PATH and lib to LD_LIBRARY_PATH.
func setupUserPackagesPaths() {
	const pkgDir = "/opt/alf/user-packages"
	binDir := filepath.Join(pkgDir, "bin")
	libDir := filepath.Join(pkgDir, "lib")
	os.MkdirAll(binDir, 0o755)
	os.MkdirAll(libDir, 0o755)

	path := os.Getenv("PATH")
	if !strings.Contains(path, binDir) {
		os.Setenv("PATH", binDir+":"+path)
	}
	ldPath := os.Getenv("LD_LIBRARY_PATH")
	if !strings.Contains(ldPath, libDir) {
		if ldPath == "" {
			os.Setenv("LD_LIBRARY_PATH", libDir)
		} else {
			os.Setenv("LD_LIBRARY_PATH", libDir+":"+ldPath)
		}
	}
	log.Printf("user-packages: PATH includes %s, LD_LIBRARY_PATH includes %s", binDir, libDir)
}

// tgToolExecutorAdapter bridges tooling.Executor to provider.ToolExecutor for the TG handler.
type tgToolExecutorAdapter struct {
	exec *tooling.Executor
}

func (a *tgToolExecutorAdapter) Execute(ctx context.Context, call provider.ToolCallRequest) provider.ToolCallResult {
	result := a.exec.Execute(ctx, tooling.CallRequest{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: call.Arguments,
	})
	return provider.ToolCallResult{
		ID:      result.ID,
		Output:  result.Output,
		IsError: result.IsError,
	}
}

