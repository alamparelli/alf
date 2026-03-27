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

	args := []string{
		"-p", "-",
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
			// No tools requested - disable all tools so Claude produces
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

	// System prompts: concatenate all into a single --system-prompt.
	// --append-system-prompt is unreliable (can cause both prompts to be
	// ignored), so we join everything into one block.
	if len(params.SystemPrompts) > 0 {
		combined := strings.Join(params.SystemPrompts, "\n\n")
		args = append(args, "--system-prompt", combined)
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

	log.Printf("provider: invoke (model=%s, resume=%q, write=%v, turns=%d)",
		model, params.ResumeID, params.WriteCapable, params.MaxTurns)

	// Preflight: verify claude binary is reachable with this env.
	preCmd := exec.CommandContext(cmdCtx, "claude", "--version")
	preCmd.Dir = dataDir
	preCmd.Env = cmd.Env
	if p.Credential != nil {
		preCmd.SysProcAttr = &syscall.SysProcAttr{Credential: p.Credential}
	}
	if preOut, preErr := preCmd.CombinedOutput(); preErr != nil {
		return nil, fmt.Errorf("claude preflight check failed: %w - %s", preErr, truncStderr(string(preOut), 300))
	}

	// Pipe prompt via stdin (-p -) to avoid OS argument list size limits.
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
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
	invokeStart := time.Now()

	// Write prompt to stdin and close to signal EOF.
	go func() {
		stdinPipe.Write([]byte(prompt))
		stdinPipe.Close()
	}()

	// Capture stderr for error reporting.
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			stderr.WriteString(scanner.Text() + "\n")
		}
	}()

	var (
		resultText   strings.Builder
		lastEvent    json.RawMessage
		inThinking   bool
		eventCount   int
		firstEvent   bool
		curToolName  string
		curToolID    string
		curToolInput strings.Builder
	)
	_ = curToolID // used in progress callback

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

	// Periodic heartbeat: log every 15s while waiting for the first event.
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	waitFirstEvent := true
	for {
		if waitFirstEvent {
			select {
			case <-heartbeat.C:
				log.Printf("provider: waiting for first event… %ds elapsed",
					int(time.Since(invokeStart).Seconds()))
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
				// Process this line below.
				lastEvent = make(json.RawMessage, len(line))
				copy(lastEvent, line)
				goto processEvent
			case <-firstTimer.C:
				// No events within timeout - kill and report stderr.
				cmd.Process.Kill()
				<-stderrDone
				cmd.Wait()
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
			}

			lastEvent = make(json.RawMessage, len(line))
			copy(lastEvent, line)
		}

	processEvent:

		var event struct {
			Type  string `json:"type"`
			Event struct {
				Type  string `json:"type"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					Thinking    string `json:"thinking"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
				ContentBlock struct {
					Type string `json:"type"`
					Name string `json:"name"`
					ID   string `json:"id"`
				} `json:"content_block"`
				// tool_result fields
				Content   json.RawMessage `json:"content"`
				ToolUseID string          `json:"tool_use_id"`
			} `json:"event"`
		}
		if json.Unmarshal(lastEvent, &event) != nil {
			continue
		}

		// Unwrap stream_event envelope.
		evtType := ""
		if event.Type == "stream_event" {
			evtType = event.Event.Type
		}

		if onProgress != nil {
			switch {
			// content_block_start: thinking - emit for each new thinking block
			case evtType == "content_block_start" && event.Event.ContentBlock.Type == "thinking":
				if !inThinking {
					onProgress(StreamEvent{Type: "thinking"})
					inThinking = true
				}
			// content_block_start: tool_use
			case evtType == "content_block_start" && event.Event.ContentBlock.Type == "tool_use":
				inThinking = false // end any open thinking block
				curToolName = event.Event.ContentBlock.Name
				curToolID = event.Event.ContentBlock.ID
				curToolInput.Reset()
				onProgress(StreamEvent{Type: "tool_use", Detail: event.Event.ContentBlock.Name})
			// content_block_delta: thinking
			case evtType == "content_block_delta" && event.Event.Delta.Type == "thinking_delta":
				text := event.Event.Delta.Text
				if text == "" {
					text = event.Event.Delta.Thinking
				}
				if text != "" {
					onProgress(StreamEvent{Type: "thinking", Text: text})
				}
			// content_block_delta: tool input
			case evtType == "content_block_delta" && event.Event.Delta.Type == "input_json_delta":
				chunk := event.Event.Delta.PartialJSON
				if chunk != "" {
					curToolInput.WriteString(chunk)
					onProgress(StreamEvent{Type: "tool_input", Detail: curToolName, Text: chunk})
				}
			// content_block_delta: text
			case evtType == "content_block_delta" && event.Event.Delta.Type == "text_delta":
				onProgress(StreamEvent{Type: "text_delta", Text: event.Event.Delta.Text})
			// content_block_start: text - reset thinking state
			case evtType == "content_block_start" && event.Event.ContentBlock.Type == "text":
				inThinking = false
			// content_block_stop
			case evtType == "content_block_stop":
				if inThinking {
					inThinking = false // allow new thinking block after tools
				}
			// tool_result (separate event type, not wrapped in content_block)
			case evtType == "tool_result" || (event.Type == "stream_event" && event.Event.ToolUseID != ""):
				resultStr := ""
				if event.Event.Content != nil {
					// Content can be string or array of blocks
					var s string
					if json.Unmarshal(event.Event.Content, &s) == nil {
						resultStr = s
					} else {
						var blocks []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						}
						if json.Unmarshal(event.Event.Content, &blocks) == nil {
							for _, b := range blocks {
								if b.Type == "text" {
									resultStr += b.Text
								}
							}
						}
					}
				}
				if len(resultStr) > 500 {
					resultStr = resultStr[:500] + "…"
				}
				onProgress(StreamEvent{Type: "tool_result", Detail: event.Event.ToolUseID, Text: resultStr})
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
	log.Printf("provider: done %dms events=%d", invokeDur.Milliseconds(), eventCount)
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
				stderrStr := strings.TrimSpace(stderr.String())
				log.Printf("provider: error subtype=%s stderr=%q raw=%s", parsed.Subtype, truncStderr(stderrStr, 500), string(lastEvent))
				return nil, fmt.Errorf("claude: %s", errDetail)
			}
			if text == "" {
				switch parsed.Subtype {
				case "error_max_turns":
					text = "Turn limit reached - try breaking this into smaller steps."
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
	// at 127.0.0.1:4751 causes Claude CLI to hang - its HTTPS CONNECT handling
	// is incompatible with Claude's OAuth/API flows. Claude Code's own tool
	// invocations (web fetches, etc.) will still respect the proxy if needed,
	// but the CLI subprocess itself must connect directly.
	"TMPDIR=",
	"XDG_",
	"OMP_NUM_THREADS=",
	"ANTHROPIC_",    // Claude API keys (needed for claude CLI)
	"CLAUDE_",       // Claude Code config
	"DISABLE_PROMPT", // Claude Code prompt settings
	"VAULT_TOKEN=",     // Vault proxy-scoped token
	"VAULT_ADDR=",      // Vault server address
	"ALF_TOOLS_SOCK=",  // Tools proxy Unix socket (replaces CC_AUTH_TOKEN)
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
					toolsDirs := filepath.Join(dataDir, "tools.d") + ":" + filepath.Join(dataDir, "tools")
					e = "PATH=" + localBin + ":" + toolsDirs + ":" + strings.TrimPrefix(e, "PATH=")
				}
				env = append(env, e)
				break
			}
		}
	}
	env = append(env, "HOME="+homeDir, "ALF_DATA_DIR="+dataDir)
	return env
}
