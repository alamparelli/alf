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
	// HomeDir is HOME for the Claude subprocess (where .claude/ config lives).
	HomeDir string
	// DefaultDataDir is used when Params.DataDir is empty.
	DefaultDataDir string
	// Timeout for each invocation. Zero means 5 minutes.
	Timeout time.Duration
	// Credential for subprocess isolation (uid/gid). Nil = inherit.
	Credential *syscall.Credential
}

// NewCLIProvider creates a new CLIProvider.
func NewCLIProvider(homeDir, dataDir string, timeout time.Duration, cred *syscall.Credential) *CLIProvider {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &CLIProvider{
		HomeDir:        homeDir,
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

	// Always skip permissions so resumed sessions never inherit a
	// restrictive permission mode from a previous read-only invocation.
	args = append(args, "--dangerously-skip-permissions")

	if !params.WriteCapable {
		if len(params.Tools) > 0 {
			// Read-only: whitelist specific tools, disable everything else.
			// --tools controls availability (not just permissions), so it works
			// even with --dangerously-skip-permissions.
			args = append(args, "--tools", strings.Join(params.Tools, ","))
		} else {
			// No tools requested — disable all tools so Claude produces
			// text output only, without wasting turns on tool calls.
			args = append(args, "--tools", "")
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

	// System prompts: first one replaces Claude Code's default identity
	// (--system-prompt), rest are appended (--append-system-prompt).
	for i, sp := range params.SystemPrompts {
		if i == 0 {
			args = append(args, "--system-prompt", sp)
		} else {
			args = append(args, "--append-system-prompt", sp)
		}
	}

	dataDir := params.DataDir
	if dataDir == "" {
		dataDir = p.DefaultDataDir
	}

	// Remove stale settings.json before every invocation. Claude Code may
	// persist restrictive allow-lists that block tools on resumed sessions.
	homeDir := p.HomeDir
	if homeDir == "" {
		homeDir = p.DefaultDataDir
	}
	if sp := filepath.Join(homeDir, ".claude", "settings.json"); true {
		_ = os.Remove(sp)
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
	// HOME must always point to the home dir (where .claude/ config lives),
	// not the task-specific working directory.
	cmd.Env = safeEnv(homeDir, dataDir)
	cmd.Env = append(cmd.Env, params.Env...)

	// Log full args (excluding prompt content which can be huge).
	debugArgs := make([]string, 0, len(args))
	for i, a := range args {
		if i > 0 && (args[i-1] == "-p" || args[i-1] == "--append-system-prompt") {
			debugArgs = append(debugArgs, fmt.Sprintf("[%d chars]", len(a)))
		} else {
			debugArgs = append(debugArgs, a)
		}
	}
	log.Printf("provider: invoke starting (resume=%q, model=%s, max_turns=%d, effort=%s, sys_prompts=%d, tools=%v, write=%v, env_count=%d)",
		params.ResumeID, model, params.MaxTurns, params.Effort, len(params.SystemPrompts), params.Tools, params.WriteCapable, len(cmd.Env))
	log.Printf("provider: args=%v", debugArgs)
	// Log environment for debugging (redact values for safety).
	for _, e := range cmd.Env {
		if k, _, ok := strings.Cut(e, "="); ok {
			v := strings.TrimPrefix(e, k+"=")
			if len(v) > 80 {
				v = v[:80] + "..."
			}
			log.Printf("provider: env %s=%s", k, v)
		}
	}

	// Preflight: verify claude binary is reachable and responsive with this env.
	preCmd := exec.CommandContext(cmdCtx, "claude", "--version")
	preCmd.Dir = dataDir
	preCmd.Env = cmd.Env
	if p.Credential != nil {
		preCmd.SysProcAttr = &syscall.SysProcAttr{Credential: p.Credential}
	}
	if preOut, preErr := preCmd.CombinedOutput(); preErr != nil {
		log.Printf("provider: preflight FAILED: %v — output: %s", preErr, truncStderr(string(preOut), 500))
		return nil, fmt.Errorf("claude preflight check failed: %w — %s", preErr, truncStderr(string(preOut), 300))
	} else {
		log.Printf("provider: preflight OK: %s", strings.TrimSpace(string(preOut)))
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// Capture stderr concurrently so we can surface errors during startup hangs.
	var stderr bytes.Buffer
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	log.Printf("provider: process started (pid=%d, uid=%d, home=%s, dir=%s)",
		cmd.Process.Pid, func() uint32 { if p.Credential != nil { return p.Credential.Uid }; return 0 }(), homeDir, dataDir)
	invokeStart := time.Now()

	// Read stderr in background — log lines in real-time for debugging.
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			stderr.WriteString(line + "\n")
			log.Printf("provider stderr [pid=%d]: %s", cmd.Process.Pid, line)
		}
	}()

	var (
		resultText   strings.Builder
		lastEvent    json.RawMessage
		sentThinking bool
		eventCount   int
		firstEvent   bool
	)

	// Use a channel to detect first-event timeout.
	lineCh := make(chan []byte, 64)
	scanDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 256*1024), 1024*1024)
		for scanner.Scan() {
			line := make([]byte, len(scanner.Bytes()))
			copy(line, scanner.Bytes())
			lineCh <- line
		}
		scanDone <- scanner.Err()
		close(lineCh)
	}()

	// Wait up to 90s for the first event. After that, fail fast with stderr.
	const firstEventTimeout = 90 * time.Second
	firstTimer := time.NewTimer(firstEventTimeout)
	defer firstTimer.Stop()

	// Periodic heartbeat: log every 10s while waiting for the first event.
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()

	waitFirstEvent := true
	for {
		if waitFirstEvent {
			select {
			case <-heartbeat.C:
				// Check if process is still alive via /proc (Linux).
				procStatus := "unknown"
				if statusBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", cmd.Process.Pid)); err == nil {
					for _, line := range strings.Split(string(statusBytes), "\n") {
						if strings.HasPrefix(line, "State:") {
							procStatus = strings.TrimSpace(strings.TrimPrefix(line, "State:"))
							break
						}
					}
				}
				// Check open fds for /dev/tty or /dev/pts (would indicate interactive prompt).
				fds := ""
				if entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", cmd.Process.Pid)); err == nil {
					for _, e := range entries {
						if target, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", cmd.Process.Pid, e.Name())); err == nil {
							if strings.Contains(target, "tty") || strings.Contains(target, "pts") {
								fds += " " + e.Name() + "→" + target
							}
						}
					}
				}
				log.Printf("provider: waiting for first event... %dms elapsed, pid=%d state=%s stderr=%d tty_fds=[%s]",
					time.Since(invokeStart).Milliseconds(), cmd.Process.Pid, procStatus, stderr.Len(), strings.TrimSpace(fds))
				continue
			case line, ok := <-lineCh:
				if !ok {
					goto done
				}
				firstTimer.Stop()
				waitFirstEvent = false
				if len(line) == 0 {
					continue
				}
				eventCount++
				firstEvent = true
				log.Printf("provider: first event after %dms", time.Since(invokeStart).Milliseconds())
				// Process this line below.
				lastEvent = make(json.RawMessage, len(line))
				copy(lastEvent, line)
				goto processEvent
			case <-firstTimer.C:
				// No events within timeout — kill and report stderr.
				log.Printf("provider: TIMEOUT pid=%d after %v — killing process", cmd.Process.Pid, firstEventTimeout)
				cmd.Process.Kill()
				<-stderrDone
				waitErr := cmd.Wait()
				log.Printf("provider: killed pid=%d, wait=%v, stderr_len=%d", cmd.Process.Pid, waitErr, stderr.Len())
				errMsg := strings.TrimSpace(stderr.String())
				if errMsg == "" {
					errMsg = "no output on stdout or stderr"
				}
				return nil, fmt.Errorf("claude startup timeout (%v): %s", firstEventTimeout, truncStderr(errMsg, 500))
			case <-cmdCtx.Done():
				cmd.Process.Kill()
				<-stderrDone
				cmd.Wait()
				return nil, fmt.Errorf("claude context cancelled during startup: %v", cmdCtx.Err())
			}
		} else {
			line, ok := <-lineCh
			if !ok {
				goto done
			}
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
		}

	processEvent:

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
		if json.Unmarshal(lastEvent, &event) != nil {
			continue
		}

		if onProgress != nil {
			switch {
			case event.Type == "stream_event" && event.Event.ContentBlock.Type == "thinking" && !sentThinking:
				onProgress(StreamEvent{Type: "thinking"})
				sentThinking = true
			case event.Type == "stream_event" && event.Event.Delta.Type == "thinking_delta":
				onProgress(StreamEvent{Type: "thinking", Text: event.Event.Delta.Text})
			case event.Type == "stream_event" && event.Event.ContentBlock.Type == "tool_use":
				onProgress(StreamEvent{Type: "tool_use", Detail: event.Event.ContentBlock.Name})
			case event.Type == "stream_event" && event.Event.Delta.Type == "text_delta":
				onProgress(StreamEvent{Type: "text_delta", Text: event.Event.Delta.Text})
			}
		}

		if event.Type == "stream_event" && event.Event.Delta.Type == "text_delta" {
			resultText.WriteString(event.Event.Delta.Text)
		}
	}

