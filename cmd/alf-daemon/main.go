package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/eventlog"
	"github.com/alamparelli/alf/internal/gittrack"
	"github.com/alamparelli/alf/internal/media"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memstore"
	"github.com/alamparelli/alf/internal/mood"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/router"
	"github.com/alamparelli/alf/internal/session"
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

	// CC external URL for magic link generation.
	ccExternalURL := os.Getenv("CC_EXTERNAL_URL")
	if ccExternalURL == "" {
		ccExternalURL = "http://localhost:8080"
	}

	log.Printf("alf-daemon %s starting...", version)

	// Write version file so Claude -p can read it.
	os.WriteFile(filepath.Join(dataDir, ".version"), []byte(version), 0o644)

	// Ensure directories exist.
	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(filepath.Join(dataDir, "logs", "events"), 0o755)
	os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o755)
	for _, sub := range []string{"config", "tools", "skills", "context"} {
		os.MkdirAll(filepath.Join(dataDir, sub), 0o755)
	}

	// Populate tools.d/ with symlinks to each system tool in /opt/alf/tools/.
	// The host volume mount overwrites any Dockerfile-created symlinks,
	// so we link individual tools at runtime instead.
	linkSystemTools(filepath.Join(dataDir, "tools.d"), "/opt/alf/tools")

	// Fix data directory permissions so the claude subprocess (uid 1001, gid 1000)
	// can read/write files created before the permission refactoring.
	fixDataPermissions(dataDir)

	// Migrate config from old data/config/ to configDir (before loading).
	migrateConfig(dataDir, configDir)

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
			if cfg.GitSweepInterval > 0 {
				git.SetInterval(time.Duration(cfg.GitSweepInterval) * time.Minute)
				git.StartSweep()
			}
			defer git.Stop()
			log.Printf("git tracker started (sweep=%dm)", cfg.GitSweepInterval)
		}
	}

	// Voice transcriber (persistent faster-whisper Python process).
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
			// Start persistent process in background (model loads once).
			go func() {
				if err := transcriber.Start(); err != nil {
					log.Printf("voice: failed to start whisper server: %v", err)
				}
			}()
		}
	} else {
		log.Println("voice transcription disabled (transcribe.py not found)")
	}

	// Embedding sidecar (persistent ONNX Runtime Python process).
	embedScriptPath := "/opt/alf/embed.py"
	if p := os.Getenv("ALF_EMBED_SCRIPT"); p != "" {
		embedScriptPath = p
	}
	var memDB *memstore.Store
	if memstore.IsAvailable(embedScriptPath) {
		embedder, err := memstore.NewEmbedder(embedScriptPath, "", 30*time.Second)
		if err != nil {
			log.Printf("memstore: embedder disabled: %v", err)
		} else {
			// Start synchronously — wait for model load before proceeding.
			// This avoids a race where Search falls back to FTS5 because
			// the embedder isn't ready yet.
			if err := embedder.Start(); err != nil {
				log.Printf("memstore: embedder start failed: %v", err)
			}

			memDB, err = memstore.New(filepath.Join(contextDir, "memory.db"), embedder)
			if err != nil {
				log.Printf("warning: memory store init failed: %v", err)
			} else {
				defer memDB.Close()
				sockPath := filepath.Join(contextDir, "memstore.sock")
				go memDB.ServeUnix(sockPath)
				log.Printf("memstore: ready (db=%s, socket=%s)", filepath.Join(contextDir, "memory.db"), sockPath)

				// Periodic memory extraction (every 3h).
				extractor := memstore.NewExtractor(memDB, dataDir, contextDir, 3*time.Hour, func(cmd *exec.Cmd) {
					cmd.SysProcAttr = &syscall.SysProcAttr{
						Credential: &syscall.Credential{Uid: 1001, Gid: 1000},
					}
				})
				extractor.Start()
				defer extractor.Stop()
			}
		}
	} else {
		log.Println("memstore: embedding sidecar disabled (embed.py not found)")
	}

	// Ring buffer tracking Alf's sent message IDs for reaction matching.
	alfMsgIDs := newRingBuffer(200)

	// Chat message store for mobile app API.
	chatStore := cc.NewChatStore(dataDir)

	// Claude subprocess credential (run as claude user uid 1001, gid 1000/node).
	claudeCred := &syscall.Credential{Uid: 1001, Gid: 1000}

	// Provider: spawn-per-call Claude CLI for responses.
	cliProvider := provider.NewCLIProvider(dataDir, 5*time.Minute, claudeCred)

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
	if memDB != nil {
		chatService.Recaller = &memStoreRecaller{store: memDB}
	}

	// Start Control Center HTTP server.
	if authToken != "" || len(allowedChatIDs) > 0 {
		server, err := cc.New(dataDir, configDir, skillsDir, stats, version, authToken, ccExternalURL, reloadCh, magic, sessions, chatService)
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

	// Auto-update checker.
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
		uc := updater.New(image, version, updateInterval, notifyFn)
		uc.Start()
		defer uc.Stop()
	}

	for {
		// Check for reload events (non-blocking).
		select {
		case event := <-reloadCh:
			switch event {
			case cc.ReloadConfig:
				if newCfg, err := configStore.Load(); err == nil {
					cfg = newCfg
					if cfg.SessionTimeout > 0 {
						chatSessions.SetTimeout(time.Duration(cfg.SessionTimeout) * time.Minute)
					}
					log.Printf("config reloaded: log_level=%s session_timeout=%dm", cfg.LogLevel, cfg.SessionTimeout)
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
				log.Println("skills reloaded")
				if git != nil {
					git.Commit("skills updated via CC")
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
				var fileID, fileName string
				var duration int
				if len(u.Message.Photo) > 0 {
					fileID = u.Message.Photo[len(u.Message.Photo)-1].FileID
					fileName = "photo.jpg"
				} else if u.Message.Document != nil {
					fileID = u.Message.Document.FileID
					fileName = u.Message.Document.FileName
					if fileName == "" {
						fileName = "document"
					}
				} else if u.Message.Video != nil {
					fileID = u.Message.Video.FileID
					fileName = u.Message.Video.FileName
					if fileName == "" {
						fileName = "video.mp4"
					}
					duration = u.Message.Video.Duration
				} else if u.Message.Animation != nil {
					fileID = u.Message.Animation.FileID
					fileName = u.Message.Animation.FileName
					if fileName == "" {
						fileName = "animation.gif"
					}
					duration = u.Message.Animation.Duration
				} else if u.Message.VideoNote != nil {
					fileID = u.Message.VideoNote.FileID
					fileName = "videonote.mp4"
					duration = u.Message.VideoNote.Duration
				}

				if fileID != "" {
					tg.SendChatAction(u.Message.Chat.ID, "typing")
					data, err := media.DownloadFile(client, token, fileID)
					if err != nil {
						log.Printf("media download failed: %v", err)
					} else {
						mimeType := media.DetectMimeType(data, fileName)
						ext := extFromMime(mimeType, fileName)
						tmpFile, err := os.CreateTemp("", "alf-media-*"+ext)
						if err != nil {
							log.Printf("media temp file failed: %v", err)
						} else {
							tmpFile.Write(data)
							tmpFile.Close()
							os.Chmod(tmpFile.Name(), 0o644) // world-readable for claude subprocess
							tmpPath := tmpFile.Name()

							// Track all temp files for delayed cleanup.
							var cleanupPaths []string
							cleanupPaths = append(cleanupPaths, tmpPath)

							caption := u.Message.Caption
							if caption == "" {
								caption = u.Message.Text
							}

							// Video/GIF/VideoNote/video documents: extract frames + audio transcript.
							isVideoDoc := !hasVideo && media.IsVideoContent(mimeType, fileName)
							if hasVideo || isVideoDoc {
								mediaType := "VIDEO"
								if u.Message.Animation != nil {
									mediaType = "GIF/Animation"
								} else if u.Message.VideoNote != nil {
									mediaType = "VIDEO NOTE (round video)"
								}

								maxFrames := 8
								if u.Message.Animation != nil {
									maxFrames = 4
								}

								frames, err := media.ExtractFrames(tmpPath, maxFrames)
								if err != nil {
									log.Printf("frame extraction failed: %v", err)
									// Fallback: tell Claude the file is a video it can't view.
									if caption != "" {
										u.Message.Text = fmt.Sprintf("[%s from Telegram, %ds — frame extraction failed]\n%s", mediaType, duration, caption)
									} else {
										u.Message.Text = fmt.Sprintf("[%s from Telegram, %ds — frame extraction failed. Ask the user what it's about.]", mediaType, duration)
									}
								} else {
									cleanupPaths = append(cleanupPaths, frames...)

									// Try to extract and transcribe audio from videos (not GIFs).
									var transcript string
									if u.Message.Animation == nil && transcriber != nil && transcriber.IsReady() {
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

									var parts []string
									if len(frames) == 1 {
										parts = append(parts, fmt.Sprintf("[%s \"%s\" from Telegram (%ds) — contact sheet with key frames. Use Read tool to view: %s]", mediaType, fileName, duration, frames[0]))
									} else {
										parts = append(parts, fmt.Sprintf("[%s \"%s\" from Telegram (%ds) — %d frames extracted. Use Read tool to view: %s]", mediaType, fileName, duration, len(frames), strings.Join(frames, ", ")))
									}
									if transcript != "" {
										parts = append(parts, fmt.Sprintf("[Audio transcript: %s]", transcript))
									}
									if caption != "" {
										parts = append(parts, caption)
									} else if u.Message.Animation != nil {
										parts = append(parts, "The user sent this GIF as a reaction to the conversation. GIFs express emotions, humor, or reactions — don't describe the GIF literally. Instead, understand the feeling/mood it conveys and respond to that emotion naturally, matching the vibe. Keep it short.")
									} else {
										parts = append(parts, "The user shared this video in chat. Describe what you see in the frames and the audio context. React naturally.")
									}
									u.Message.Text = strings.Join(parts, "\n")
								}

								log.Printf("media: video %s (%ds) → %d frames", fileName, duration, len(cleanupPaths)-1)
							} else if media.IsImageContent(mimeType) {
								if caption != "" {
									u.Message.Text = fmt.Sprintf("[PHOTO from Telegram chat — use Read tool to view: %s]\n%s", tmpPath, caption)
								} else {
									u.Message.Text = fmt.Sprintf("[PHOTO from Telegram chat — use Read tool to view: %s]\nThe user shared this photo in chat. React naturally as you would in a personal conversation — comment on what you see, the mood, the context. This is NOT a code review.", tmpPath)
								}
							} else if media.IsTextContent(mimeType) || mimeType == "application/pdf" {
								textContent := media.ExtractTextFromDocument(data, mimeType)
								if caption != "" {
									u.Message.Text = fmt.Sprintf("[FILE from Telegram chat: %s]\nContent:\n%s\n\n%s", fileName, textContent, caption)
								} else {
									u.Message.Text = fmt.Sprintf("[FILE from Telegram chat: %s]\nContent:\n%s", fileName, textContent)
								}
							} else {
								if caption != "" {
									u.Message.Text = fmt.Sprintf("[FILE from Telegram chat: %s — use Read tool to view: %s]\n%s", fileName, tmpPath, caption)
								} else {
									u.Message.Text = fmt.Sprintf("[FILE from Telegram chat: %s — use Read tool to view: %s]\nThe user shared this file. Analyze and respond.", fileName, tmpPath)
								}
							}

							mediaCleanup = func() {
								for _, p := range cleanupPaths {
									os.Remove(p)
								}
							}

							log.Printf("media: saved %s (%s, %d bytes) → %s", fileName, mimeType, len(data), tmpPath)
							eventLog.Log("media_in", map[string]any{
								"chat_id":   u.Message.Chat.ID,
								"username":  u.Message.From.Username,
								"file_name": fileName,
								"mime_type": mimeType,
								"size":      len(data),
								"tmp_path":  tmpPath,
								"is_video":  hasVideo,
								"duration":  duration,
							})
						}
					}
				}
			}

			// Command routing: handle /commands before passing to Claude.
			if strings.HasPrefix(u.Message.Text, "/") {
				if handleCommand(tg, u.Message, chatSessions, eventLog, magic, ccExternalURL, allowedChatIDs) {
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
			if memDB != nil {
				preRecallBlock = autoRecall(memDB, u.Message.Text)
			}

			// Route message to appropriate tier.
			var tp tierParams
			var routeResult router.Result

			// Media messages bypass the router — they need a full Claude Code
			// session with Read tool access to view images/files.
			if hasMedia {
				routingAnim.Stop()
				if routingMsgID != 0 {
					tg.DeleteMessage(chatID, routingMsgID)
				}
				// Pick the lowest-priority enabled tier for media processing.
				tierName := ""
				bestPriority := int(^uint(0) >> 1) // max int
				for _, t := range tierStore.Current().Tiers {
					log.Printf("media tier scan: %s priority=%d enabled=%v instant=%v", t.Name, t.Priority, t.Enabled, t.Instant)
					if t.Enabled && t.Priority < bestPriority {
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
			if !hasMedia {
				routingAnim.Stop()
			}
			// If memories were recalled, override instant responses — the user
			// is asking about something personal that needs memory context.
			if preRecallBlock != "" && routeResult.Response != "" && routeResult.Tier == "" {
				log.Printf("→ memory override: instant response upgraded to tier (recalled memories found)")
				fallback := tierStore.Current().DefaultFallback
				if fallback == "" {
					fallback = "haiku_r"
				}
				routeResult = router.Result{Tier: fallback, Reason: "memory-override: instant→" + fallback}
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

			// Transition routing message into processing status message.
			var statusAnim *dotAnimator
			if routingMsgID != 0 {
				thinkBase := pickRandom(statusThinking)
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
					statusAnim.SetPhase(pickRandom(statusThinking), "choose_sticker")
				case "tool_use":
					statusAnim.SetPhase(pickRandom(statusToolUse), "upload_document")
				case "text":
					statusAnim.SetPhase(pickRandom(statusWriting), "typing")
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
			sysPromptTexts = append(sysPromptTexts, fmt.Sprintf(reactionSystemPromptTmpl, mood.AllowedReactionList()))

			invokeParams := provider.Params{
				Model:         tp.Model,
				Tools:         tp.Tools,
				Effort:        tp.Effort,
				MaxTurns:      tp.MaxTurns,
				SystemPrompts: sysPromptTexts,
				ResumeID:      resumeID,
				DataDir:       dataDir,
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

			if msgID, err := tg.SendMessageReturnID(chatID, reply); err == nil && msgID != 0 {
				alfMsgIDs.Add(msgID)
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
	Model    string   // full model name, e.g. "claude-sonnet-4-5"
	Tools    []string // nil = omit flag
	Effort   string   // "" = omit flag
	MaxTurns int      // 0 = omit flag (use Claude default)
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

	body, _ := io.ReadAll(resp.Body)

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
func handleCommand(tg *tgclient.Client, msg *Message, chatSessions *session.Store, eventLog *eventlog.Logger, magic *cc.MagicStore, ccExternalURL string, allowedChatIDs map[int64]bool) bool {
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
		welcome := `Hey, I'm <b>Alf</b> — your personal AI assistant powered by Claude.

<b>Getting started:</b>
1. Just send me a message — I'll respond naturally
2. Use the <b>Control Center</b> to customize my personality, tiers, and tools → /login
3. Edit <b>context/index.md</b> via the dashboard to teach me about your projects, preferences, and context

<b>Good to know:</b>
• I react to your emoji — positive reactions make me bolder, negative ones make me more careful
• My mood changes daily and adapts to your feedback in real-time
• Use /new to start a fresh conversation when switching topics

<b>Commands:</b>
/new — Fresh conversation
/login — Access the Control Center
/restart — Restart the daemon
/help — Show all commands

Ask me anything to get started.`
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
			"/restart — Restart the ALF daemon\n" +
			"/login — Get a login link for the Control Center\n" +
			"/start — Welcome message"
		tg.SendHTML(msg.Chat.ID, help)
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
	tg.SendHTML(chatID, fmt.Sprintf("Session: %s · Expires in 5 min\n%s", label, link))
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
				Model:    router.ResolveModel(t.Model),
				Tools:    t.Tools,
				Effort:   t.Effort,
				MaxTurns: t.MaxTurns,
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
	fixed := 0
	filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
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
		return nil
	})
	if fixed > 0 {
		log.Printf("fixed group permissions on %d files/dirs in data/", fixed)
	}
}

// linkSystemTools creates symlinks in toolsDir for each binary in srcDir.
func linkSystemTools(toolsDir, srcDir string) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}
	os.MkdirAll(toolsDir, 0o755)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		link := filepath.Join(toolsDir, e.Name())
		target := filepath.Join(srcDir, e.Name())
		// Skip if already a correct symlink.
		if existing, err := os.Readlink(link); err == nil && existing == target {
			continue
		}
		os.Remove(link) // remove stale symlink or file
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
	"Thinking",
	"Analyzing",
	"Digging in",
	"Reasoning",
	"Working it out",
	"Considering",
}

var statusToolUse = []string{
	"Reading files",
	"Looking things up",
	"Checking the code",
	"Investigating",
	"Doing some research",
	"Gathering context",
}

var statusWriting = []string{
	"Writing",
	"Drafting",
	"Putting it together",
	"Almost there",
	"Wrapping up",
}

// dotCycle returns animated dots: ".", "..", "...", "." cycling on each call.
var dotFrames = []string{".", "..", "..."}

func pickRandom(pool []string) string {
	return pool[rand.Intn(len(pool))]
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
// a formatted system prompt block. Returns "" if nothing relevant.
func autoRecall(store *memstore.Store, message string) string {
	if len(message) < 5 {
		return ""
	}
	q := message
	if len(q) > 60 {
		q = q[:60] + "..."
	}
	results, err := store.Search(message, 3)
	if err != nil {
		log.Printf("auto-recall: search error for %q: %v", q, err)
		return ""
	}
	if len(results) == 0 {
		log.Printf("auto-recall: no results for %q", q)
		return ""
	}
	var sb strings.Builder
	filtered := 0
	for _, r := range results {
		if r.Distance >= 1.2 {
			filtered++
			continue
		}
		if sb.Len() == 0 {
			sb.WriteString("=== [auto-recall] ===\nRelevant memories about the user (auto-retrieved):\n")
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", r.Type, r.Text))
	}
	if sb.Len() > 0 {
		log.Printf("auto-recall: injected %d memories for %q (filtered %d by distance)", strings.Count(sb.String(), "\n- "), q, filtered)
	} else {
		log.Printf("auto-recall: %d results for %q but all filtered by distance (>=0.8)", len(results), q)
	}
	return sb.String()
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

