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

	"github.com/alamparelli/alf/internal/ai"
	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/runtime/agents"
	"github.com/alamparelli/alf/internal/runtime/classifier"
	"github.com/alamparelli/alf/internal/comms"
	firewall "github.com/alamparelli/alf/internal/sandbox/network"
	"github.com/alamparelli/alf/internal/marketplace"
	"github.com/alamparelli/alf/internal/envsecrets"
	vault "github.com/alamparelli/alf/internal/sandbox/secrets"
	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/platform/eventlog"
	"github.com/alamparelli/alf/internal/platform/gittrack"
	"github.com/alamparelli/alf/internal/platform/media"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/socketsrv"
	"github.com/alamparelli/alf/internal/memory/curation"
	"github.com/alamparelli/alf/internal/platform/mood"
	provider "github.com/alamparelli/alf/internal/ai/provider"
	"github.com/alamparelli/alf/internal/runtime"
	"github.com/alamparelli/alf/internal/sandbox"
	"github.com/alamparelli/alf/internal/sandbox/integrity"
	"github.com/alamparelli/alf/internal/scheduler"
	"github.com/alamparelli/alf/internal/platform/session"
	"github.com/alamparelli/alf/internal/platform/signal"
	"github.com/alamparelli/alf/internal/platform/supervisor"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
	"github.com/alamparelli/alf/internal/platform/trace"
	tgclient "github.com/alamparelli/alf/internal/telegram"
	"github.com/alamparelli/alf/internal/platform/updater"
	"github.com/alamparelli/alf/internal/voice"
	"gopkg.in/natefinch/lumberjack.v2"
)

var version = "dev"

// resolveModel is the daemon's local adapter around ai.ResolveModel so call
// sites keep the `func(string) string` shape that several downstream APIs
// (agents.NewOrchestrator, cc.NewChatService, ...) still require. Introduced
// by #340 R5g as part of removing the router shim from the daemon's imports.
var resolveModel = func(s string) string { return string(ai.ResolveModel(s)) }

// collectRecentAllMem merges the last n messages across every conversation
// in the memory store, sorted chronologically. Replacement for the previous
// conversation.Store.RecentAll in the daemon's classifier wiring (#336).
func collectRecentAllMem(s memory.Store, n int) []memory.Message {
	if s == nil || n <= 0 {
		return nil
	}
	ctx := context.Background()
	convs, err := s.ListConvs(ctx, memory.ConvFilter{})
	if err != nil || len(convs) == 0 {
		return nil
	}
	var all []memory.Message
	for _, c := range convs {
		msgs, err := s.ListMessages(ctx, c.ID, memory.ListOpts{ApplySummary: true})
		if err != nil {
			continue
		}
		all = append(all, msgs...)
	}
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j-1].CreatedAt > all[j].CreatedAt; j-- {
			all[j-1], all[j] = all[j], all[j-1]
		}
	}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

