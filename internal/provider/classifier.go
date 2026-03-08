package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ClassifierConfig configures the persistent CLIClassifier process.
type ClassifierConfig struct {
	Model        string             // e.g. "claude-haiku-4-5"
	SystemPrompt string             // one-time system prompt (personality + tiers + rules)
	HomeDir      string             // HOME for subprocess (where .claude/ lives); falls back to DataDir
	DataDir      string             // working directory
	Credential   *syscall.Credential // subprocess isolation
	IdleTimeout  time.Duration      // restart after idle (resets conversation context)
	MaxRetries   int                // max restart attempts before fallback
}

// CLIClassifier maintains a persistent Claude CLI process for fast classification.
// Messages are sent via stdin (stream-json) and responses read from stdout.
// Follows the voice.Transcriber pattern: mutex serialization, crash detection, auto-restart.
type CLIClassifier struct {
	cfg ClassifierConfig

	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	reader   *bufio.Reader
	ready    bool
	retries  int
	lastUsed time.Time

	idleTimer *time.Timer
	stopCh    chan struct{}
	stopped   bool
}

// NewCLIClassifier creates a new classifier. Call Start() to launch the process.
func NewCLIClassifier(cfg ClassifierConfig) *CLIClassifier {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	return &CLIClassifier{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
}

// Start launches the persistent Claude process.
func (c *CLIClassifier) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startLocked()
}

func (c *CLIClassifier) startLocked() error {
	if c.ready {
		return nil
	}

	model := c.cfg.Model
	if model == "" {
		model = "claude-haiku-4-5"
	}

	args := []string{
		"--system-prompt", c.cfg.SystemPrompt,
		"--model", model,
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--max-turns", "3",
		"--tools", "",
		"--dangerously-skip-permissions",
		"--no-session-persistence",
		"--verbose",
	}

	cmd := exec.Command("claude", args...)
	cmd.Dir = c.cfg.DataDir
	if c.cfg.Credential != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: c.cfg.Credential,
		}
	}

	// Build a safe environment (same allowlist as CLIProvider).
	classifierHome := c.cfg.HomeDir
	if classifierHome == "" {
		classifierHome = c.cfg.DataDir
	}
	cmd.Env = safeEnv(classifierHome, c.cfg.DataDir)

	// Log classifier environment for debugging.
	for _, e := range cmd.Env {
		if k, _, ok := strings.Cut(e, "="); ok {
			v := strings.TrimPrefix(e, k+"=")
			if len(v) > 80 {
				v = v[:80] + "..."
			}
			log.Printf("classifier: env %s=%s", k, v)
		}
	}

	// Capture stderr to log any Claude CLI errors.
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("start classifier: %w", err)
	}
	log.Printf("classifier: process started (pid=%d, model=%s)", cmd.Process.Pid, model)

	// Log stderr in background.
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			log.Printf("classifier stderr: %s", scanner.Text())
		}
	}()

	reader := bufio.NewReaderSize(stdout, 256*1024)

	c.cmd = cmd
	c.stdin = stdin
	c.reader = reader
	c.lastUsed = time.Now()
	c.retries = 0

	// Send a warmup message to trigger the init event + first response.
	// Claude CLI with --input-format stream-json only starts producing output
	// after receiving input on stdin.
	warmup := `{"type":"user","message":{"role":"user","content":"ready"}}` + "\n"
	if _, err := stdin.Write([]byte(warmup)); err != nil {
		c.killLocked()
		return fmt.Errorf("write warmup: %w", err)
	}

	// Drain init event + warmup response.
	initCtx, initCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer initCancel()
	c.ready = true // temporarily set so readResponse works
	_, err = c.readResponse(initCtx)
	if err != nil {
		c.ready = false
		c.killLocked()
		return fmt.Errorf("drain warmup response: %w", err)
	}

	// Start idle timer.
	c.resetIdleTimer()

	log.Printf("classifier: ready (model=%s, pid=%d)", model, cmd.Process.Pid)
	return nil
}

