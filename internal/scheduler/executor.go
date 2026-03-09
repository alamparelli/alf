package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// execResult captures metadata from LLM/orchestrator invocations.
type execResult struct {
	CostUSD    float64
	Model      string
	NumTurns   int
	Iterations int
}

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
	WriteCapable  bool
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

// OrchestratorRunner runs the multi-agent orchestrator.
type OrchestratorRunner interface {
	Run(ctx context.Context, userMessage string, systemPrompts []string, rc RunConfig, onProgress ProgressFunc) (string, *TaskMeta, error)
}

// ProgressFunc reports orchestrator status updates.
type ProgressFunc func(phase, detail string)

// RunConfig holds tier-level settings for an orchestrator run (mirrors agents.RunConfig).
type RunConfig struct {
	Model                string
	Effort               string
	MaxIterations        int
	MaxTurns             int
	OrchestratorMaxTurns int
	Tools                []string
}

// TaskMeta tracks orchestration lifecycle (mirrors agents.TaskMeta).
type TaskMeta struct {
	Iterations int
	TotalCost  float64
	Status     string
}

// SkillStoreReader provides skill prompts for injection into scheduler jobs.
type SkillStoreReader interface {
	Get(name string) (*SkillInfo, bool)
}

// SkillInfo holds the fields the executor needs from a skill.
type SkillInfo struct {
	Name   string
	Prompt string
}

// TiersSnapshot is a minimal view of tier config needed by the executor.
type TiersSnapshot struct {
	Tiers []TierInfo
}

// TierInfo holds the fields the executor needs from a tier.
type TierInfo struct {
	Name         string
	Model        string
	Tools        []string
	WriteCapable bool
	Effort       string
	MaxTurns     int
}

// executeJob runs a scheduled job with concurrency guard.
func (e *Engine) executeJob(j *Job) {
	if j.running {
		log.Printf("scheduler: skipping %s (still running)", j.ID)
		e.runLog.appendAndTruncate(RunRecord{
			JobID:     j.ID,
			JobName:   j.Name,
			Tier:      j.Tier,
			StartedAt: time.Now(),
			Status:    "skipped",
		})
		return
	}
	j.running = true
	start := time.Now()
	defer func() { j.running = false }()

	j.LastRun = &start

	var text string
	var err error
	var execResult *execResult

	if j.Tier == "direct" {
		if j.Command != "" {
			text, err = e.runCommand(j)
		} else {
			log.Printf("scheduler: [%s] DEPRECATION: direct job using prompt instead of command — migrate to --command", j.ID)
			text = j.Prompt
		}
	} else if j.Tier == "agent" && e.cfg.Orchestrator != nil {
		text, execResult, err = e.invokeOrchestratorWithMeta(j)
	} else {
		text, execResult, err = e.invokeLLMWithMeta(j)
	}

	duration := time.Since(start)
	rec := RunRecord{
		JobID:      j.ID,
		JobName:    j.Name,
		Tier:       j.Tier,
		StartedAt:  start,
		DurationMs: duration.Milliseconds(),
	}
	if execResult != nil {
		rec.CostUSD = execResult.CostUSD
		rec.Model = execResult.Model
		rec.NumTurns = execResult.NumTurns
		rec.Iterations = execResult.Iterations
	}

	if err != nil {
		j.LastError = err.Error()
		rec.Status = "error"
		rec.Error = err.Error()
		if strings.Contains(err.Error(), "timed out") {
			rec.Status = "timeout"
		}
		log.Printf("scheduler: [%s] %q failed (%s): %v", j.ID, j.Name, duration.Round(time.Millisecond), err)
		e.runLog.appendAndTruncate(rec)

		// Notify on failure if output includes telegram.
		if j.Output == "telegram" || j.Output == "both" {
			if e.cfg.TG != nil && e.cfg.ChatID != 0 {
				e.cfg.TG.SendMessage(e.cfg.ChatID, fmt.Sprintf("⚠️ Scheduled job \"%s\" failed: %s", j.Name, err))
			}
		}
		return
	}

	j.LastError = ""
	rec.Status = "ok"
	rec.OutputLen = len(text)
	log.Printf("scheduler: [%s] %q ok (%s, %d chars)", j.ID, j.Name, duration.Round(time.Millisecond), len(text))
	e.runLog.appendAndTruncate(rec)

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

// runCommand executes a bash command for direct-tier jobs.
func (e *Engine) runCommand(j *Job) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", j.Command)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	output := buf.String()

	// Truncate to 4000 chars (Telegram message limit safety).
	const maxOutput = 4000
	if len(output) > maxOutput {
		output = output[:maxOutput] + "\n... (truncated)"
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out after 2 minutes")
		}
		// Include output on failure for debugging.
		if output != "" {
			return "", fmt.Errorf("command failed: %w\n%s", err, output)
		}
		return "", fmt.Errorf("command failed: %w", err)
	}

	return strings.TrimSpace(output), nil
}