func main() {
	// Ensure daemon-created files are group-writable (umask 002 = rwxrwxr-x).
	syscall.Umask(0o002)

	var token, chatID string // resolved from vault after unlock
	// Read CC auth token: prefer vault-data (daemon-only), fallback to Docker secret.
	authToken := readAuthToken()

	// CC_AUTH_TOKEN no longer passed to subprocess env — system-tools use ALF_TOOLS_SOCK instead.

	// Claude OAuth: source of truth is ~/.claude/.credentials.json (written by `claude login`).
	// The claude CLI subprocess reads it directly via HOME, and refreshes the token automatically.

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

	// tg is the Telegram client — declared early so onTaskEvent closure can capture it.
	// Assigned later when Telegram credentials are available.
	var tg *tgclient.Client

	// Shared stats for CC status endpoint.
	stats := cc.NewStats()

	// Reload channel: CC writes, daemon reads.
	reloadCh := make(chan cc.ReloadEvent, 16)

	// Magic link auth stores (shared between CC and daemon).
	magic := cc.NewMagicStore(nil)
	magic.StartCleanup()
	sessions := cc.NewFileSessionStore(filepath.Join(configDir, "sessions.json"), nil)
	sessions.StartCleanup()

	// CC external URL for magic link generation.
	ccExternalURL := os.Getenv("CC_EXTERNAL_URL")
	if ccExternalURL == "" {
		ccExternalURL = "http://localhost:" + cc.DefaultPort
	}

	// Ensure log directory exists before setting up file logging.
	os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755)

	// Tee log output to both stdout and a rotating file.
	logWriter := &lumberjack.Logger{
		Filename:   filepath.Join(dataDir, "logs", "daemon.log"),
		MaxSize:    2,    // MB
		MaxBackups: 3,
		MaxAge:     30,   // days
		Compress:   true,
	}
	log.SetOutput(io.MultiWriter(os.Stderr, logWriter))
	defer logWriter.Close()

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

	// Clean deprecated skills and seed bundled defaults from Docker image.
	cleanDeprecatedSkills(skillsDir)
	seedBundledSkills(skillsDir)
	seedBundledTeams(dataDir)
	seedBundledApps(dataDir)

	// Set up user-packages paths.
	setupUserPackagesPaths()

	// Permissions are fixed by the entrypoint (Phase 2.5) before dropping to uid 1000.

	// Migrate config from old data/config/ to configDir (before loading).
	migrateConfig(dataDir, configDir)

	// Generate llms.txt index and seed README.md files.
	writeLLMSIndex(dataDir)
	seedREADMEs(dataDir)

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
	// Seed default claude_models.txt (user-editable Claude model allowlist).
	seedDefaultClaudeModels(configDir)
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
	fwProxy.Store = fwStore
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

	// Start network connection tracker (connects to nettrack-helper if available).
	var netTracker *firewall.NetTracker
	if _, err := exec.LookPath("nettrack-helper"); err == nil {
		netTracker = firewall.NewNetTracker(fwProxy, "/run/alf-nettrack.sock")
		go netTracker.Run(context.Background())
	}

	// Start vault-server if binary is available.
	var vaultMgr *vault.Manager
	if _, err := exec.LookPath("vault-server"); err == nil {
		vaultMgr = vault.NewManager("/opt/alf/vault-data")
		vaultMgr.SetHTTPProxy(proxyURL)
		if err := vaultMgr.Start(context.Background()); err != nil {
			log.Printf("warning: vault-server failed to start: %v", err)
			vaultMgr = nil
		} else if pw := vaultPassword(vaultMgr); pw != "" {
			if err := vaultMgr.AutoUnlock(pw); err != nil {
				log.Printf("warning: vault auto-unlock failed: %v", err)
			} else if _, err := vaultMgr.CreateProxyToken(); err != nil {
				log.Printf("warning: vault proxy token failed: %v", err)
			} else {
				log.Println("vault: unlocked, proxy token created")
				syncVaultHostsToFirewall(vaultMgr, fwProxy)
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

	// Load Claude models allowlist (user-editable, feeds tier-form dropdown
	// and validator). Falls back to embedded default when file absent/empty.
	claudeModelsStore := cc.NewFileClaudeModelsStore(cc.ClaudeModelsPath(configDir))
	if err := claudeModelsStore.Reload(); err != nil {
		log.Printf("WARN: failed to load claude_models.txt: %v — using embedded default", err)
	}
	cc.SetClaudeModelsStore(claudeModelsStore)

	// Load skill catalog: system → bundled copy → user (later overrides earlier).
	skillStore := skills.NewFileSkillStore(skillsDir, filepath.Join(dataDir, "skills.d"), filepath.Join(dataDir, "skills"))

	// Inject existing app names as dynamic triggers for app-builder skill.
	// This lets "update the crypto app" match app-builder automatically.
	injectAppTriggers(skillStore, filepath.Join(dataDir, "apps"))

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

	// Ensure user-facing directories exist.
	os.MkdirAll(filepath.Join(dataDir, "documents"), 0o755)

	// Seed default heartbeat.md if missing.
	seedHeartbeatFile(contextDir)

	// Fix context file permissions (Claude CLI may create files with restrictive umask).
	fixContextPermissions(contextDir)

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

	// LLM transaction log — daily-rotated JSONL in logs/llm/.
	provider.InitLLMLog(dataDir)
	defer provider.CloseLLMLog()

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
	whisperSecret := envsecrets.ReadSecret("WHISPER_SHARED_SECRET")
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

	// Embedding engine: resolve once and share between the legacy memstore
	// (cmd/alf-daemon-side semantic memory) and the unified memory.Store
	// (opened below). Both paths consult the same HTTP embed-server.
	var embedder memory.Embedder
	if cfg.EffectiveMemoryEnabled() {
		embedder = resolveEmbedder(tierStore)
		if embedder != nil {
			if stopper, ok := embedder.(interface{ Stop() }); ok {
				defer stopper.Stop()
			}
		}
	}

	if !cfg.EffectiveMemoryEnabled() {
		log.Println("memstore: disabled by config (memory_enabled=false)")
	}

	// Ring buffer tracking Alf's sent message IDs for reaction matching.
	alfMsgIDs := newRingBuffer(200)
	chatHistory := newChatHistoryBuffer(10) // last 10 exchanges per chat

	// Unified memory store: replaces chatdb + conversation (see #336).
	// One-shot import of any legacy dataDir/logs/chat.db happens BEFORE we
	// open memory.db — migrateChatDBToMemoryDB refuses to run if memory.db
	// already has messages, so pre-existing state is always safe.
	// DEPRECATED: migration call planned for removal in v0.7.14
	// (see cmd/alf-daemon/memorymigrate.go::migrationTargetRemovalVersion).
	if err := migrateChatDBToMemoryDB(dataDir); err != nil {
		log.Fatalf("memory migration: %v", err)
	}
	var memOpts []memory.StoreOption
	if embedder != nil {
		memOpts = append(memOpts, memory.WithEmbedder(embedder))
	}
	memStore, err := memory.NewSQLiteStore(dataDir, memOpts...)
	if err != nil {
		log.Fatalf("memory: %v", err)
	}
	defer memStore.Close()

	// memstore.Store retired in #337 close-out: every writer targets
	// memory.Store directly (Extractor via dedup.IndexWithDedup,
	// Consolidator same, ingest adapter, socket server via socketsrv).
	// The legacy package now holds only the ONNX Embedder + Tokenizer
	// that the embed-server binary consumes.

	// One-shot backfill of pre-#337 memstore data into memory.Store so the
	// recallers (#337c2, now reading from memory.Store) see the existing
	// fact corpus. Sentinel-gated so it runs exactly once per install.
	// Runs asynchronously — on a corpus of thousands of rows the
	// embedder round-trips serialise to ~1s/row, which would block the
	// HTTP server from opening for many minutes otherwise. Search+recall
	// degrade gracefully until the backfill lands (missing rows just
	// don't show up in vec hits yet).
	if cfg.EffectiveMemoryEnabled() {
		go func() {
			if err := migrateMemstoreToMemory(context.Background(), contextDir, memStore); err != nil {
				log.Printf("[memstore-migrate] failed: %v", err)
			}
		}()

		// memstore.sock protocol now served by socketsrv on top of
		// memory.Store (#337c4b3). The path is unchanged so memory-tools
		// connects transparently; the old memDB.ServeUnix is gone, which
		// means /remember calls from LLMs now land in memory.db directly
		// (no longer through the C1 dual-write shim, which only covers
		// the extractor/consolidator write paths).
		sockPath := filepath.Join(contextDir, "memstore.sock")
		memSocketSrv := socketsrv.New(memStore)
		go func() {
			if err := memSocketSrv.ServeUnix(sockPath); err != nil {
				log.Printf("[memory-socket] serve %s: %v", sockPath, err)
			}
		}()
		log.Printf("[memory-socket] ready (socket=%s)", sockPath)
	}

	// Provider: spawn-per-call Claude CLI for responses.
	// Process isolation: daemon runs as alfd (uid 1001), subprocess runs as alf (uid 1000).
	alfCred := &syscall.Credential{Uid: 1000, Gid: 1000}
	tiersTimeout := time.Duration(cfg.TiersTimeout) * time.Second // 0 → default 5m inside NewCLIProvider
	cliProvider := provider.NewCLIProvider(homeDir, dataDir, tiersTimeout, alfCred)

	// API backends: config-driven registration.
	apiHistory := provider.NewHistory(dataDir, 100, sessionTimeout)
	registry := provider.NewRegistry(cliProvider)
	registerBackends(registry, cfg, apiHistory, vaultMgr)
	registerCodex(registry, dataDir, tiersTimeout, vaultMgr, alfCred)

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
					model = resolveModel(t.Model)
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
	orch := agents.NewOrchestrator(cliProvider, agentStore, dataDir, resolveModel, resolveTier)
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
				if fb := cc.DefaultFallbackModel(tierStore.Current()); fb != "" {
					if !strings.Contains(fb, "/") {
						fb = "anthropic/" + fb
					}
					routerModel = fb
				}
			}
		}
	} else {
		routerModel = resolveModel(routerModel)
		if routerModel == "" {
			routerModel = cc.DefaultFallbackModel(tierStore.Current())
		}
	}

	// agentTeamsForRouter converts the agent store into router-friendly team info.
	agentTeamsForRouter := func() []classifier.AgentTeamInfo {
		teams := agentStore.All()
		infos := make([]classifier.AgentTeamInfo, 0, len(teams))
		for _, t := range teams {
			names := make([]string, len(t.Agents))
			for i, a := range t.Agents {
				names[i] = a.Name
			}
			infos = append(infos, classifier.AgentTeamInfo{
				Name:        t.Name,
				Description: t.Description,
				Agents:      names,
			})
		}
		return infos
	}

	// Persistent CLI classifier: avoids 60s+ CLI startup per classification
	// on low-end CPUs. Starts once, stays alive, resets after idle timeout.
	// The system prompt is built from the live tier catalog so the subprocess
	// has authoritative tier state in context from the first message (#332).
	var cliClassifier *provider.CLIClassifier
	if !isAPIRouter {
		cliClassifier = provider.NewCLIClassifier(provider.ClassifierConfig{
			Model:          routerModel,
			SystemPrompt:   classifier.BuildSystemPrompt(tierStore.Current(), dataDir, configDir, agentTeamsForRouter()),
			HomeDir:        homeDir,
			DataDir:        dataDir,
			Credential:     cliProvider.Credential,
			IdleTimeout:    60 * time.Minute,
			EmptyMCPConfig: cliProvider.EmptyMCPConfig,
		})
		go func() {
			if err := cliClassifier.Start(); err != nil {
				log.Printf("classifier: start failed: %v (will retry on first classify)", err)
			}
		}()
	}

	// classifyMessageFull includes session context for continuity routing.
	classifyMessageFull := func(message string, tiers *cc.TiersConfig, lastTier string, msgCount int, recentContext string) classifier.Result {
		prompt := classifier.BuildClassifyPrompt(classifier.ClassifyInput{
			Message:       message,
			Tiers:         tiers,
			DataDir:       dataDir,
			ConfigDir:     configDir,
			AgentTeams:    agentTeamsForRouter(),
			LastTier:      lastTier,
			MessageCount:  msgCount,
			RecentContext: recentContext,
		})
		start := time.Now()

		// Use persistent CLI classifier when available (avoids 60s+ startup per call).
		if cliClassifier != nil {
			cr, err := cliClassifier.Classify(context.Background(), prompt)
			if err != nil {
				log.Printf("router: classifier error: %v", err)
				return classifier.FallbackResult(tiers)
			}
			log.Printf("router: classify took %dms (classifier)", time.Since(start).Milliseconds())
			return classifier.InterpretRaw(cr.Response, tiers, message)
		}

		routerProv := registry.ForBackend(routerBackend)
		params := provider.Params{
			Model:    routerModel,
			MaxTurns: 2,
			DataDir:  dataDir,
		}
		result, err := routerProv.Invoke(context.Background(), prompt, params, nil)
		if err != nil {
			log.Printf("router: classify error: %v", err)
			return classifier.FallbackResult(tiers)
		}
		log.Printf("router: classify took %dms (backend=%s)", time.Since(start).Milliseconds(), routerBackend)
		return classifier.InterpretRaw(result.Text, tiers, message)
	}

	// Chat service for mobile app API (shares Claude invocation with Telegram bot).
	classifyFn := func(message, lastTier string, msgCount int) cc.RouteResult {
		// Build recent context from conversation history (cross-session for continuity).
		recentCtx := memory.BuildRouterContext(collectRecentAllMem(memStore, 6), 3)
		rr := classifyMessageFull(message, tierStore.Current(), lastTier, msgCount, recentCtx)
		return cc.RouteResult{
			Tier:     rr.Tier,
			Response: rr.Response,
			Reason:   rr.Reason,
			React:    rr.React,
		}
	}
	chatService := cc.NewChatService(dataDir, configDir, contextDir, tierStore, chatSessions, eventLog, memStore, transcriber, classifyFn, resolveModel, cliProvider)
	toolRegistry := tooling.NewRegistry(dataDir)
	// Unified capability registry (#338 C2): every NativeTool registered on
	// toolRegistry is mirrored as a KindTool Capability. Consumers keep using
	// tooling.Registry until Runtime arrives in Step 4.
	capRegistry := capability.NewRegistry()
	toolRegistry.SetCapabilityRegistry(capRegistry)
	// #338 C3: mirror every loaded Skill into capRegistry as KindSkill.
	// Re-run after each Reload below so the unified registry stays in sync.
	if err := skills.MirrorInto(skillStore, capRegistry); err != nil {
		log.Printf("skills: capability mirror (initial): %v", err)
	}
	nativeTools := []tooling.NativeTool{
		tooling.BashNativeTool{DataDir: dataDir},
		tooling.GrepNativeTool{DataDir: dataDir},
		tooling.GlobNativeTool{DataDir: dataDir},
		tooling.ReadFileNativeTool{DataDir: dataDir},
		tooling.WriteFileNativeTool{DataDir: dataDir},
		tooling.RemoveNativeTool{DataDir: dataDir},
	}
	toolErrorJournal := tooling.NewErrorJournal(dataDir)
	avatarHandler := &cc.AvatarHandler{DataDir: dataDir}

	toolExecutor := &tooling.Executor{
		DataDir:      dataDir,
		HomeDir:      homeDir,
		Registry:     toolRegistry,
		ErrorJournal: toolErrorJournal,
		Timeout:      30 * time.Second,
		Env:          nil, // Tools use ALF_TOOLS_SOCK (from safeEnv) instead of CC_AUTH_TOKEN
	}

	// Tool integrity guard — hash-based tamper detection for user tools (issue #121).
	var broadcastFunc func(string) // set after commEngine init
	integrityNotify := func(tool, oldHash, newHash string) {
		msg := fmt.Sprintf("⚠️ Tool %q was modified (hash mismatch). Quarantined.\nOld: %s\nNew: %s\nUse /tool keep %s or /tool revert %s",
			tool, oldHash[:12], newHash[:12], tool, tool)
		if broadcastFunc != nil {
			broadcastFunc(msg)
		} else {
			log.Printf("[integrity] %s", msg)
		}
	}
	integrityGuard, err := integrity.NewIntegrityGuard(dataDir, integrityNotify)
	if err != nil {
		log.Printf("integrity guard: %v (disabled)", err)
	} else {
		toolExecutor.Integrity = integrityGuard
		toolRegistry.Integrity = integrityGuard
		integrityGuard.Watch(500 * time.Millisecond)
		log.Println("integrity guard: enabled for user tools (polling every 500ms)")
	}
	for _, t := range nativeTools {
		toolRegistry.RegisterNative(t)
		toolExecutor.RegisterNative(t)
	}
	orch.SetTooling(toolRegistry, toolExecutor)

	// Unified comms engine: shared pipeline for CC (and later TG).
	engineClassify := func(message, lastTier string, msgCount int, recentCtx string) comms.RouteResult {
		rr := classifyMessageFull(message, tierStore.Current(), lastTier, msgCount, recentCtx)
		return comms.RouteResult{Tier: rr.Tier, Response: rr.Response, Reason: rr.Reason, React: rr.React}
	}
	engineBackendConfigs := func() map[string]comms.BackendConfig {
		result := make(map[string]comms.BackendConfig, len(cfg.Backends))
		for name, bc := range cfg.Backends {
			result[name] = comms.BackendConfig{InputPrice: bc.InputPrice, OutputPrice: bc.OutputPrice}
		}
		return result
	}
	// Recaller reads from the unified memory.Store (#337c2). The dual-write
	// shim (C1) keeps documents in sync with memstore, so this path now
	// covers both freshly-extracted facts and the existing memstore corpus
	// (via the planned one-shot migration in C3).
	var engineRecaller comms.MemoryRecaller
	if cfg.EffectiveMemoryEnabled() {
		engineRecaller = &memoryCommsRecaller{store: memStore}
	}
	commEngine := comms.NewEngine(comms.EngineConfig{
		DataDir:        dataDir,
		ConfigDir:      configDir,
		ContextDir:     contextDir,
		Sessions:       chatSessions,
		Memory:         memStore,
		EventLog:       eventLog,
		TierStore:      &commsTierStore{ts: tierStore},
		SkillStore:     skillStore,
		Registry:       registry,
		Orchestrator:   orch,
		Recaller:       engineRecaller,
		ToolRegistry:   toolRegistry,
		ToolExecutor:   toolExecutor,
		ClassifyFull:   engineClassify,
		ResolveModel:   resolveModel,
		BackendConfigs: engineBackendConfigs,
		RecallCfg: comms.RecallConfig{
			Limit:    cfg.RecallLimit,
			Distance: cfg.RecallDistance,
		},
		SummarizationEnabled:   cfg.EffectiveSummarizationEnabled(),
		SummarizationThreshold: cfg.EffectiveSummarizationThreshold(),
		SummarizationKeepLast:  cfg.EffectiveSummarizationKeepLast(),
	})
	// Initialize all optional dependencies in one place (issue #91).
	// Reader path: memory.Store.Search via fan-out across scopes (#337c2).
	// Writer path for /api/memory/ingest: memory.Store.Index via the ingest
	// adapter (#337c4a). memstore's dual-write shim still fires for legacy
	// write paths (extractor/consolidator/socket-server) until they retire.
	var recaller cc.MemoryRecaller
	var memRecallStore cc.MemoryStorer
	if cfg.EffectiveMemoryEnabled() {
		recaller = &memoryCCRecaller{store: memStore}
		memRecallStore = &memoryIngestAdapter{store: memStore}
	}
	chatService.Init(cc.ChatServiceOpts{
		Registry:       registry,
		SkillStore:     skillStore,
		Orchestrator:   orch,
		ToolRegistry:   toolRegistry,
		ToolExecutor:   toolExecutor,
		Recaller:       recaller,
		MemStore:       memRecallStore,
		BackendConfigs: func() map[string]cc.BackendConfig { return cfg.Backends },
	})
	chatService.SetEngine(commEngine)
	broadcastFunc = commEngine.Broadcast // wire integrity guard notifications
	if cfg.BroadcastChannel != "" {
		comms.BroadcastChannel = cfg.BroadcastChannel
	}
	log.Println("comms engine: initialized for CC channel")

	// notifyChannel sends a text message to the originating channel via the comms engine.
	// Used by chain/task notifications to route results back to CC, TG, or any future channel.
	notifyChannel := func(source, text string) {
		if source == "" {
			source = "cc"
		}
		adapter := commEngine.Adapter(source)
		if adapter == nil {
			log.Printf("[notify] no adapter for channel %q, falling back to cc", source)
			adapter = commEngine.Adapter("cc")
		}
		if adapter == nil {
			log.Printf("[notify] no cc adapter available, dropping message")
			return
		}
		channelID := comms.ChannelID(source + ":0")
		if _, err := adapter.SendText(channelID, text); err != nil {
			log.Printf("[notify] %s send failed: %v", source, err)
		}
	}

	// Declared early so the signal server closure can reference it.
	var eventBroker *cc.EventBroker

	// Persistent signal server for notify/react/status tools (all channels).
	persistentSigPath := filepath.Join(dataDir, "signal.sock")
	persistentSigServer := &signal.Server{Notify: func(text string) {
		// Inject into the active CC chat conversation (same channel as the LLM).
		convID := chatService.CurrentConvID()
		if convID == "" {
			convID = "_system"
		}
		ctx := context.Background()
		_ = memStore.EnsureConv(ctx, memory.ConvID(convID), "", "cc")
		_, _ = memStore.AppendMessage(ctx, memory.ConvID(convID), memory.Message{
			Role:      "assistant",
			Channel:   "cc",
			Content:   text,
			Blocks:    []memory.ContentBlock{{Type: memory.BlockText, Text: text}},
			Tier:      "notify",
			SessionID: "signal:notify",
		})
		// Emit SSE so CC frontend reloads the conversation.
		if eventBroker != nil {
			eventBroker.Emit(cc.EventNewMessage)
		}
	}}
	if ln, err := persistentSigServer.ListenUnix(persistentSigPath); err != nil {
		log.Printf("signal: persistent listen error: %v", err)
	} else {
		go persistentSigServer.Serve(ln)
		commEngine.SignalSockPath = persistentSigPath
		toolExecutor.Env = append(toolExecutor.Env, "ALF_SIGNAL_SOCK="+persistentSigPath)
		log.Printf("signal: persistent socket ready at %s", persistentSigPath)
		defer func() { ln.Close(); os.Remove(persistentSigPath) }()
	}

	// Marketplace: app lifecycle management.
	// Only enabled when ALF_MARKETPLACE_URL is set (homelab only for now).
	var mpManager *marketplace.Manager
	if os.Getenv("ALF_MARKETPLACE_URL") != "" || os.Getenv("ALF_MARKETPLACE_ENABLED") == "true" {
		mpManager = marketplace.NewManager(dataDir)
		mpManager.SetOnChange(func() {
			toolRegistry.Rescan()
			// #338 C4: re-mirror apps into the unified registry on every
			// install / uninstall / update.
			if err := marketplace.MirrorInto(mpManager, capRegistry); err != nil {
				log.Printf("marketplace: capability mirror (change): %v", err)
			}
		})
		if err := mpManager.RestoreInstalled(); err != nil {
			log.Printf("[marketplace] restore installed apps: %v", err)
		} else {
			toolRegistry.Rescan()
			if err := marketplace.MirrorInto(mpManager, capRegistry); err != nil {
				log.Printf("marketplace: capability mirror (initial): %v", err)
			}
		}
		log.Printf("[marketplace] enabled (registry=%s)", os.Getenv("ALF_MARKETPLACE_URL"))
	} else {
		log.Println("[marketplace] disabled (set ALF_MARKETPLACE_URL or ALF_MARKETPLACE_ENABLED=true to enable)")
	}

	// Schedule adapter (engine set later after scheduler is created).
	schedAdapter := &ccScheduleAdapter{}
	var ccServerRef *cc.Server
	var llmVaultProxy *vault.VaultProxy

	// #340 R4g: build the shared Runtime once, before CC + scheduler wire up.
	// Both consumers reuse the same instance — Options.Tier is ignored by
	// Converse/Chat/Invoke, so a single Runtime is correct. capRegistry is a
	// pointer, so later registrations (scheduler.CommandCapability) stay
	// visible to the Runtime's List()/Resolve() lookups.
	sharedRuntime, rtErr := runtime.New(runtime.Deps{
		Registry: capRegistry,
		Memory:   memStore,
		AI:       provider.NewRegistryEngine(registry),
		Sandbox:  sandbox.New(),
	}, runtime.Options{Tier: sandbox.Tier("direct")})
	if rtErr != nil {
		log.Printf("runtime: init failed: %v (CC + scheduler will fall back to legacy paths)", rtErr)
	}
	if sharedRuntime != nil && chatService != nil {
		chatService.SetRuntime(sharedRuntime)
	}
	if sharedRuntime != nil && commEngine != nil {
		commEngine.SetRuntime(sharedRuntime)
	}

	// Start Control Center HTTP server.
	if authToken != "" || len(allowedChatIDs) > 0 {
		// On vault unlock, re-register backends and load Telegram credentials.
		onVaultUnlock := func() {
			if vaultMgr == nil || vaultMgr.AdminToken() == "" {
				return
			}
			// Re-register backends now that vault is unlocked and API keys are accessible.
			registerBackends(registry, cfg, apiHistory, vaultMgr)
			registerCodex(registry, dataDir, tiersTimeout, vaultMgr, alfCred)
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
			// Tag vault service hosts in firewall log.
			syncVaultHostsToFirewall(vaultMgr, fwProxy)
		}
		// Task event callback: route notification to originating channel.
		onTaskEvent := func(source, taskID, status, summary string) {
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
			log.Printf("[tasks] event: task=%s status=%s origin=%s", taskID[:min(8, len(taskID))], status, source)
			notifyChannel(source, text)
		}
		ccServer, broker, err := cc.New(dataDir, configDir, skillsDir, stats, version, authToken, ccExternalURL, cfg, reloadCh, magic, sessions, chatService, memRecallStore, cliProvider, orch, agentStore, schedAdapter, fwStore, fwProxy, netTracker, vaultMgr, registry, onVaultUnlock, onTaskEvent, mpManager, toolErrorJournal, avatarHandler, sharedRuntime)
		if err != nil {
			log.Printf("warning: failed to start Control Center: %v", err)
		} else {
			eventBroker = broker
			chatService.SetEventBroker(broker)
			go func() {
				if err := ccServer.Start(); err != nil && err != http.ErrServerClosed {
					log.Printf("Control Center error: %v", err)
				}
			}()
			log.Printf("Control Center started on :8080 (allowed_chat_ids=%d, external_url=%s)", len(allowedChatIDs), ccExternalURL)
		}
		ccServerRef = ccServer

		// Tools proxy socket: system-tools connect here instead of HTTP+CC_AUTH_TOKEN.
		// Socket access (mode 0660, group alf) = authentication. Dangerous endpoints blocked.
		toolsSockPath := filepath.Join(contextDir, "tools.sock")
		if toolsLn, err := cc.ListenAndServeTools(toolsSockPath, ccServer.InternalHandler()); err != nil {
			log.Printf("warning: tools proxy socket failed: %v", err)
		} else {
			os.Setenv("ALF_TOOLS_SOCK", toolsSockPath)
			defer func() { toolsLn.Close(); os.Remove(toolsSockPath) }()
		}

		// LLM vault proxy socket: unfiltered proxy for all LLM subprocesses
		// (Claude CLI, Codex, vault-cli). Token injected server-side.
		// llmVaultProxy is hoisted so OnTokenUpdate can refresh it after vault restart.
		if vaultMgr != nil && vaultMgr.ProxyToken() != "" {
			llmVaultSock := filepath.Join(contextDir, "vault-llm.sock")
			llmVaultProxy = vault.NewVaultProxy(vaultMgr.SocketPath(), vaultMgr.ProxyToken(), nil)
			if llmLn, err := llmVaultProxy.ListenAndServe(llmVaultSock); err != nil {
				log.Printf("warning: LLM vault proxy socket failed: %v", err)
			} else {
				os.Setenv("VAULT_PROXY_SOCK", llmVaultSock)
				os.Setenv("VAULT_ADDR", "unix:"+llmVaultSock)
				defer func() { llmLn.Close(); os.Remove(llmVaultSock) }()
				log.Printf("vault: LLM proxy on %s", llmVaultSock)
			}
		}
	} else {
		log.Println("CC_AUTH_TOKEN and ALLOWED_CHAT_IDS not set - Control Center disabled")
	}

	// Register system native tools — gives the LLM structured access to ALF subsystems.
	// Registered after CC init so mpManager and other deps are available.
	toolAppStore := cc.NewFileAppStore(filepath.Join(dataDir, "apps"))
	toolLogReader := cc.LogReaderFactory(dataDir)
	appToolAdapter := appAdapter{appStore: toolAppStore, marketplace: mpManager}

	// Shared chain notification function — used by both LLMNativeTool and TaskNativeTool.
	chainNotifyFunc := func(origin tooling.ChainOrigin, chainID, status, message string) {
		short := chainID
		if len(short) > 8 {
			short = short[:8]
		}
		var text string
		if status == "completed" {
			text = "Chain #" + short + " completed"
			if message != "" {
				preview := message
				if len(preview) > 500 {
					preview = preview[:500] + "..."
				}
				text += "\n" + preview
			}
		} else {
			text = "Chain #" + short + " " + status
			if message != "" {
				text += ": " + message
			}
		}
		log.Printf("[chain] event: chain=%s status=%s origin=%s", short, status, origin.Source)
		notifyChannel(origin.Source, text)
	}

	systemTools := []tooling.NativeTool{
		tooling.TaskNativeTool{
			Service: &taskAdapter{
				orch:         orch,
				dataDir:      dataDir,
				contextDir:   contextDir,
				tierStore:    tierStore,
				skillStore:   skillStore,
				resolveModel: resolveModel,
				eventLog:     eventLog,
			},
			TeamService: &teamAdapter{
				store:   agentStore,
				dataDir: dataDir,
			},
			DataDir: dataDir,
			LLMService: &llmAdapter{
				tierStore:        tierStore,
				providerRegistry: registry,
				resolveModel:     resolveModel,
				dataDir:          dataDir,
			},
			NotifyFunc: chainNotifyFunc,
		},
		tooling.TeamNativeTool{Service: &teamAdapter{
			store:   agentStore,
			dataDir: dataDir,
		}},
		tooling.SkillNativeTool{Service: &skillAdapter{
			store:     skillStore,
			skillsDir: skillsDir,
			dataDir:   dataDir,
		}},
		tooling.AppNativeTool{Service: &appToolAdapter},
		tooling.ConfigNativeTool{Service: &configAdapter{store: configStore}, Avatar: avatarHandler},
		tooling.TierNativeTool{Service: &tierAdapter{store: tierStore}},
		tooling.LogNativeTool{Service: &logAdapter{reader: toolLogReader}},
		tooling.FirewallNativeTool{Service: &firewallToolAdapter{proxy: fwProxy, store: fwStore}},
		tooling.SearchNativeTool{Service: &searchAdapter{
			appStore:    toolAppStore,
			marketplace: mpManager,
			dataDir:     dataDir,
		}},
		tooling.LLMNativeTool{
			Service: &llmAdapter{
				tierStore:        tierStore,
				providerRegistry: registry,
				resolveModel:     resolveModel,
				dataDir:          dataDir,
			},
			NotifyFunc: chainNotifyFunc,
		},
	}
	for _, t := range systemTools {
		toolRegistry.RegisterNative(t)
		toolExecutor.RegisterNative(t)
	}
	log.Printf("tooling: registered %d system native tools", len(systemTools))

	var offset int64
	client := &http.Client{Timeout: 35 * time.Second}

	// Telegram client for sending formatted messages (nil if TG disabled).
	// var tg declared earlier (line ~107) so onTaskEvent closure can capture it.
	var tgAdapt *tgAdapter
	tgChatSem := make(chan struct{}, 1) // serialize message processing per chat
	if telegramEnabled {
		tg = tgclient.NewClient(token)
		tg.HTTP = client
		tg.OnRateLimit = func(wait time.Duration) {
			eventLog.Log("telegram_rate_limit", map[string]any{
				"wait_seconds": wait.Seconds(),
			})
			log.Printf("[telegram] rate limited - waiting %v before retry", wait)
		}
		tgAdapt = newTGAdapter(tg)
		// Set broadcast targets so system notifications reach all allowed chats.
		if len(allowedChatIDs) > 0 {
			targets := make([]int64, 0, len(allowedChatIDs))
			for id := range allowedChatIDs {
				targets = append(targets, id)
			}
			tgAdapt.SetBroadcastTargets(targets)
		}
		commEngine.RegisterAdapter(tgAdapt)

		// Register bot commands for the Telegram command menu (/ autocomplete).
		go refreshTelegramCommands(tg, tierStore)
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

	var catchupMinInterval time.Duration
	if s := cfg.CatchupRecurringMinInterval; s != "" {
		if d, err := time.ParseDuration(s); err != nil {
			log.Printf("scheduler: invalid catchup_recurring_min_interval %q: %v — disabled", s, err)
		} else {
			catchupMinInterval = d
		}
	}

	// #340 R5a: register scheduler.command Capability so direct-tier bash
	// jobs can execute through Runtime.Invoke. Constructed before Runtime
	// so runtime.New sees it in the registry listing.
	if err := capRegistry.Register(scheduler.NewCommandCapability(dataDir, persistentSigPath)); err != nil {
		log.Printf("scheduler: register CommandCapability: %v (direct-tier jobs will use legacy path)", err)
	}
	// #340 R5e3: wrap the multi-agent orchestrator as an ai.Strategy so
	// orchestrator-tier scheduler jobs dispatch through Runtime.Converse
	// like the direct-LLM path. StrategyOptions.Source is the tag that
	// surfaces in TaskMeta; SkillLookup / MemoryContext stay nil — the
	// scheduler still flattens skills into SystemPrompts the legacy way.
	schedOrchStrategy := agents.NewStrategy(orch, agents.StrategyOptions{Source: "schedule"})

	// #340 R4g: scheduler reuses the shared Runtime built earlier.
	schedRuntime := sharedRuntime

	sched := scheduler.New(scheduler.Config{
		DataDir:      dataDir,
		ContextDir:   contextDir,
		ChatID:       parsedChatID,
		TG:           tg,
		CC:           &schedulerCCNotifier{mem: memStore, broker: eventBroker},
		Provider:     &schedulerProvider{r: registry},
		TierStore:    &schedulerTierStore{ts: tierStore},
		SkillStore:   &schedulerSkillStore{s: skillStore},
		Orchestrator: &schedulerOrchestrator{o: orch},
		ChatLogger:   &schedulerChatLogger{mem: memStore},
		EventLog:       eventLog,
		ToolErrors:     toolErrorJournal,
		CronPath:       filepath.Join(configDir, "cron.json"),
		Location:       schedLocation,
		SignalSockPath: persistentSigPath,
		Runtime:              schedRuntime,
		OrchestratorStrategy: schedOrchStrategy,
		CatchupRecurringMinInterval: catchupMinInterval,
	})

	// Register system jobs (replaces individual goroutine patterns).
	if git != nil && cfg.GitSweepInterval > 0 {
		sched.RegisterSystem("git-sweep", "Git Sweep",
			fmt.Sprintf("@every %dm", cfg.GitSweepInterval),
			func() error { return git.Commit("auto: periodic sweep") },
			"Periodically commits changes in the data directory to the local git repo for backup and history tracking.",
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
			"Checks for new ALF Docker image versions and notifies when an update is available.",
		)
		if ccServerRef != nil {
			ccServerRef.SetUpdater(uc)
		}
	}
	var memExtractor *curation.Extractor
	if cfg.EffectiveMemoryEnabled() {
		extractorTierResolver := func() string {
			// Delegates to the single source of truth. Never returns a
			// hardcoded model — users can run any backend (see #291).
			return cc.DefaultFallbackModel(tierStore.Current())
		}
		extractTimeout := time.Duration(cfg.EffectiveMemoryExtractTimeout()) * time.Second
		extractAdapter := &extractorAdapter{prov: cliProvider, registry: registry, tierStore: tierStore}

		memExtractor = curation.NewExtractor(dataDir, contextDir, curation.ExtractorConfig{
			Timeout:      extractTimeout,
			MsgThreshold: cfg.EffectiveMemoryExtractMinMessages(),
		}, extractAdapter, extractorTierResolver)

		// Extractor writes through memory.Store via dedup. Threshold
		// 0.85 matches memstore's prior CosineThreshold at the high end
		// — conservative so we don't over-deduplicate while the embedder
		// warms up.
		memExtractor.SetMemoryBackend(memStore, 0.85)

		// Consolidator walks memory.Store via ListDocuments across the
		// same scopes the socket server and recallers use. Same
		// threshold as the extractor for symmetry.
		consolidator := curation.NewConsolidator(memExtractor, extractAdapter, extractTimeout)
		consolidator.SetMemoryBackend(memStore, socketsrv.KnownScopes, 0.85)
		sched.RegisterSystem("mem-consolidate", "Memory Consolidation", "@every 360m", func() error {
			return consolidator.RunOnce()
		}, "Review the long-term memory store for redundancy and quality. Use recall to sample memories across all types (fact, decision, preference, contact). For each group of similar or overlapping memories: keep the most accurate and complete version, delete duplicates with forget, and if needed consolidate into a single updated memory with remember. Do not remove unique information. Focus on reducing noise without data loss.")

		// Run initial extraction after boot delay.
		extractBootDelay := time.Duration(cfg.EffectiveMemoryExtractBootDelay()) * time.Second
		go func() {
			time.Sleep(extractBootDelay)
			if memExtractor.HasChanges() {
				log.Println("memstore: running initial extraction (pending changes)")
				if err := memExtractor.Extract(); err != nil {
					log.Printf("memstore: initial extraction failed: %v", err)
				}
			}
		}()
	}

	// Wire memory extraction hooks into comms engine.
	if memExtractor != nil {
		commEngine.OnSessionEnd = memExtractor.OnSessionEnd
		commEngine.OnMessage = memExtractor.OnMessage
	}

	// Daily schedule digest - runs at 08:00 local time.
	sched.RegisterSystem("sched-digest", "Schedule Digest", "0 0 8 * * *", sched.SendDailyDigest,
		"Sends a daily summary of scheduled jobs at 8am: upcoming runs, recent failures, and job stats.")

	// Daily tool stats — aggregates last 7 days of tool_exec spans into
	// logs/traces/stats-YYYY-MM-DD.json. Runs at 00:05 local time.
	sched.RegisterSystem("tool-stats", "Tool Execution Stats", "0 5 0 * * *", func() error {
		report, err := trace.AggregateToolStats(dataDir, 7)
		if err != nil {
			return err
		}
		return trace.WriteToolStatsReport(dataDir, report)
	}, "Aggregates the last 7 days of tool execution traces (logs/traces/*.jsonl) into a daily stats report at logs/traces/stats-YYYY-MM-DD.json: runs, errors, error rate, avg/p95 duration per tool.")

	// Vault token health check — every hour, alerts on expired/expiring tokens.
	if vaultMgr != nil {
		checker := newVaultTokenChecker(vaultMgr)
		sched.RegisterSystem("vault-token-check", "Vault Token Health", "@every 60m", checker.Check,
			"Checks OAuth2 token expiry for all vault services. Alerts when tokens are expiring or expired.")
	}

	schedAdapter.engine = sched
	if eventBroker != nil {
		sched.OnChange = func() { eventBroker.Emit(cc.EventSchedules) }
	}

	// Ensure contextDir exists before the scheduler binds its socket there.
	// Bootstrap already creates it, but this is cheap insurance against a
	// future refactor that removes or reorders the bootstrap call.
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		log.Printf("ERROR: cannot create context dir %s: %v", contextDir, err)
	}
	schedSockPath := filepath.Join(contextDir, "scheduler.sock")
	if err := sched.Start(schedSockPath); err != nil {
		// Do not silently degrade: without the socket, schedule-tools, digests,
		// memory consolidation and health checks all stop working. Log loudly
		// so operators notice in daemon.log.
		log.Printf("ERROR: scheduler failed to start on %s: %v — schedule tools will be unavailable", schedSockPath, err)
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
	if vaultMgr != nil && vaultMgr.ProxyToken() != "" && mpManager != nil {
		appsSupervisor.SetVault(vaultMgr.SocketPath(), vaultMgr.ProxyToken(), mpManager.GetServices)
	}
	// Per-app tools sockets: each supervised app gets <workDir>/tools.sock
	// serving a slug-scoped CC subset (reads + /api/bash with permission check).
	if ccServerRef != nil {
		appsSupervisor.SetAppTools(func(sockPath, slug string) (net.Listener, error) {
			return cc.ListenAndServeAppTools(sockPath, slug, ccServerRef.InternalHandler())
		})
	}
	// Always register OnTokenUpdate — vault may be unlocked after boot via CC.
	if vaultMgr != nil {
		vaultMgr.OnTokenUpdate = func(token string) {
			// Initialize supervisor vault on first unlock, update token on subsequent calls.
			if appsSupervisor.HasVault() {
				appsSupervisor.UpdateProxyToken(token)
			} else if mpManager != nil {
				appsSupervisor.SetVault(vaultMgr.SocketPath(), token, mpManager.GetServices)
			}
			if llmVaultProxy != nil {
				llmVaultProxy.UpdateToken(token)
				log.Println("[vault] LLM proxy token updated")
			} else {
				// Vault was unlocked after daemon boot — create the LLM proxy now.
				llmVaultSock := filepath.Join(contextDir, "vault-llm.sock")
				llmVaultProxy = vault.NewVaultProxy(vaultMgr.SocketPath(), token, nil)
				if _, err := llmVaultProxy.ListenAndServe(llmVaultSock); err != nil {
					log.Printf("warning: LLM vault proxy socket failed: %v", err)
					llmVaultProxy = nil
				} else {
					os.Setenv("VAULT_PROXY_SOCK", llmVaultSock)
					os.Setenv("VAULT_ADDR", "unix:"+llmVaultSock)
					log.Printf("[vault] LLM proxy created on %s (late start)", llmVaultSock)
				}
			}
		}
	}
	appsSupervisor.Start()
	defer appsSupervisor.Stop()
	cc.SetAppRestarter(appsSupervisor)

	// Wire supervisor into marketplace and app tool so install/enable/disable/update/restart manage services.
	if mpManager != nil {
		mpManager.SetSupervisor(appsSupervisor)
	}
	appToolAdapter.supervisor = appsSupervisor

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
					registerCodex(registry, dataDir, tiersTimeout, vaultMgr, alfCred)
					// Dedup thresholds are no longer tunable at runtime — the
					// legacy memstore.Store.SetDedupConfig path is gone (#337
					// close-out). dedup.Options.NearDupThreshold is set at
					// Extractor/Consolidator wire time; a restart is required
					// to change it.
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
						if fb := cc.DefaultFallbackModel(tierStore.Current()); fb != "" {
							if !strings.Contains(fb, "/") {
								fb = "anthropic/" + fb
							}
							newModel = fb
						}
					}
					routerModel = newModel
					// Shut down CLI classifier if switching to API router.
					if cliClassifier != nil {
						cliClassifier.Close()
						cliClassifier = nil
					}
				} else {
					newModel := resolveModel(tierStore.Current().RouterModel)
					if newModel == "" {
						newModel = resolveModel("haiku")
					}
					routerModel = newModel
					// Rebuild the classifier system prompt from the fresh tier
					// catalog. Without this, the persistent subprocess keeps
					// stale tier state in its conversation history and the
					// router ignores renames/additions until the next idle
					// restart (#332).
					newSysPrompt := classifier.BuildSystemPrompt(tierStore.Current(), dataDir, configDir, agentTeamsForRouter())
					if cliClassifier != nil {
						if err := cliClassifier.UpdateSystemPrompt(newSysPrompt); err != nil {
							log.Printf("classifier: UpdateSystemPrompt failed: %v", err)
						}
						if err := cliClassifier.UpdateModel(newModel); err != nil {
							log.Printf("classifier: UpdateModel failed: %v", err)
						}
					} else {
						cliClassifier = provider.NewCLIClassifier(provider.ClassifierConfig{
							Model:          newModel,
							SystemPrompt:   newSysPrompt,
							HomeDir:        homeDir,
							DataDir:        dataDir,
							Credential:     cliProvider.Credential,
							IdleTimeout:    60 * time.Minute,
							EmptyMCPConfig: cliProvider.EmptyMCPConfig,
						})
						go func() {
							if err := cliClassifier.Start(); err != nil {
								log.Printf("classifier: restart failed: %v", err)
							}
						}()
					}
				}
				// Re-publish Telegram bot command menu so newly-enabled or
				// renamed force-command tiers appear in `/` autocomplete.
				if telegramEnabled {
					go refreshTelegramCommands(tg, tierStore)
				}
				if git != nil {
					git.Commit("tiers updated via CC")
				}
			case cc.ReloadSkills:
				if err := skillStore.Reload(); err != nil {
					log.Printf("skills reload error: %v", err)
				} else {
					injectAppTriggers(skillStore, filepath.Join(dataDir, "apps"))
					if err := skills.MirrorInto(skillStore, capRegistry); err != nil {
						log.Printf("skills: capability mirror (reload): %v", err)
					}
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
			case cc.ReloadClaudeModels:
				if err := claudeModelsStore.Reload(); err != nil {
					log.Printf("ERROR: claude_models reload failed: %v", err)
				} else {
					log.Printf("claude_models reloaded (%d entries)", len(claudeModelsStore.Current()))
				}
				if eventBroker != nil {
					eventBroker.Emit(cc.EventClaudeModels)
				}
				if git != nil {
					git.Commit("claude_models updated via CC")
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
					registerCodex(registry, dataDir, tiersTimeout, vaultMgr, alfCred)
					// Dedup thresholds are no longer tunable at runtime — the
					// legacy memstore.Store.SetDedupConfig path is gone (#337
					// close-out). dedup.Options.NearDupThreshold is set at
					// Extractor/Consolidator wire time; a restart is required
					// to change it.
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
						if fb := cc.DefaultFallbackModel(tierStore.Current()); fb != "" {
							if !strings.Contains(fb, "/") {
								fb = "anthropic/" + fb
							}
							newModel = fb
						}
					}
					routerModel = newModel
					if cliClassifier != nil {
						cliClassifier.Close()
						cliClassifier = nil
					}
				} else {
					newModel := resolveModel(tierStore.Current().RouterModel)
					if newModel == "" {
						newModel = resolveModel("haiku")
					}
					routerModel = newModel
					if cliClassifier != nil {
						// Rebuild the classifier system prompt so the
						// persistent subprocess sees the fresh tier catalog
						// (#332).
						newSysPrompt := classifier.BuildSystemPrompt(tierStore.Current(), dataDir, configDir, agentTeamsForRouter())
						if err := cliClassifier.UpdateSystemPrompt(newSysPrompt); err != nil {
							log.Printf("classifier: UpdateSystemPrompt failed: %v", err)
						}
						if err := cliClassifier.UpdateModel(newModel); err != nil {
							log.Printf("classifier: UpdateModel failed: %v", err)
						}
					}
				}
				// Re-publish Telegram bot command menu so newly-enabled or
				// renamed force-command tiers appear in `/` autocomplete.
				if telegramEnabled {
					go refreshTelegramCommands(tg, tierStore)
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
					injectAppTriggers(skillStore, filepath.Join(dataDir, "apps"))
					if err := skills.MirrorInto(skillStore, capRegistry); err != nil {
						log.Printf("skills: capability mirror (reload): %v", err)
					}
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
			case cc.ReloadClaudeModels:
				if err := claudeModelsStore.Reload(); err != nil {
					log.Printf("ERROR: claude_models reload failed: %v", err)
				} else {
					log.Printf("claude_models reloaded (%d entries)", len(claudeModelsStore.Current()))
				}
				if eventBroker != nil {
					eventBroker.Emit(cc.EventClaudeModels)
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
				go handleReaction(tg, mr.Chat.ID, mr.MessageID, emoji, contextDir, dataDir, chatSessions, tierStore, alfMsgIDs, eventLog, cliProvider, memStore, commEngine)
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

			log.Printf("← %s: (%d chars)", u.Message.From.Username, len(u.Message.Text))
			stats.RecordMessage()

			// Record user message in chat history buffer (for GIF/media context).
			userText := u.Message.Text
			if userText == "" {
				userText = u.Message.Caption
			}
			if userText != "" {
				chatHistory.Add(u.Message.Chat.ID, "user", userText)
			}

			// Extract reply context if this is a quoted reply.
			isReply := u.Message.ReplyToMessage != nil

			// message_in is now logged centrally by comms.Process()

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
					log.Printf("voice: transcribed %d chars (%s)", len(result.Text), result.Language)
				}
			} else if hasVoice && transcriber == nil {
				tg.SendHTML(u.Message.Chat.ID, "Voice messages are not supported yet. Please send text.")
				continue
			}

			// Handle media messages: download and save for Claude to read.
			var mediaCleanup func()
			var mediaEntries []comms.MediaEntry
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
						mediaDir := filepath.Join(dataDir, "media")
						os.MkdirAll(mediaDir, 0o755)
						tmpFile, err := os.CreateTemp(mediaDir, "alf-media-*"+ext)
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
											log.Printf("video audio: transcribed %d chars (%s)", len(transcript), result.Language)
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
							mediaEntries = append(mediaEntries, comms.MediaEntry{
								Type: "photo", FileName: f.FileName, MimeType: mimeType, TempPath: tmpPath,
							})
						} else if media.IsTextContent(mimeType) || mimeType == "application/pdf" {
							textContent := media.ExtractTextFromDocument(data, mimeType)
							allParts = append(allParts, fmt.Sprintf("[FILE from Telegram chat: %s]\nContent:\n%s", f.FileName, textContent))
							mediaEntries = append(mediaEntries, comms.MediaEntry{
								Type: "document", FileName: f.FileName, MimeType: mimeType,
								TempPath: tmpPath, TextContent: textContent,
							})
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
				if handleCommand(tg, u.Message, commEngine, magic, ccExternalURL, allowedChatIDs, orch) {
					continue
				}
				// Check for force command: /<tier_name> <message>
				parts := strings.SplitN(cmdSource, " ", 2)
				cmdName := strings.TrimPrefix(parts[0], "/")
				for _, t := range tierStore.Current().Tiers {
					if t.ForceCommand && (t.Name == cmdName || cc.SanitizeTierCommand(t.Name) == cmdName) {
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

			// --- Unified engine processing ---
			tgChatID := u.Message.Chat.ID
			tgChannelID := comms.ChannelID(fmt.Sprintf("tg:%d", tgChatID))

			msgWithReplyContext := buildMessageContent(u.Message)
			routerMsg := buildRouterMessage(u.Message)

			msg := comms.InMessage{
				ChannelID:  tgChannelID,
				Text:       msgWithReplyContext,
				RawText:    userText,
				RouterText: routerMsg,
				IsReply:    isReply,
				ForcedTier: forcedTierName,
				ConvID:     fmt.Sprintf("tg-%d", tgChatID),
				Source:     "telegram",
				Media:      mediaEntries,
			}
			if isReply {
				msg.ReplyTo = extractReplyContext(u.Message)
			}

			// Signal socket for mid-response reactions from Claude subprocess.
			sigSockPath := filepath.Join(dataDir, fmt.Sprintf("signal-%d.sock", u.Message.MessageID))
			sigServer := &signal.Server{
				TG: tg, ChatID: tgChatID, MessageID: u.Message.MessageID,
				Notify: func(text string) {
					// Detect media URLs and use appropriate TG method.
					if err := sendTGNotify(tg, tgChatID, text); err != nil {
						log.Printf("signal: notify to TG failed: %v", err)
					}
				},
			}
			var sigLn net.Listener
			if ln, err := sigServer.ListenUnix(sigSockPath); err != nil {
				log.Printf("signal: listen error: %v", err)
			} else {
				sigLn = ln
				go sigServer.Serve(sigLn)
				msg.Env = append(msg.Env, "ALF_SIGNAL_SOCK="+sigSockPath)
			}

			// Typing indicator (managed by tgAdapter.OnEvent).
			tg.SendChatAction(tgChatID, "typing")
			indicator := newTypingIndicator(tg, tgChatID, "choose_sticker")
			tgAdapt.SetIndicator(tgChannelID, indicator)

			// Capture loop variables for goroutine.
			tgMsg := u.Message
			tgMediaCleanup := mediaCleanup

			go func() {
				tgChatSem <- struct{}{} // serialize per-chat
				defer func() { <-tgChatSem }()
				defer func() {
					indicator.Stop()
					tgAdapt.ClearIndicator(tgChannelID)
					if sigLn != nil {
						sigLn.Close()
						os.Remove(sigSockPath)
					}
				}()

				// Per-message budget: cap the handler at 2× the provider timeout so
				// that retry + one fallback cannot extend indefinitely. Without a
				// deadline, a mid-stream provider kill leaves the pipeline running
				// retry/fallback on context.Background() (both gated on ctx.Err()==nil),
				// which holds the global tgChatSem and makes the bot appear frozen
				// to the next Telegram message until restart (issue #253).
				msgBudget := 2 * tiersTimeout
				if msgBudget <= 0 {
					msgBudget = 20 * time.Minute
				}
				msgCtx, msgCancel := context.WithTimeout(context.Background(), msgBudget)
				defer msgCancel()

				result, err := commEngine.Process(msgCtx, msg)

				if err != nil {
					log.Printf("engine error: %v", err)
					tg.SendHTML(tgChatID, fmt.Sprintf("Error: %v", err))
					return
				}

				reply := result.Text

				// Suppress internal fallback messages.
				if reply == "Done (no text output)." {
					log.Printf("suppressing empty response for chat %d", tgChatID)
					return
				}

				// Detect Claude not logged in.
				lower := strings.ToLower(reply)
				if strings.Contains(lower, "not logged in") || strings.Contains(lower, "authenticate") || strings.Contains(lower, "login required") {
					reply = "Not logged in \u00b7 Go to the Terminal tab, type `claude`, then run `/login` to authenticate. Type `/exit` when done."
				}

				// React to the user's message before sending the reply.
				maybeSpontaneousReact(tg, tgMsg.Chat.ID, tgMsg.MessageID, result.Reaction, contextDir)

				// Append footer with tier and active skills.
				if cfg.ShowSkillFooter == nil || *cfg.ShowSkillFooter {
					var footerParts []string
					if result.Tier != "" {
						footerParts = append(footerParts, "["+result.Tier+"]")
					}
					if len(result.Skills) > 0 {
						footerParts = append(footerParts, strings.Join(result.Skills, ", "))
					}
					if len(footerParts) > 0 {
						reply += "\n\n\u2699\ufe0f " + strings.Join(footerParts, " · ")
					}
				}

				if msgID, err := tg.SendMessageReturnID(tgChatID, reply); err == nil && msgID != 0 {
					alfMsgIDs.Add(msgID)
					chatHistory.Add(tgChatID, "alf", reply)
					log.Printf("tracking alf msg %d (buffer=%d)", msgID, alfMsgIDs.Size())
				}

				// Schedule media cleanup.
				if tgMediaCleanup != nil {
					go func() {
						time.Sleep(10 * time.Minute)
						tgMediaCleanup()
					}()
				}
			}()
		}
	}
}