// Classify sends a message to the persistent process and returns the classification.
func (c *CLIClassifier) Classify(ctx context.Context, message string) (*ClassifyResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.ready {
		if err := c.tryRestart(); err != nil {
			return nil, fmt.Errorf("classifier not ready: %w", err)
		}
	}

	c.lastUsed = time.Now()
	c.resetIdleTimer()

	// Send user message via stdin as stream-json.
	msg := map[string]any{
		"type": "user",
		"message": map[string]string{
			"role":    "user",
			"content": message,
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal message: %w", err)
	}
	data = append(data, '\n')

	if _, err := c.stdin.Write(data); err != nil {
		c.ready = false
		return nil, fmt.Errorf("write to classifier: %w", err)
	}

	// Read the classification response (initial "ready" already drained at startup).
	result, err := c.readResponse(ctx)
	if err != nil {
		c.ready = false
		return nil, err
	}

	return result, nil
}

// InjectContext sends a post-response context summary as an assistant message
// so the classifier tracks what happened.
func (c *CLIClassifier) InjectContext(tierName, access, summary string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.ready {
		return nil // silently skip if not ready
	}

	text := fmt.Sprintf("[%s (%s) responded: %s]", tierName, access, truncate(summary, 120))

	msg := map[string]any{
		"type": "user",
		"message": map[string]string{
			"role":    "user",
			"content": text,
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal context: %w", err)
	}
	data = append(data, '\n')

	if _, err := c.stdin.Write(data); err != nil {
		c.ready = false
		return fmt.Errorf("write context: %w", err)
	}

	// Drain the response (we don't need it).
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()
	_, err = c.readResponse(drainCtx)
	if err != nil {
		c.ready = false
		return fmt.Errorf("drain context response: %w", err)
	}

	return nil
}

// readResponse reads stream events until a result event, extracting the text.
func (c *CLIClassifier) readResponse(ctx context.Context) (*ClassifyResult, error) {
	type readResult struct {
		line string
		err  error
	}

	var resultText strings.Builder

	for {
		ch := make(chan readResult, 1)
		go func() {
			line, err := c.reader.ReadString('\n')
			ch <- readResult{line, err}
		}()

		select {
		case res := <-ch:
			if res.err != nil {
				return nil, fmt.Errorf("read from classifier: %w", res.err)
			}

			line := strings.TrimSpace(res.line)
			if line == "" {
				continue
			}

			var event struct {
				Type  string `json:"type"`
				Event struct {
					Delta struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"delta"`
				} `json:"event"`
				// Result-level fields.
				ResultText string `json:"result"`
			}
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				log.Printf("classifier: unparseable line: %s", truncate(line, 200))
				continue
			}

			// Accumulate text deltas.
			if event.Type == "stream_event" && event.Event.Delta.Type == "text_delta" {
				resultText.WriteString(event.Event.Delta.Text)
			}

			// Result event = end of response.
			if event.Type == "result" {
				text := event.ResultText
				if text == "" {
					text = resultText.String()
				}
				log.Printf("classifier: result event (resultField=%q, accumulated=%q)", truncate(event.ResultText, 100), truncate(resultText.String(), 100))
				return &ClassifyResult{Response: strings.TrimSpace(text)}, nil
			}

		case <-ctx.Done():
			return nil, fmt.Errorf("classifier response timeout: %v", ctx.Err())
		}
	}
}

// Restart kills the current process and starts a new one.
func (c *CLIClassifier) Restart() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killLocked()
	return c.startLocked()
}

// Close stops the classifier process.
func (c *CLIClassifier) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	if c.idleTimer != nil {
		c.idleTimer.Stop()
	}
	c.killLocked()
	return nil
}

// IsReady returns whether the classifier process is alive.
func (c *CLIClassifier) IsReady() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ready
}

func (c *CLIClassifier) killLocked() {
	if c.cmd != nil && c.cmd.Process != nil {
		c.stdin.Close()
		c.cmd.Process.Kill()
		c.cmd.Wait()
		c.cmd = nil
		c.ready = false
		log.Println("classifier: process stopped")
	}
}

func (c *CLIClassifier) tryRestart() error {
	if c.retries >= c.cfg.MaxRetries {
		return fmt.Errorf("max retries (%d) exceeded", c.cfg.MaxRetries)
	}
	c.retries++
	log.Printf("classifier: restarting (attempt %d/%d)", c.retries, c.cfg.MaxRetries)
	c.killLocked()
	return c.startLocked()
}

func (c *CLIClassifier) resetIdleTimer() {
	if c.idleTimer != nil {
		c.idleTimer.Stop()
	}
	c.idleTimer = time.AfterFunc(c.cfg.IdleTimeout, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.stopped {
			return
		}
		log.Printf("classifier: idle timeout (%v), restarting for fresh context", c.cfg.IdleTimeout)
		c.killLocked()
		if err := c.startLocked(); err != nil {
			log.Printf("classifier: idle restart failed: %v", err)
		}
	})
}

// UpdateSystemPrompt restarts the classifier with a new system prompt.
// Use when tiers config changes via CC hot-reload.
func (c *CLIClassifier) UpdateSystemPrompt(systemPrompt string) error {
	c.mu.Lock()
	c.cfg.SystemPrompt = systemPrompt
	c.mu.Unlock()
	return c.Restart()
}

// UpdateModel restarts the classifier with a new model.
func (c *CLIClassifier) UpdateModel(model string) error {
	c.mu.Lock()
	c.cfg.Model = model
	c.mu.Unlock()
	return c.Restart()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
