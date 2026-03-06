package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// CLIProvider invokes Claude Code CLI as a subprocess (spawn-per-call).
type CLIProvider struct {
	// DefaultDataDir is used when Params.DataDir is empty.
	DefaultDataDir string
	// Timeout for each invocation. Zero means 5 minutes.
	Timeout time.Duration
	// Credential for subprocess isolation (uid/gid). Nil = inherit.
	Credential *syscall.Credential
}

// NewCLIProvider creates a new CLIProvider.
func NewCLIProvider(dataDir string, timeout time.Duration, cred *syscall.Credential) *CLIProvider {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &CLIProvider{
		DefaultDataDir: dataDir,
		Timeout:        timeout,
		Credential:     cred,
	}
}

// Invoke spawns a claude -p subprocess, parses stream-json, and returns the result.
func (p *CLIProvider) Invoke(ctx context.Context, prompt string, params Params, onProgress OnProgress) (*Result, error) {
	model := params.Model
	if model == "" {
		model = "claude-haiku-4-5"
	}

	// Prevent prompt from being parsed as a CLI flag.
	safePrompt := prompt
	if strings.HasPrefix(prompt, "-") {
		safePrompt = "\u200B" + prompt
	}

	args := []string{
		"-p", safePrompt,
		"--model", model,
		"--output-format", "stream-json",
		"--verbose",
	}

	if params.WriteCapable {
		// Full access — all tools auto-approved.
		args = append(args, "--dangerously-skip-permissions")
	} else {
		// Read-only: whitelist specific tools, deny everything else.
		// In non-interactive (-p) mode, non-allowed tools are auto-denied.
		for _, tool := range params.Tools {
			args = append(args, "--allowedTools", tool)
		}
	}
	if params.Effort != "" {
		args = append(args, "--effort", params.Effort)
	}
	if params.ResumeID != "" {
		args = append(args, "--resume", params.ResumeID)
	}
	if params.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", params.MaxTurns))
	}

	// Append system prompts (context files, reaction instructions, etc.)
	for _, sp := range params.SystemPrompts {
		args = append(args, "--append-system-prompt", sp)
	}

	dataDir := params.DataDir
	if dataDir == "" {
		dataDir = p.DefaultDataDir
	}

	timeout := p.Timeout
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "claude", args...)
	cmd.Dir = dataDir
	if p.Credential != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: p.Credential,
		}
	}

	// Build a safe environment for the subprocess (allowlist, not blocklist).
	// Prevents leaking secrets (TELEGRAM_BOT_TOKEN, CC_AUTH_TOKEN, etc.)
	// to Claude which runs with --dangerously-skip-permissions.
	cmd.Env = safeEnv(dataDir)
	cmd.Env = append(cmd.Env, params.Env...)

	log.Printf("provider: invoke starting (resume=%q, model=%s, max_turns=%d, effort=%s, sys_prompts=%d, tools=%v, write=%v)",
		params.ResumeID, model, params.MaxTurns, params.Effort, len(params.SystemPrompts), params.Tools, params.WriteCapable)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	invokeStart := time.Now()

	var (
		resultText   strings.Builder
		lastEvent    json.RawMessage
		sentThinking bool
		eventCount   int
		firstEvent   bool
	)

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		eventCount++
		if !firstEvent {
			firstEvent = true
			log.Printf("provider: first event after %dms", time.Since(invokeStart).Milliseconds())
		}

		lastEvent = make(json.RawMessage, len(line))
		copy(lastEvent, line)

		var event struct {
			Type  string `json:"type"`
			Event struct {
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
				ContentBlock struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"content_block"`
			} `json:"event"`
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}

		if onProgress != nil {
			switch {
			case event.Type == "stream_event" && event.Event.ContentBlock.Type == "thinking" && !sentThinking:
				onProgress(StreamEvent{Type: "thinking"})
				sentThinking = true
			case event.Type == "stream_event" && event.Event.ContentBlock.Type == "tool_use":
				onProgress(StreamEvent{Type: "tool_use", Detail: event.Event.ContentBlock.Name})
			case event.Type == "stream_event" && event.Event.Delta.Type == "text_delta":
				onProgress(StreamEvent{Type: "text"})
			}
		}

		if event.Type == "stream_event" && event.Event.Delta.Type == "text_delta" {
			resultText.WriteString(event.Event.Delta.Text)
		}
	}

	waitErr := cmd.Wait()
	invokeDur := time.Since(invokeStart)
	log.Printf("provider: invoke done %dms events=%d text=%d bytes stderr=%q",
		invokeDur.Milliseconds(), eventCount, resultText.Len(), truncStderr(stderr.String(), 200))
	if cmdCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("claude timed out after %v", timeout)
	}

	// Try to parse the last event as result metadata.
	if lastEvent != nil {
		var parsed struct {
			Type         string                   `json:"type"`
			SessionID    string                   `json:"session_id"`
			Subtype      string                   `json:"subtype"`
			ResultText   string                   `json:"result"`
			IsError      bool                     `json:"is_error"`
			NumTurns     int                      `json:"num_turns"`
			TotalCostUSD float64                  `json:"total_cost_usd"`
			ModelUsage   map[string]jsonModelEntry `json:"modelUsage"`
		}
		if json.Unmarshal(lastEvent, &parsed) == nil && parsed.Type == "result" {
			text := parsed.ResultText
			if text == "" {
				text = resultText.String()
			}
			if text == "" {
				switch parsed.Subtype {
				case "error_max_turns":
					text = "Turn limit reached — try breaking this into smaller steps."
				default:
					if parsed.IsError {
						text = "An error occurred processing your request."
					} else {
						text = "Done (no text output)."
					}
				}
			}
			if parsed.IsError && strings.Contains(text, "No conversation found") {
				return nil, fmt.Errorf("claude: %s", text)
			}
			usedModel := "unknown"
			for m := range parsed.ModelUsage {
				usedModel = m
				break
			}
			return &Result{
				SessionID: parsed.SessionID,
				Text:      text,
				Model:     usedModel,
				CostUSD:   parsed.TotalCostUSD,
				NumTurns:  parsed.NumTurns,
			}, nil
		}
	}

	// Fallback: use accumulated text.
	accumulated := strings.TrimSpace(resultText.String())
	if accumulated != "" {
		return &Result{Text: accumulated}, nil
	}

	errOut := strings.TrimSpace(stderr.String())
	if waitErr != nil {
		if errOut != "" {
			return nil, fmt.Errorf("claude: %s", errOut)
		}
		return nil, fmt.Errorf("claude failed: %v", waitErr)
	}

	return nil, fmt.Errorf("claude returned empty response")
}

func truncStderr(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

type jsonModelEntry struct {
	CostUSD float64 `json:"costUSD"`
}

// safeEnvPrefixes are environment variable prefixes safe for the Claude subprocess.
// Everything else (secrets, tokens, internal config) is excluded.
var safeEnvPrefixes = []string{
	"PATH=",
	"TERM=",
	"LANG=",
	"LC_",
	"TZ=",
	"TMPDIR=",
	"XDG_",
	"OMP_NUM_THREADS=",
	"ANTHROPIC_",    // Claude API keys (needed for claude CLI)
	"CLAUDE_",       // Claude Code config
	"DISABLE_PROMPT", // Claude Code prompt settings
}

// safeEnv builds a subprocess environment with only safe variables plus HOME/ALF_DATA_DIR.
// It prepends $HOME/.local/bin to PATH so Claude Code finds its native binary.
func safeEnv(dataDir string) []string {
	env := make([]string, 0, 16)
	localBin := filepath.Join(dataDir, ".local", "bin")
	for _, e := range os.Environ() {
		for _, prefix := range safeEnvPrefixes {
			if strings.HasPrefix(e, prefix) {
				if strings.HasPrefix(e, "PATH=") {
					e = "PATH=" + localBin + ":" + strings.TrimPrefix(e, "PATH=")
				}
				env = append(env, e)
				break
			}
		}
	}
	env = append(env, "HOME="+dataDir, "ALF_DATA_DIR="+dataDir)
	return env
}
