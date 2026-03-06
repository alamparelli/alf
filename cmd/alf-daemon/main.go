package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	if token == "" || chatID == "" {
		// Log diagnostic info to help users debug secrets issues.
		log.Println("ERROR: TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required")
		for _, name := range []string{"TELEGRAM_BOT_TOKEN", "TELEGRAM_CHAT_ID"} {
			filePath := os.Getenv(name + "_FILE")
			if filePath == "" {
				log.Printf("  %s_FILE env var: not set", name)
			} else if _, err := os.Stat(filePath); err != nil {
				log.Printf("  %s_FILE=%s: file not found", name, filePath)
			} else {
				data, _ := os.ReadFile(filePath)
				if strings.TrimSpace(string(data)) == "" {
					log.Printf("  %s_FILE=%s: file exists but is empty", name, filePath)
				} else {
					log.Printf("  %s_FILE=%s: file exists with content", name, filePath)
				}
			}
			if v := os.Getenv(name); v != "" {
				log.Printf("  %s env var: set", name)
			} else {
				log.Printf("  %s env var: not set", name)
			}
		}
		log.Fatal("Exiting. Ensure secrets are configured — see https://github.com/alamparelli/alf#secrets")
	}

	// Verify claude CLI is available.
	if _, err := exec.LookPath("claude"); err != nil {
		log.Fatal("claude CLI not found in PATH")
	}

	// Data directory for logs, sessions, context, etc.
	dataDir := "/home/node/data"
	if d := os.Getenv("ALF_DATA_DIR"); d != "" {
		dataDir = d
	}

	// Config directory (RW for CC, separate from data volume).
	configDir := "/opt/alf/config"
	if d := os.Getenv("ALF_CONFIG_DIR"); d != "" {
		configDir = d
	}

	// Skills directory (RW for CC, separate from data volume).
	skillsDir := "/opt/alf/skills"
	if d := os.Getenv("ALF_SKILLS_DIR"); d != "" {
		skillsDir = d
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
	allowedRaw := readSecret("ALLOWED_CHAT_IDS")
	if allowedRaw == "" {
		allowedRaw = chatID
	}
	allowedChatIDs := parseAllowedChatIDs(allowedRaw)

	// Shared stats for CC status endpoint.
	stats := cc.NewStats()

	// Reload channel: CC writes, daemon reads.
	reloadCh := make(chan cc.ReloadEvent, 4)

	// Magic link auth stores (shared between CC and daemon).
	magic := cc.NewMagicStore(nil)
	magic.StartCleanup()
	sessions := cc.NewFileSessionStore(filepath.Join(configDir, "sessions.json"), nil)
	sessions.StartCleanup()
	magic.SetSessionStore(sessions)

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
	os.MkdirAll(filepath.Join(dataDir, "logs", "events"), 0o755)
	os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o755)
	for _, sub := range []string{"config", "tools", "skills", "context", "pages"} {
		os.MkdirAll(filepath.Join(dataDir, sub), 0o755)
	}
	os.MkdirAll(filepath.Join(configDir, "agents"), 0o755)

	// Populate tools.d/ with symlinks to each system tool in /opt/alf/tools/.
	// The host volume mount overwrites any Dockerfile-created symlinks,
	// so we link individual tools at runtime instead.
	linkSystemTools(filepath.Join(dataDir, "tools.d"), "/opt/alf/tools")

	// Ensure Claude Code finds its native binary at $HOME/.local/bin/claude.
	// The volume mount overwrites any Dockerfile-created structure.
	if claudePath, err := exec.LookPath("claude"); err == nil {
		localBin := filepath.Join(dataDir, ".local", "bin")
		os.MkdirAll(localBin, 0o755)
		link := filepath.Join(localBin, "claude")
		os.Remove(link) // remove stale symlink
		if err := os.Symlink(claudePath, link); err == nil {
			log.Printf("linked %s → %s", link, claudePath)
		}
	}

	// Fix directory permissions so the claude subprocess (uid 1001, gid 1000)
	// can read/write files created before the permission refactoring.
	fixDataPermissions(dataDir)
	fixDataPermissions(configDir)

	// Migrate config from old data/config/ to configDir (before loading).
	migrateConfig(dataDir, configDir)

	// Run user setup script if modified since last run.
	runSetupScript(dataDir)

	// Generate llms.txt index of available documentation.
	writeLLMSIndex(dataDir)

	// Load initial config.
	configStore := cc.NewFileConfigStore(cc.ConfigPath(configDir))
	cfg, err := configStore.Load()
	if err != nil {
		log.Printf("warning: failed to load config: %v", err)
		cfg = cc.DefaultConfig()
	}
	// Load initial tiers config.
	tierStore := cc.NewFileTierStore(cc.TiersPath(configDir))
	if err := tierStore.Reload(); err != nil {
		log.Printf("warning: failed to load tiers: %v", err)
	}

	// Load skill catalog: system → bundled copy → user (later overrides earlier).
	skillStore := skills.NewFileSkillStore(skillsDir, filepath.Join(dataDir, "skills.d"), filepath.Join(dataDir, "skills"))

	// Load agent team configurations.
	agentStore := agents.NewFileAgentStore(filepath.Join(configDir, "agents"))

	// Set process-wide timezone from config so log timestamps are correct.
	time.Local = resolveTimezone(cfg.Timezone)

	// Bootstrap default memory files (soul.md, mood.md, index.md).
	contextDir := filepath.Join(dataDir, "context")
	memory.Bootstrap(contextDir)

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

	// Claude subprocess credential (run as claude user uid 1001, gid 1000/node).
	claudeCred := &syscall.Credential{Uid: 1001, Gid: 1000}

	// Provider: spawn-per-call Claude CLI for responses.
	tiersTimeout := time.Duration(cfg.TiersTimeout) * time.Second // 0 → default 5m inside NewCLIProvider
	cliProvider := provider.NewCLIProvider(dataDir, tiersTimeout, claudeCred)

	// Multi-agent orchestrator.
	orch := agents.NewOrchestrator(cliProvider, agentStore, dataDir, router.ResolveModel)

	// Router model for message classification.
	routerModel := router.ResolveModel(tierStore.Current().RouterModel)
	if routerModel == "" {
		routerModel = router.ResolveModel("haiku")
	}

	// classifyMessage spawns a Claude CLI process per classification.
	classifyMessage := func(message string, tiers *cc.TiersConfig) router.Result {
		prompt := router.BuildClassifyPrompt(router.ClassifyInput{
			Message:   message,
			Tiers:     tiers,
			DataDir:   dataDir,
			ConfigDir: configDir,
		})
		params := provider.Params{
			Model:    routerModel,
			MaxTurns: 2,
			DataDir:  dataDir,
		}
		start := time.Now()
		result, err := cliProvider.Invoke(context.Background(), prompt, params, nil)
		if err != nil {
			log.Printf("router: classify error: %v", err)
			return router.FallbackResult(tiers)
		}
		log.Printf("router: classify took %dms", time.Since(start).Milliseconds())
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
	chatService.SkillStore = skillStore
	if memDB != nil {
		chatService.Recaller = &memStoreRecaller{store: memDB}
	}

	// Start Control Center HTTP server.
	if authToken != "" || len(allowedChatIDs) > 0 {
		server, err := cc.New(dataDir, configDir, skillsDir, stats, version, authToken, ccExternalURL, cfg, reloadCh, magic, sessions, chatService, memDB, cliProvider)
		if err != nil {
			log.Printf("warning: failed to start Control Center: %v", err)
		} else {
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

	// Telegram client for sending formatted messages.
	tg := tgclient.NewClient(token)
	tg.HTTP = client

	// Auto-update checker (initialized here, scheduled via unified scheduler below).
	var uc *updater.Checker
	if cfg.AutoUpdateCheck {
		image := os.Getenv("ALF_IMAGE")
		if image == "" {
			image = "ghcr.io/alamparelli/alf"
		}
		notifyFn := func(current, latest string) {
			log.Printf("update available: %s → %s", current, latest)
			if cfg.AutoUpdateNotify && token != "" && chatID != "" {
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
		DataDir:    dataDir,
		ContextDir: contextDir,
		ChatID:     parsedChatID,
		TG:         tg,
		Provider:   &schedulerProvider{p: cliProvider},
		TierStore:  &schedulerTierStore{ts: tierStore},
		SkillStore: &schedulerSkillStore{s: skillStore},
		ChatLogger: &schedulerChatLogger{store: chatStore},
		CronPath:   filepath.Join(configDir, "cron.json"),
		Location:   schedLocation,
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
			"haiku_r",
			"Run a full security audit. Read all files in /home/node/data/skills.d/, /home/node/data/skills/, /home/node/data/tools.d/, and /home/node/data/tools/. Follow the security-audit skill instructions to produce a structured report.",
			"telegram",
			[]string{"security-audit"},
		); err != nil {
			log.Printf("warning: failed to seed security-audit job: %v", err)
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
					if cfg.Timezone != oldTZ {
						time.Local = resolveTimezone(cfg.Timezone)
						log.Printf("config: timezone changed to %q (logs updated, scheduler needs restart)", cfg.Timezone)
					}
					log.Printf("config reloaded: log_level=%s session_timeout=%dm timezone=%s", cfg.LogLevel, cfg.SessionTimeout, cfg.Timezone)
				}
				if git != nil {
					git.Commit("config updated via CC")
				}
			case cc.ReloadTiers:
				log.Println("tiers reloaded")
				newModel := router.ResolveModel(tierStore.Current().RouterModel)
				if newModel == "" {
					newModel = router.ResolveModel("haiku")
				}
				routerModel = newModel
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
					log.Printf("agents reloaded (%d teams)", len(agentStore.All()))
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

			// Extract reply context if this is a quoted reply.
			isReply := u.Message.ReplyToMessage != nil
			repliedToID := int64(0)
			if isReply {
				repliedToID = u.Message.ReplyToMessage.MessageID
			}

			// Note: hasText, hasMedia, hasVoice already determined above

			truncated := u.Message.Text
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
			var forcedTierName string
			if strings.HasPrefix(u.Message.Text, "/") {
				if handleCommand(tg, u.Message, chatSessions, eventLog, magic, ccExternalURL, allowedChatIDs, contextDir) {
					continue
				}
				// Check for force command: /<tier_name> <message>
				parts := strings.SplitN(u.Message.Text, " ", 2)
				cmdName := strings.TrimPrefix(parts[0], "/")
				for _, t := range tierStore.Current().Tiers {
					if t.Enabled && t.ForceCommand && t.Name == cmdName {
						if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
							tg.SendHTML(u.Message.Chat.ID, fmt.Sprintf("Usage: <code>/%s &lt;message&gt;</code>", t.Name))
							forcedTierName = "_skip" // signal to skip this update
						} else {
							forcedTierName = t.Name
							u.Message.Text = strings.TrimSpace(parts[1])
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

			// Show routing status immediately (silent, will be deleted).
			tg.SendChatAction(chatID, "typing")
			routingBase := pickRandom(statusRouting)
			routingMsgID, _ := tg.SendMessageGetID(chatID, routingBase+".")

			// Animate dots on routing message while classifying.
			routingAnim := newDotAnimator(tg, chatID, routingMsgID, routingBase, "typing")

			// Build complete message content including media captions and reply context.
			msgWithReplyContext := buildMessageContent(u.Message)
			// Build a short version for the router (user text + brief quote hint, no full quoted text).
			routerMsg := buildRouterMessage(u.Message)

			// Pre-route memory recall: check long-term store BEFORE routing
			// so instant-tier responses also have personal context.
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
				if routingMsgID != 0 {
					tg.DeleteMessage(chatID, routingMsgID)
				}
				routeResult = router.Result{Tier: forcedTierName, Reason: "force_command"}
				log.Printf("→ force command → tier %q", forcedTierName)
			} else if hasMedia {
			// Media messages bypass the router — they need a full Claude Code
			// session with Read tool access to view images/files.
				routingAnim.Stop()
				if routingMsgID != 0 {
					tg.DeleteMessage(chatID, routingMsgID)
				}
				// Pick the lowest-priority enabled non-instant tier for media.
				// Instant tiers (haiku) can't meaningfully respond to images/GIFs.
				tierName := ""
				bestPriority := int(^uint(0) >> 1) // max int
				for _, t := range tierStore.Current().Tiers {
					log.Printf("media tier scan: %s priority=%d enabled=%v instant=%v", t.Name, t.Priority, t.Enabled, t.Instant)
					if t.Enabled && !t.Instant && t.Priority < bestPriority {
						tierName = t.Name
						bestPriority = t.Priority
					}
				}
				if tierName == "" && len(tierStore.Current().Tiers) > 0 {
					tierName = tierStore.Current().Tiers[0].Name
				}
				routeResult = router.Result{Tier: tierName, Reason: "media bypass"}
				log.Printf("→ media detected, bypassing router → tier %q", tierName)
			} else {
				routeResult = classifyMessage(routerMsg, tierStore.Current())
			}

			// Router answered directly — no second LLM call needed.
			if forcedTierName == "" && !hasMedia {
				routingAnim.Stop()
			}

			// Quote-reply upgrade: replies carry important context that instant
			// tiers cannot handle well (no conversation history). Upgrade to
			// the default fallback tier so the quoted message gets proper treatment.
			if isReply && forcedTierName == "" {
				upgraded := false
				if routeResult.Response != "" && routeResult.Tier == "" {
					// Direct response → upgrade to tier.
					fallback := tierStore.Current().DefaultFallback
					if fallback == "" {
						fallback = "haiku_r"
					}
					routeResult = router.Result{Tier: fallback, Reason: "reply-upgrade: direct→" + fallback}
					upgraded = true
				} else if routeResult.Tier != "" {
					// Check if routed to an instant tier → upgrade.
					for _, t := range tierStore.Current().Tiers {
						if t.Name == routeResult.Tier && t.Instant {
							fallback := tierStore.Current().DefaultFallback
							if fallback == "" {
								fallback = "haiku_r"
							}
							routeResult = router.Result{Tier: fallback, Reason: fmt.Sprintf("reply-upgrade: %s→%s", t.Name, fallback)}
							upgraded = true
							break
						}
					}
				}
				if upgraded {
					log.Printf("→ reply detected, upgrading tier → %s", routeResult.Tier)
				}
			}

			// If highly relevant memories were recalled (distance < 0.6), override
			// instant responses — the user is asking about something personal.
			if preRecallBlock != "" && recallBestDist < 0.6 && routeResult.Response != "" && routeResult.Tier == "" {
				log.Printf("→ memory override: instant response upgraded to tier (best_dist=%.2f)", recallBestDist)
				fallback := tierStore.Current().DefaultFallback
				if fallback == "" {
					fallback = "haiku_r"
				}
				routeResult = router.Result{Tier: fallback, Reason: "memory-override: instant→" + fallback}
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
				// Delete routing status message.
				if routingMsgID != 0 {
					tg.DeleteMessage(chatID, routingMsgID)
				}
				eventLog.Log("router_direct", map[string]any{
					"chat_id":          chatID,
					"reason":           routeResult.Reason,
					"project_context":  filepath.Join(".claude/projects", fmt.Sprintf("%d", chatID)),
				})
				chatSessions.TouchContext(chatID, "router")
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

			// Resolve tier to params.
			tp = resolveTierParams(routeResult.Tier, tierStore.Current())

			eventLog.Log("router_classify", map[string]any{
				"chat_id":          chatID,
				"tier":             routeResult.Tier,
				"reason":           routeResult.Reason,
				"model":            tp.Model,
				"project_context":  filepath.Join(".claude/projects", fmt.Sprintf("%d", chatID)),
			})

			// Orchestrator dispatch: delegate to multi-agent coordinator.
			if routeResult.Tier == "orchestrator" && len(agentStore.All()) > 0 {
				// Build system prompts (same as normal path).
				sysPrompts := memory.CollectPrompts(contextDir)
				var orchSysPrompts []string
				for i := 0; i < len(sysPrompts)-1; i += 2 {
					if sysPrompts[i] == "--append-system-prompt" {
						orchSysPrompts = append(orchSysPrompts, sysPrompts[i+1])
					}
				}
				if preRecallBlock != "" {
					orchSysPrompts = append(orchSysPrompts, preRecallBlock)
				}
				if catalog := skills.BuildCatalog(skillStore); catalog != "" {
					orchSysPrompts = append(orchSysPrompts, catalog)
				}

				// Status animation for orchestrator.
				orchTag := "[orchestrator] "
				if routingMsgID != 0 {
					tg.EditMessage(chatID, routingMsgID, orchTag+"Coordinating agents...")
				}
				orchAnim := newDotAnimator(tg, chatID, routingMsgID, orchTag+"Coordinating agents", "choose_sticker")

				orchProgress := func(phase, detail string) {
					switch phase {
					case "thinking":
						orchAnim.SetPhase(orchTag+"Thinking", "choose_sticker")
					case "agent":
						orchAnim.SetPhase(orchTag+"Agent: "+detail, "upload_document")
					}
				}

				start := time.Now()
				orchResult, orchMeta, orchErr := orch.Run(context.Background(), msgWithReplyContext, orchSysPrompts, orchProgress)
				duration := time.Since(start)

				orchAnim.Stop()
				if routingMsgID != 0 {
					tg.DeleteMessage(chatID, routingMsgID)
				}

				if orchErr != nil {
					log.Printf("orchestrator error: %v", orchErr)
					tg.SendHTML(chatID, fmt.Sprintf("Orchestrator error: %v", orchErr))
					eventLog.Log("orchestrator_error", map[string]any{
						"chat_id":     chatID,
						"error":       orchErr.Error(),
						"iterations":  orchMeta.Iterations,
						"total_cost":  orchMeta.TotalCost,
						"duration_ms": duration.Milliseconds(),
					})
					continue
				}

				log.Printf("→ orchestrator %dms %d iterations $%.4f", duration.Milliseconds(), orchMeta.Iterations, orchMeta.TotalCost)

				eventLog.Log("orchestrator_out", map[string]any{
					"chat_id":      chatID,
					"iterations":   orchMeta.Iterations,
					"total_cost":   orchMeta.TotalCost,
					"agent_calls":  len(orchMeta.AgentCalls),
					"duration_ms":  duration.Milliseconds(),
					"text_length":  len(orchResult),
					"task_id":      orchMeta.ID,
				})

				if msgID, err := tg.SendMessageReturnID(chatID, orchResult); err == nil && msgID != 0 {
					alfMsgIDs.Add(msgID)
					chatHistory.Add(chatID, "alf", orchResult)
				}
				if mediaCleanup != nil {
					cleanup := mediaCleanup
					go func() {
						time.Sleep(10 * time.Minute)
						cleanup()
					}()
				}
				continue
			}

			// Transition routing message into processing status message.
			tierTag := "[" + routeResult.Tier + "] "
			var statusAnim *dotAnimator
			if routingMsgID != 0 {
				thinkBase := tierTag + pickRandom(statusThinking)
				tg.EditMessage(chatID, routingMsgID, thinkBase+dotFrames[0])
				statusAnim = newDotAnimator(tg, chatID, routingMsgID, thinkBase, "choose_sticker")
			}

			lastPhase := ""
			onProgress := func(event provider.StreamEvent) {
				if statusAnim == nil {
					return
				}
				if event.Type == lastPhase {
					return
				}
				lastPhase = event.Type
				switch event.Type {
				case "thinking":
					statusAnim.SetPhase(tierTag+pickRandom(statusThinking), "choose_sticker")
				case "tool_use":
					statusAnim.SetPhase(tierTag+pickRandom(statusToolUse), "upload_document")
				case "text":
					statusAnim.SetPhase(tierTag+pickRandom(statusWriting), "typing")
				}
			}

			// Build system prompts (context files + reaction instruction).
			sysPrompts := memory.CollectPrompts(contextDir)
			var sysPromptTexts []string
			for i := 0; i < len(sysPrompts)-1; i += 2 {
				if sysPrompts[i] == "--append-system-prompt" {
					sysPromptTexts = append(sysPromptTexts, sysPrompts[i+1])
				}
			}
			// Inject pre-recalled memories (computed before routing).
			if preRecallBlock != "" {
				sysPromptTexts = append(sysPromptTexts, preRecallBlock)
			}
			// Inject onboarding prompt on first use.
			onboarding := memory.OnboardingPrompt(contextDir)
			if onboarding != "" {
				sysPromptTexts = append(sysPromptTexts, onboarding)
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
			sysPromptTexts = append(sysPromptTexts, fmt.Sprintf(reactionSystemPromptTmpl, mood.AllowedReactionList()))

			// Documentation index — lets the model discover and read docs.
			if _, err := os.Stat(filepath.Join(dataDir, "llms.txt")); err == nil {
				sysPromptTexts = append(sysPromptTexts, "Documentation is available in ~/data/docs/. Read ~/data/llms.txt for the index. When you install packages, read the container-packages doc first.")
			}

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
			result, err := cliProvider.Invoke(context.Background(), msgWithReplyContext, invokeParams, onProgress)
			// Retry without resume if session not found.
			if err != nil && resumeID != "" && strings.Contains(err.Error(), "No conversation found") {
				log.Printf("session %s expired, starting fresh", resumeID)
				chatSessions.Archive(chatID)
				invokeParams.ResumeID = ""
				result, err = cliProvider.Invoke(context.Background(), msgWithReplyContext, invokeParams, onProgress)
			}
			duration := time.Since(start)

			// Cleanup signal socket immediately (defer won't fire in this loop).
			if sigLn != nil {
				sigLn.Close()
				os.Remove(sigSockPath)
			}

			// Cleanup: stop animation, delete status msg.
			if statusAnim != nil {
				statusAnim.Stop()
				tg.DeleteMessage(chatID, routingMsgID)
			}

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
			if onboarding != "" {
				memory.ClearOnboarding(contextDir)
			}

			// Store the session ID returned by Claude for future --resume.
			if result.SessionID != "" {
				isNew := resumeID == ""
				chatSessions.SetWithContext(chatID, result.SessionID, routeResult.Tier)
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

			log.Printf("→ %s %dms %dt $%.4f", result.Model, duration.Milliseconds(), result.NumTurns, result.CostUSD)

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

// reactionSystemPromptTmpl is the template for the reaction instruction injected into Claude calls.
// The %s placeholder is filled with mood.AllowedReactionList().
const reactionSystemPromptTmpl = `You may optionally suggest a single emoji reaction for the user's message by starting your response with [[react:EMOJI]]. Pick an emoji that shows you understood the message — not generic thumbs up. Use [[react:none]] or omit the tag if no reaction fits. The tag will be stripped before the user sees your response.
IMPORTANT: You MUST only use one of these Telegram-allowed reaction emoji: %s`

// tierParams holds per-tier Claude CLI arguments.
type tierParams struct {
	Model        string   // full model name, e.g. "claude-sonnet-4-5"
	Tools        []string // nil = omit flag
	WriteCapable bool     // if true, grants full tool access; if false, restricts to Tools whitelist
	Effort       string   // "" = omit flag
	MaxTurns     int      // 0 = omit flag (use Claude default)
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
func handleCommand(tg *tgclient.Client, msg *Message, chatSessions *session.Store, eventLog *eventlog.Logger, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool, contextDir string) bool {
	cmd := strings.SplitN(msg.Text, " ", 2)[0]
	switch cmd {
	case "/login":
		handleLogin(tg, msg, magic, ccExternalURL, allowedChatIDs)
		return true
	case "/new":
		old := chatSessions.Archive(msg.Chat.ID)
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
		welcome := `Hey, I'm <b>Alf</b> — your personal AI assistant powered by Claude.

Send me a message to get started — I'll introduce myself and we'll get to know each other.

<b>Commands:</b>
/new — Fresh conversation
/login — Access the Control Center
/help — Show all commands`
		tg.SendHTML(msg.Chat.ID, welcome)
		return true
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
	case "/help":
		help := "<b>Available commands:</b>\n" +
			"/help — Show this message\n" +
			"/new — Start a new conversation session\n" +
			"/bash — Execute a bash command directly\n" +
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

func resolveTierParams(tierName string, tiers *cc.TiersConfig) tierParams {
	for _, t := range tiers.Tiers {
		if t.Name == tierName {
			return tierParams{
				Model:        router.ResolveModel(t.Model),
				Tools:        t.Tools,
				WriteCapable: t.WriteCapable,
				Effort:       t.Effort,
				MaxTurns:     t.MaxTurns,
			}
		}
	}
	// Tier not found — use defaults.
	return tierParams{Model: "claude-haiku-4-5"}
}

// migrateConfig copies config files from old data/config/ to configDir on first run.
// fixDataPermissions ensures all files and directories under dataDir are
// group-readable/writable so the claude subprocess (uid 1001, gid node/1000)
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

// Status message pools for natural, varied progress indicators.
// Status message pools — no trailing dots (animated separately).
var statusRouting = []string{
	"Let me think",
	"On it",
	"Hmm",
	"One sec",
	"Looking into it",
	"Give me a moment",
	"Processing",
	"Checking",
}

var statusThinking = []string{
	"🧠 Thinking",
	"🔍 Analyzing",
	"⛏ Digging in",
	"💭 Reasoning",
	"🧩 Working it out",
	"🤔 Considering",
}

var statusToolUse = []string{
	"📂 Reading files",
	"🔎 Looking things up",
	"📝 Checking the code",
	"🕵️ Investigating",
	"📚 Doing some research",
	"🗂 Gathering context",
}

var statusWriting = []string{
	"✍️ Writing",
	"📝 Drafting",
	"🔧 Putting it together",
	"⏳ Almost there",
	"🎁 Wrapping up",
}

// dotCycle returns animated dots: ".", "..", "...", "." cycling on each call.
var dotFrames = []string{".", "..", "..."}

func pickRandom(pool []string) string {
	return pool[rand.Intn(len(pool))]
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

// dotAnimator animates a Telegram status message with cycling dots and chat actions.
type dotAnimator struct {
	tg       *tgclient.Client
	chatID   int64
	msgID    int64
	base     string // current text prefix (e.g. "Thinking")
	dotIdx   int
	lastEdit time.Time
	mu       sync.Mutex
	done     chan struct{}
	action   string // current chat action (e.g. "typing")
}

// newDotAnimator creates and starts a dot animator that ticks every second.
func newDotAnimator(tg *tgclient.Client, chatID, msgID int64, base, action string) *dotAnimator {
	da := &dotAnimator{
		tg:       tg,
		chatID:   chatID,
		msgID:    msgID,
		base:     base,
		dotIdx:   1, // 0th frame already shown by caller
		lastEdit: time.Now(),
		done:     make(chan struct{}),
		action:   action,
	}
	go da.run()
	return da
}

func (da *dotAnimator) run() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-da.done:
			return
		case <-ticker.C:
			da.tick()
		}
	}
}

func (da *dotAnimator) tick() {
	da.mu.Lock()
	defer da.mu.Unlock()
	if da.msgID == 0 {
		return
	}
	da.tg.EditMessage(da.chatID, da.msgID, da.base+dotFrames[da.dotIdx%len(dotFrames)])
	da.dotIdx++
	da.lastEdit = time.Now()
	da.tg.SendChatAction(da.chatID, da.action)
}

// SetPhase changes the status text and chat action (e.g. on progress events).
func (da *dotAnimator) SetPhase(base, action string) {
	da.mu.Lock()
	defer da.mu.Unlock()
	da.base = base
	da.action = action
	da.dotIdx = 0
}

// SetAction changes only the chat action without resetting the text.
func (da *dotAnimator) SetAction(action string) {
	da.mu.Lock()
	defer da.mu.Unlock()
	da.action = action
}

// Stop halts the animation.
func (da *dotAnimator) Stop() {
	select {
	case <-da.done:
	default:
		close(da.done)
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
	// Use the instant tier for fast follow-up.
	model := "claude-haiku-4-5"
	for _, t := range tierStore.Current().Tiers {
		if t.Instant {
			m := router.ResolveModel(t.Model)
			if m != "" {
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

// runSetupScript executes data/setup.sh if it exists and has changed since last run.
// A SHA-256 hash is stored in data/.setup-hash to skip unchanged scripts.
func runSetupScript(dataDir string) {
	script := filepath.Join(dataDir, "setup.sh")
	data, err := os.ReadFile(script)
	if err != nil {
		return // no setup.sh — nothing to do
	}

	// Check hash to skip if unchanged.
	h := sha256.Sum256(data)
	currentHash := hex.EncodeToString(h[:])
	hashFile := filepath.Join(dataDir, ".setup-hash")
	if prev, err := os.ReadFile(hashFile); err == nil && strings.TrimSpace(string(prev)) == currentHash {
		log.Printf("setup: script unchanged, skipping")
		return
	}

	log.Printf("setup: running %s ...", script)
	cmd := exec.Command("bash", script)
	cmd.Dir = dataDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("setup: script failed: %v (will retry on next restart)", err)
		return // don't save hash so it retries
	}

	os.WriteFile(hashFile, []byte(currentHash), 0o644)
	log.Printf("setup: script completed successfully")
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

