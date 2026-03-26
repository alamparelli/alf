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

	"github.com/alamparelli/alf/internal/memory"
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

// ChatNotifier pushes notifications to the Control Center chat.
type ChatNotifier interface {
	Notify(text string)
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

// EventLogger writes structured events to daily log files.
type EventLogger interface {
	Log(event string, fields map[string]any)
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
	Backend              string
	Effort               string
	MaxIterations        int
	MaxTurns             int
	OrchestratorMaxTurns int
	Tools                []string
	Source               string
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
		rec := RunRecord{
			JobID:     j.ID,
			JobName:   j.Name,
			Tier:      j.Tier,
			StartedAt: time.Now(),
			Status:    "skipped",
		}
		e.runLog.appendAndTruncate(rec)
		e.logScheduleRun(rec, "")
		return
	}
	j.running = true
	start := time.Now()
	defer func() { j.running = false }()

	j.LastRun = &start

	var text string
	var err error
	var execResult *execResult

	if j.Message != "" {
		// Reminder: push message directly, no LLM/command execution.
		text = j.Message
	} else if j.Prompt == "__heartbeat__" {
		// Heartbeat: read context/heartbeat.md, skip if empty body.
		text, execResult, err = e.executeHeartbeat(j)
	} else if j.Command != "" && j.Prompt != "" && j.Tier != "direct" {
		// Two-phase job: run command first, only invoke LLM if output has issues.
		text, execResult, err = e.runTwoPhase(j)
	} else if j.Tier == "direct" {
		if j.Command != "" {
			text, err = e.runCommand(j)
		} else {
			log.Printf("scheduler: [%s] DEPRECATION: direct job using prompt instead of command - migrate to --command", j.ID)
			text = j.Prompt
		}
	} else if j.Tier == "agent" && e.cfg.Orchestrator != nil {
		text, execResult, err = e.invokeOrchestratorWithMeta(j)
	} else {
		text, execResult, err = e.invokeLLMWithMeta(j)
	}

	duration := time.Since(start)
	tier := j.Tier
	if j.Message != "" {
		tier = "reminder"
	}
	rec := RunRecord{
		JobID:      j.ID,
		JobName:    j.Name,
		Tier:       tier,
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
		e.logScheduleRun(rec, "")

		// Notify on failure if output includes chat.
		if j.Output != "file" && j.Output != "silent" {
			e.dispatch(j, fmt.Sprintf("⚠️ Scheduled job \"%s\" failed: %s", j.Name, err))
		}
		return
	}

	// Detect turn limit as a soft failure - enrich with job context.
	if strings.Contains(text, "Turn limit reached") || strings.Contains(text, "turn limit") {
		rec.Status = "turn_limit"
		promptSnippet := j.Prompt
		if len(promptSnippet) > 120 {
			promptSnippet = promptSnippet[:120] + "..."
		}
		tierLabel := j.Tier
		if tierLabel == "" {
			tierLabel = "default"
		}
		detail := fmt.Sprintf(
			"⚠️ Turn limit reached for job \"%s\"\n"+
				"• Job ID: %s\n"+
				"• Tier: %s\n"+
				"• Prompt: %s\n\n"+
				"The task could not complete within the allowed turns. "+
				"Try: simplify the prompt, increase max_turns in tier config, or split into smaller steps.",
			j.Name, j.ID, tierLabel, promptSnippet)
		j.LastError = "turn limit reached"
		rec.Error = "turn limit reached"
		log.Printf("scheduler: [%s] %q turn limit reached (%s)", j.ID, j.Name, duration.Round(time.Millisecond))
		e.runLog.appendAndTruncate(rec)
		e.logScheduleRun(rec, text)

		if j.Output != "file" && j.Output != "silent" {
			e.dispatch(j, detail)
		}
		if !j.System {
			e.store.Save()
		}
		return
	}

	j.LastError = ""
	rec.Status = "ok"
	rec.OutputLen = len(text)
	log.Printf("scheduler: [%s] %q ok (%s, %d chars)", j.ID, j.Name, duration.Round(time.Millisecond), len(text))
	e.runLog.appendAndTruncate(rec)
	e.logScheduleRun(rec, text)

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
	timeout := j.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
			return "", fmt.Errorf("command timed out after %v", timeout)
		}
		// Include output on failure for debugging.
		if output != "" {
			return "", fmt.Errorf("command failed: %w\n%s", err, output)
		}
		return "", fmt.Errorf("command failed: %w", err)
	}

	return strings.TrimSpace(output), nil
}

// errorPatterns are strings that indicate a command output contains issues worth analyzing.
var errorPatterns = []string{"error", "panic", "fatal", "failed", "timeout", "killed", "ERR", "CRITICAL", "WARNING"}

