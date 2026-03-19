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
	"strings"
	"time"
)

// CodexProvider invokes OpenAI Codex CLI as a subprocess (spawn-per-call).
type CodexProvider struct {
	// DefaultDataDir is used when Params.DataDir is empty.
	DefaultDataDir string
	// Timeout for each invocation. Zero means 5 minutes.
	Timeout time.Duration
	// APIKey is the OpenAI/Codex API key from vault.
	APIKey string
}

// NewCodexProvider creates a new CodexProvider.
func NewCodexProvider(dataDir string, timeout time.Duration, apiKey string) *CodexProvider {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &CodexProvider{
		DefaultDataDir: dataDir,
		Timeout:        timeout,
		APIKey:         apiKey,
	}
}

// Invoke spawns a codex exec subprocess, parses JSONL events, and returns the result.
func (p *CodexProvider) Invoke(ctx context.Context, prompt string, params Params, onProgress OnProgress) (*Result, error) {
	model := params.Model
	if model == "" {
		model = "gpt-5-codex"
	}

	// Build prompt: prepend system prompts if any.
	fullPrompt := prompt
	if len(params.SystemPrompts) > 0 {
		fullPrompt = strings.Join(params.SystemPrompts, "\n\n") + "\n\n" + prompt
	}

	args := []string{
		"exec",
		"--json",
		"--full-auto",
		"--model", model,
	}

	dataDir := params.DataDir
	if dataDir == "" {
		dataDir = p.DefaultDataDir
	}
	args = append(args, "-C", dataDir)
	args = append(args, fullPrompt)

	timeout := p.Timeout
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "codex", args...)
	cmd.Dir = dataDir
	cmd.Env = codexEnv(p.APIKey)
	cmd.Env = append(cmd.Env, params.Env...)

	log.Printf("codex: invoke (model=%s)", model)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("codex stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("codex stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex: %w", err)
	}
	invokeStart := time.Now()

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
		sessionID    string
		inputTokens  int
		outputTokens int
		eventCount   int
	)

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

	// Wait up to 90s for the first event.
	const firstEventTimeout = 90 * time.Second
	firstTimer := time.NewTimer(firstEventTimeout)
	defer firstTimer.Stop()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	waitFirstEvent := true
	for {
		var line []byte
		var ok bool

		if waitFirstEvent {
			select {
			case <-heartbeat.C:
				log.Printf("codex: waiting for first event… %ds elapsed",
					int(time.Since(invokeStart).Seconds()))
				continue
			case line, ok = <-lineCh:
				if !ok {
					goto done
				}
				firstTimer.Stop()
				waitFirstEvent = false
			case <-firstTimer.C:
				cmd.Process.Kill()
				<-stderrDone
				cmd.Wait()
				errMsg := strings.TrimSpace(stderr.String())
				if errMsg == "" {
					errMsg = "no output on stdout or stderr"
				}
				return nil, fmt.Errorf("codex startup timeout (%v): %s", firstEventTimeout, truncStderr(errMsg, 500))
			case <-cmdCtx.Done():
				cmd.Process.Kill()
				<-stderrDone
				cmd.Wait()
				return nil, fmt.Errorf("codex context cancelled during startup: %v", cmdCtx.Err())
			}
		} else {
			line, ok = <-lineCh
			if !ok {
				goto done
			}
		}

		if len(line) == 0 {
			continue
		}
		eventCount++

		var evt codexEvent
		if json.Unmarshal(line, &evt) != nil {
			continue
		}

		switch evt.Type {
		case "thread.started":
			sessionID = evt.ThreadID

		case "item.started":
			if evt.Item.Type == "command_execution" && onProgress != nil {
				onProgress(StreamEvent{Type: "tool_use", Detail: evt.Item.Command})
			}

		case "item.completed":
			switch evt.Item.Type {
			case "agent_message":
				if evt.Item.Text != "" {
					resultText.WriteString(evt.Item.Text)
					if onProgress != nil {
						onProgress(StreamEvent{Type: "text_delta", Text: evt.Item.Text})
					}
				}
			case "command_execution":
				if onProgress != nil {
					output := evt.Item.Output
					if len(output) > 500 {
						output = output[:500] + "…"
					}
					onProgress(StreamEvent{Type: "tool_result", Detail: evt.Item.ID, Text: output})
				}
			}

		case "turn.completed":
			if evt.Usage.InputTokens > 0 {
				inputTokens += evt.Usage.InputTokens
			}
			if evt.Usage.OutputTokens > 0 {
				outputTokens += evt.Usage.OutputTokens
			}

		case "error":
			errMsg := evt.Message
			if errMsg == "" {
				errMsg = "unknown codex error"
			}
			cmd.Process.Kill()
			<-stderrDone
			cmd.Wait()
			return nil, fmt.Errorf("codex: %s", errMsg)
		}
	}

done:
	<-scanDone
	<-stderrDone
	waitErr := cmd.Wait()
	invokeDur := time.Since(invokeStart)
	log.Printf("codex: done %dms events=%d", invokeDur.Milliseconds(), eventCount)
	if cmdCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("codex timed out after %v", timeout)
	}

	accumulated := strings.TrimSpace(resultText.String())
	if accumulated != "" {
		return &Result{
			SessionID:    sessionID,
			Text:         accumulated,
			Model:        model,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
		}, nil
	}

	errOut := strings.TrimSpace(stderr.String())
	if waitErr != nil {
		if errOut != "" {
			return nil, fmt.Errorf("codex: %s", truncStderr(errOut, 500))
		}
		return nil, fmt.Errorf("codex failed: %v", waitErr)
	}

	return nil, fmt.Errorf("codex returned empty response")
}

// codexEvent represents a JSONL event from codex exec --json.
type codexEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id,omitempty"`
	Message  string `json:"message,omitempty"`
	Item     struct {
		ID      string `json:"id,omitempty"`
		Type    string `json:"type,omitempty"`
		Text    string `json:"text,omitempty"`
		Command string `json:"command,omitempty"`
		Output  string `json:"output,omitempty"`
	} `json:"item,omitempty"`
	Usage struct {
		InputTokens       int `json:"input_tokens,omitempty"`
		CachedInputTokens int `json:"cached_input_tokens,omitempty"`
		OutputTokens      int `json:"output_tokens,omitempty"`
	} `json:"usage,omitempty"`
}

// codexEnv builds a minimal environment for the codex subprocess.
// If apiKey is empty, Codex falls back to ~/.codex/auth.json (ChatGPT login).
func codexEnv(apiKey string) []string {
	env := make([]string, 0, 8)
	// Pass through essential variables.
	for _, key := range []string{"PATH", "TERM", "LANG", "HOME", "TMPDIR", "TZ"} {
		if v := os.Getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	if apiKey != "" {
		env = append(env, "CODEX_API_KEY="+apiKey)
	}
	return env
}
