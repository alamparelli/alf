package scheduler

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// TelegramSender sends messages to Telegram.
type TelegramSender interface {
	SendMessage(chatID int64, text string) error
}

// ProviderInvoker invokes Claude CLI.
type ProviderInvoker interface {
	Invoke(ctx context.Context, prompt string, params ProviderParams, onProgress interface{}) (*ProviderResult, error)
}

// ProviderParams mirrors provider.Params to avoid circular imports.
type ProviderParams struct {
	Model         string
	Tools         []string
	Effort        string
	SystemPrompts []string
	MaxTurns      int
	DataDir       string
}

// ProviderResult mirrors provider.Result.
type ProviderResult struct {
	SessionID string
	Text      string
	Model     string
	CostUSD   float64
	NumTurns  int
}

// ChatLogger records messages into the conversation history.
type ChatLogger interface {
	LogScheduledMessage(text, tier, jobName string)
}

// TierStoreReader reads tier configuration.
type TierStoreReader interface {
	Current() *TiersSnapshot
}

// TiersSnapshot is a minimal view of tier config needed by the executor.
type TiersSnapshot struct {
	Tiers []TierInfo
}

// TierInfo holds the fields the executor needs from a tier.
type TierInfo struct {
	Name     string
	Model    string
	Tools    []string
	Effort   string
	MaxTurns int
}

// executeJob runs a scheduled job with concurrency guard.
func (e *Engine) executeJob(j *Job) {
	if j.running {
		log.Printf("scheduler: skipping %s (still running)", j.ID)
		return
	}
	j.running = true
	start := time.Now()
	defer func() { j.running = false }()

	j.LastRun = &start

	var text string
	var err error

	if j.Tier == "direct" {
		text = j.Prompt
	} else {
		text, err = e.invokeLLM(j)
	}

	if err != nil {
		j.LastError = err.Error()
		log.Printf("scheduler: [%s] %q failed (%s): %v", j.ID, j.Name, time.Since(start).Round(time.Millisecond), err)
		// Notify on failure if output includes telegram.
		if j.Output == "telegram" || j.Output == "both" {
			if e.cfg.TG != nil && e.cfg.ChatID != 0 {
				e.cfg.TG.SendMessage(e.cfg.ChatID, fmt.Sprintf("Scheduled job \"%s\" failed: %s", j.Name, err))
			}
		}
		return
	}

	j.LastError = ""
	log.Printf("scheduler: [%s] %q ok (%s)", j.ID, j.Name, time.Since(start).Round(time.Millisecond))

	// Suppress internal fallback messages.
	if text == "Done (no text output)." {
		text = ""
	}

	e.dispatch(j, text)

	// Log to conversation history so the message appears in chat context.
	if e.cfg.ChatLogger != nil && text != "" && j.Output != "silent" {
		e.cfg.ChatLogger.LogScheduledMessage(text, j.Tier, j.Name)
	}

	// Update next run.
	e.mu.Lock()
	if eid, ok := e.entries[j.ID]; ok {
		entry := e.cron.Entry(eid)
		if !entry.Next.IsZero() {
			next := entry.Next
			j.NextRun = &next
		}
	}
	e.mu.Unlock()

	// Persist updated state.
	if !j.System {
		e.store.Save()
	}
}

// invokeLLM calls the Claude provider with tier-appropriate params.
func (e *Engine) invokeLLM(j *Job) (string, error) {
	if e.cfg.Provider == nil {
		return "", fmt.Errorf("no provider configured")
	}

	params := ProviderParams{
		Model:   "claude-haiku-4-5", // default
		DataDir: e.cfg.DataDir,
	}

	// Resolve tier params from tier store.
	if e.cfg.TierStore != nil {
		snap := e.cfg.TierStore.Current()
		if snap != nil {
			for _, t := range snap.Tiers {
				if t.Name == j.Tier {
					if t.Model != "" {
						params.Model = t.Model
					}
					params.Tools = t.Tools
					params.Effort = t.Effort
					if t.MaxTurns > 0 {
						params.MaxTurns = t.MaxTurns
					}
					break
				}
			}
		}
	}

	// Load system prompts from context dir.
	if e.cfg.ContextDir != "" {
		indexPath := filepath.Join(e.cfg.ContextDir, "index.md")
		if data, err := os.ReadFile(indexPath); err == nil {
			params.SystemPrompts = append(params.SystemPrompts, string(data))
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := e.cfg.Provider.Invoke(ctx, j.Prompt, params, nil)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// dispatch routes job output to the configured destination.
func (e *Engine) dispatch(j *Job, text string) {
	if text == "" {
		return
	}

	switch j.Output {
	case "telegram":
		e.sendTelegram(j, text)
	case "file":
		e.writeFile(j, text)
	case "both":
		e.sendTelegram(j, text)
		e.writeFile(j, text)
	case "silent":
		// no-op
	}
}

func (e *Engine) sendTelegram(j *Job, text string) {
	if e.cfg.TG == nil || e.cfg.ChatID == 0 {
		log.Printf("scheduler: telegram not configured, skipping output for job %s", j.ID)
		return
	}
	if err := e.cfg.TG.SendMessage(e.cfg.ChatID, text); err != nil {
		log.Printf("scheduler: telegram send failed for job %s: %v", j.ID, err)
	}
}

func (e *Engine) writeFile(j *Job, text string) {
	dir := filepath.Join(e.cfg.DataDir, "logs", "scheduler", time.Now().Format("2006-01-02"))
	os.MkdirAll(dir, 0o755)

	path := filepath.Join(dir, j.ID+".txt")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("scheduler: write file failed for job %s: %v", j.ID, err)
		return
	}
	defer f.Close()

	header := fmt.Sprintf("\n--- %s [%s] ---\n", j.Name, time.Now().Format(time.RFC3339))
	f.WriteString(header)
	f.WriteString(text)
	f.WriteString("\n")
}