// runTwoPhase executes a command first, then only invokes the LLM if the output
// contains error-like patterns. This avoids wasting LLM calls on healthy states.
func (e *Engine) runTwoPhase(j *Job) (string, *execResult, error) {
	cmdOutput, err := e.runCommand(j)
	if err != nil {
		// Command itself failed - send error directly, no LLM needed.
		return "", nil, err
	}

	// Check if the output contains any error patterns.
	lower := strings.ToLower(cmdOutput)
	hasIssues := false
	for _, p := range errorPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			hasIssues = true
			break
		}
	}

	if !hasIssues {
		log.Printf("scheduler: [%s] two-phase: no issues detected, skipping LLM", j.ID)
		return "", nil, nil // empty = healthy = no notification
	}

	// Issues found - invoke LLM to analyze.
	log.Printf("scheduler: [%s] two-phase: issues detected, invoking LLM for analysis", j.ID)
	analysisJob := *j
	analysisJob.Command = "" // clear command so invokeLLM uses prompt only
	analysisJob.Prompt = j.Prompt + "\n\n## Command Output\n```\n" + cmdOutput + "\n```"
	text, result, err := e.invokeLLMWithMeta(&analysisJob)
	return text, result, err
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

	// Inject scheduled job context so the LLM knows why it was triggered.
	jobContext := fmt.Sprintf("You are executing scheduled job \"%s\" (ID: %s, schedule: %s).", j.Name, j.ID, j.Schedule)
	if j.Message != "" {
		jobContext += fmt.Sprintf(" The scheduled message is: %s", j.Message)
	}
	params.SystemPrompts = append(params.SystemPrompts, jobContext)

	// Load system prompts: L1 (identity), L2 (tools), L3 (user context).
	if e.cfg.ContextDir != "" {
		params.SystemPrompts = append(params.SystemPrompts, memory.CollectSchedulerPrompts(e.cfg.ContextDir)...)
	}

	// Inject flattened skill prompts for non-interactive context.
	if len(j.Skills) > 0 && e.cfg.SkillStore != nil {
		if block := buildSkillBlock(e.cfg.SkillStore, j.Skills); block != "" {
			params.SystemPrompts = append(params.SystemPrompts, block)
		}
	}

	llmTimeout := j.Timeout
	if llmTimeout <= 0 {
		llmTimeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
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

// SendDailyDigest generates and sends a schedule execution report for the last 24h.
func (e *Engine) SendDailyDigest() error {
	digest := e.runLog.DailyDigest(time.Now().Add(-24 * time.Hour))
	if digest == "" {
		log.Println("scheduler: daily digest - no runs in last 24h, skipping")
		return nil
	}
	if e.cfg.TG != nil && e.cfg.ChatID != 0 {
		if err := e.cfg.TG.SendMessage(e.cfg.ChatID, digest); err != nil {
			return fmt.Errorf("send digest: %w", err)
		}
	}
	log.Printf("scheduler: daily digest sent")
	return nil
}

// dispatch routes job output to the configured destination.
// Output modes: "chat" (tg+cc), "tg" (telegram only), "cc" (control center only),
// "file" (log file), "both" (chat+file), "silent" (no output).
func (e *Engine) dispatch(j *Job, text string) {
	if text == "" {
		return
	}

	switch j.Output {
	case "chat":
		e.sendTG(j, text)
		e.sendCC(j, text)
	case "tg":
		e.sendTG(j, text)
	case "cc":
		e.sendCC(j, text)
	case "file":
		e.writeFile(j, text)
	case "both":
		e.sendTG(j, text)
		e.sendCC(j, text)
		e.writeFile(j, text)
	case "silent":
		// no-op
	}
}

// sendTG sends to Telegram if configured.
func (e *Engine) sendTG(j *Job, text string) {
	if e.cfg.TG != nil && e.cfg.ChatID != 0 {
		if err := e.cfg.TG.SendMessage(e.cfg.ChatID, text); err != nil {
			log.Printf("scheduler: telegram send failed for job %s: %v", j.ID, err)
		}
	}
}

// sendCC sends to Control Center if configured.
func (e *Engine) sendCC(j *Job, text string) {
	if e.cfg.CC != nil {
		e.cfg.CC.Notify(text)
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

	// Build system prompts: L1 (identity), L2 (tools), L3 (user context).
	var sysPrompts []string
	if e.cfg.ContextDir != "" {
		sysPrompts = append(sysPrompts, memory.CollectSchedulerPrompts(e.cfg.ContextDir)...)
	}
	if len(j.Skills) > 0 && e.cfg.SkillStore != nil {
		if block := buildSkillBlock(e.cfg.SkillStore, j.Skills); block != "" {
			sysPrompts = append(sysPrompts, block)
		}
	}

	orchTimeout := j.Timeout
	if orchTimeout <= 0 {
		orchTimeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), orchTimeout)
	defer cancel()

	text, meta, err := e.cfg.Orchestrator.Run(ctx, j.Prompt, sysPrompts, RunConfig{Source: "schedule"}, nil)
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

// logScheduleRun writes a schedule_run event to the daily event log.
func (e *Engine) logScheduleRun(rec RunRecord, output string) {
	if e.cfg.EventLog == nil {
		return
	}
	fields := map[string]any{
		"job_id":      rec.JobID,
		"job_name":    rec.JobName,
		"tier":        rec.Tier,
		"status":      rec.Status,
		"duration_ms": rec.DurationMs,
	}
	if rec.Error != "" {
		errMsg := rec.Error
		if len(errMsg) > 300 {
			errMsg = errMsg[:300]
		}
		fields["error"] = errMsg
	}
	if rec.CostUSD > 0 {
		fields["cost_usd"] = rec.CostUSD
	}
	if rec.Model != "" {
		fields["model"] = rec.Model
	}
	if rec.Iterations > 0 {
		fields["iterations"] = rec.Iterations
	}
	if output != "" {
		fields["output"] = output
	}
	fields["output_len"] = rec.OutputLen
	e.cfg.EventLog.Log("schedule_run", fields)
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