// refreshTelegramCommands registers the Telegram bot command menu used for the
// `/` autocomplete popup. It combines the fixed built-in commands with one
// entry per enabled force-command tier (e.g. `/sonnet`, `/haiku`) so users can
// discover and invoke tier overrides directly from Telegram.
//
// Safe to call from any goroutine; intended to be invoked at daemon startup
// and again on every ReloadTiers event so the menu stays in sync with the
// currently-loaded tier configuration.
func refreshTelegramCommands(tg *tgclient.Client, tierStore cc.TierStore) {
	if tg == nil {
		return
	}
	cmds := []tgclient.BotCommand{
		{Command: "new", Description: "Start a new conversation"},
		{Command: "clear", Description: "Clear and start a new session"},
		{Command: "help", Description: "Show available commands"},
		{Command: "skills", Description: "List active skills"},
		{Command: "bash", Description: "Execute a bash command"},
		{Command: "jobs", Description: "List running agent jobs"},
		{Command: "cancel", Description: "Cancel all running jobs"},
		{Command: "login", Description: "Get a Control Center login link"},
	}
	if tc := tierStore.Current(); tc != nil {
		for _, t := range tc.Tiers {
			if !t.Enabled || !t.ForceCommand {
				continue
			}
			// Telegram rejects the whole batch with BOT_COMMAND_INVALID if any
			// command name doesn't match ^[a-z0-9_]{1,32}$ — hyphens (common
			// in tier names like "codex-fast") are not allowed. Sanitize the
			// name for the menu; the backend matchers accept both the raw
			// tier name and its sanitized alias.
			cmdName := cc.SanitizeTierCommand(t.Name)
			if cmdName == "" {
				log.Printf("[telegram] skipping tier %q from bot menu (no valid command chars)", t.Name)
				continue
			}
			desc := fmt.Sprintf("Force reply from %s tier", t.Name)
			if t.Model != "" {
				desc = fmt.Sprintf("Force reply from %s (%s)", t.Name, t.Model)
			}
			cmds = append(cmds, tgclient.BotCommand{
				Command:     cmdName,
				Description: desc,
			})
		}
	}
	if err := tg.SetMyCommands(cmds); err != nil {
		log.Printf("[telegram] setMyCommands: %v", err)
	}
}