// invokeLLM calls the Claude provider with tier-appropriate params.
func (e *Engine) invokeLLM(j *Job) (string, error) {
	text, _, err := e.invokeLLMWithMeta(j)
	return text, err
}

// invokeLLMWithMeta calls the Claude provider and returns execution metadata.
func (e *Engine) invokeLLMWithMeta(j *Job) (string, *execResult, error) {
	if e.cfg.Provider == nil {
		return "", nil, fmt.Errorf("no provider configured")
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
					params.WriteCapable = t.WriteCapable
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

	// Inject flattened skill prompts for non-interactive context.
	if len(j.Skills) > 0 && e.cfg.SkillStore != nil {
		if block := buildSkillBlock(e.cfg.SkillStore, j.Skills); block != "" {
			params.SystemPrompts = append(params.SystemPrompts, block)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := e.cfg.Provider.Invoke(ctx, j.Prompt, params, nil)
	if err != nil {
		return "", nil, err
	}

	meta := &execResult{
		CostUSD:  result.CostUSD,
		Model:    result.Model,
		NumTurns: result.NumTurns,
	}
	return result.Text, meta, nil
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

// invokeOrchestrator delegates the job to the multi-agent orchestrator (legacy wrapper).
func (e *Engine) invokeOrchestrator(j *Job) (string, error) {
	text, _, err := e.invokeOrchestratorWithMeta(j)
	return text, err
}

// invokeOrchestratorWithMeta delegates to the orchestrator and returns execution metadata.
func (e *Engine) invokeOrchestratorWithMeta(j *Job) (string, *execResult, error) {
	if e.cfg.Orchestrator == nil {
		return "", nil, fmt.Errorf("orchestrator not configured")
	}

	// Build system prompts (same as invokeLLM).
	var sysPrompts []string
	if e.cfg.ContextDir != "" {
		indexPath := filepath.Join(e.cfg.ContextDir, "index.md")
		if data, err := os.ReadFile(indexPath); err == nil {
			sysPrompts = append(sysPrompts, string(data))
		}
	}
	if len(j.Skills) > 0 && e.cfg.SkillStore != nil {
		if block := buildSkillBlock(e.cfg.SkillStore, j.Skills); block != "" {
			sysPrompts = append(sysPrompts, block)
		}
	}

	// Orchestrator jobs get a longer timeout (up to 30 minutes).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	text, meta, err := e.cfg.Orchestrator.Run(ctx, j.Prompt, sysPrompts, RunConfig{}, nil)
	if err != nil {
		return "", nil, err
	}

	result := &execResult{
		Iterations: meta.Iterations,
		CostUSD:    meta.TotalCost,
	}
	log.Printf("scheduler: [%s] orchestrator done: %d iterations, $%.4f", j.ID, meta.Iterations, meta.TotalCost)
	return text, result, nil
}

// buildSkillBlock resolves skill names and returns flattened prompts for injection.
func buildSkillBlock(store SkillStoreReader, names []string) string {
	var blocks []string
	for _, name := range names {
		sk, ok := store.Get(name)
		if !ok {
			log.Printf("scheduler: skill %q not found, skipping", name)
			continue
		}
		if sk.Prompt == "" {
			continue
		}
		blocks = append(blocks, fmt.Sprintf("--- %s ---\n%s", sk.Name, sk.Prompt))
	}
	if len(blocks) == 0 {
		return ""
	}
	return strings.Join(blocks, "\n\n")
}