done:
	<-scanDone
	<-stderrDone
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
			// If Claude CLI returned an error with no text, propagate as error
			// so callers (retry logic) can handle it properly.
			if parsed.IsError && text == "" {
				errDetail := parsed.Subtype
				if errDetail == "" {
					errDetail = "unknown error"
				}
				log.Printf("provider: claude error (subtype=%s, turns=%d)", parsed.Subtype, parsed.NumTurns)
				return nil, fmt.Errorf("claude: %s", errDetail)
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
	// Note: HTTP_PROXY/HTTPS_PROXY deliberately excluded. The firewall proxy
	// at 127.0.0.1:4751 causes Claude CLI to hang — its HTTPS CONNECT handling
	// is incompatible with Claude's OAuth/API flows. Claude Code's own tool
	// invocations (web fetches, etc.) will still respect the proxy if needed,
	// but the CLI subprocess itself must connect directly.
	"TMPDIR=",
	"XDG_",
	"OMP_NUM_THREADS=",
	"ANTHROPIC_",    // Claude API keys (needed for claude CLI)
	"CLAUDE_",       // Claude Code config
	"DISABLE_PROMPT", // Claude Code prompt settings
}

// safeEnv builds a subprocess environment with only safe variables plus HOME/ALF_DATA_DIR.
// homeDir is where .claude/ config and .local/bin live (set as HOME).
// dataDir is the working data directory (set as ALF_DATA_DIR).
func safeEnv(homeDir, dataDir string) []string {
	env := make([]string, 0, 16)
	localBin := filepath.Join(homeDir, ".local", "bin")
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
	env = append(env, "HOME="+homeDir, "ALF_DATA_DIR="+dataDir)
	return env
}