// resolveEmbedder picks the best available embedder implementation.
// Priority: 1) tier profile memory.embedding, 2) legacy tier embedding, 3) EMBED_URL env, 4) nil (FTS5-only).
func resolveEmbedder(tierStore cc.TierStore) memory.Embedder {
	// Use a stable instance ID so re-registrations reuse the same slot in the
	// embed-server token map. Docker hostnames change on every container restart,
	// which would leak slots and eventually hit the 50-instance cap.
	const embedInstanceID = "alf-daemon"
	secret := envsecrets.ReadSecret("EMBED_SHARED_SECRET")

	if tc := tierStore.Current(); tc != nil {
		// 1. New: memory.embedding config.
		if tc.Memory != nil && tc.Memory.Embedding != nil && tc.Memory.Embedding.URL != "" {
			emb := memory.NewHTTPEmbedder(tc.Memory.Embedding.URL, embedInstanceID, secret, 30*time.Second)
			go startHTTPEmbedder(emb)
			log.Printf("memstore: using HTTP embedder from memory config (url=%s)", tc.Memory.Embedding.URL)
			return emb
		}
		// 2. Legacy: embedding config at tier root (backward compat).
		if tc.Embedding != nil && tc.Embedding.URL != "" {
			emb := memory.NewHTTPEmbedder(tc.Embedding.URL, embedInstanceID, secret, 30*time.Second)
			go startHTTPEmbedder(emb)
			log.Printf("memstore: using HTTP embedder from tier config (url=%s)", tc.Embedding.URL)
			return emb
		}
	}

	// 2. From env var (embed sidecar container, same pattern as whisper).
	if url := os.Getenv("EMBED_URL"); url != "" {
		emb := memory.NewHTTPEmbedder(url, embedInstanceID, secret, 30*time.Second)
		go startHTTPEmbedder(emb)
		log.Printf("memstore: using HTTP embedder (url=%s)", url)
		return emb
	}

	// 3. Disabled — no embed service configured.
	log.Println("memstore: embedder disabled (EMBED_URL not set)")
	return nil
}

// startHTTPEmbedder registers with the embed service, retrying up to 30 times.
// Falls back to FTS5-only search if embed service is unavailable.
// Gives up early on "no route to host" / "connection refused" (service not deployed).
func startHTTPEmbedder(emb *memory.HTTPEmbedder) {
	for attempt := 1; attempt <= 30; attempt++ {
		err := emb.Start()
		if err == nil {
			return
		}
		errStr := err.Error()
		unreachable := strings.Contains(errStr, "no route to host") ||
			strings.Contains(errStr, "connection refused")
		if unreachable && attempt >= 3 {
			log.Printf("embed: service unreachable after %d attempts — falling back to FTS5-only search", attempt)
			return
		}
		if attempt <= 3 || attempt%10 == 0 {
			log.Printf("embed: registration attempt %d/30 failed: %v", attempt, err)
		}
		time.Sleep(10 * time.Second)
	}
	log.Println("embed: gave up registering after 30 attempts — falling back to FTS5-only search")
}
