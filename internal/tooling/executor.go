package tooling

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Executor runs tools: native Go tools first, subprocess fallback for user tools.
type Executor struct {
	DataDir string
	HomeDir string
	Env     []string      // base env vars to inject
	Timeout time.Duration // per-tool timeout; 0 = 30s
	natives map[string]NativeTool
}

// RegisterNative adds a Go-native tool. Native tools take priority over subprocess tools.
func (e *Executor) RegisterNative(t NativeTool) {
	if e.natives == nil {
		e.natives = make(map[string]NativeTool)
	}
	e.natives[t.ToolName()] = t
}

// CallRequest represents a single tool call from the LLM.
type CallRequest struct {
	ID        string // tool_call ID from the API
	Name      string
	Arguments string // raw JSON from LLM
}

// CallResult is the output of a tool execution.
type CallResult struct {
	ID      string
	Output  string
	IsError bool
}

// Execute runs a tool. Native Go tools are tried first, then subprocess binaries.
func (e *Executor) Execute(ctx context.Context, call CallRequest) CallResult {
	if n, ok := e.natives[call.Name]; ok {
		out, err := n.Run(ctx, call.Arguments)
		if err != nil {
			return CallResult{ID: call.ID, Output: err.Error(), IsError: true}
		}
		return CallResult{ID: call.ID, Output: out}
	}
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	toolPath := e.resolveTool(call.Name)
	if toolPath == "" {
		return CallResult{
			ID:      call.ID,
			Output:  fmt.Sprintf("tool %q not found", call.Name),
			IsError: true,
		}
	}

	cmd := exec.CommandContext(ctx, toolPath)
	cmd.Stdin = strings.NewReader(call.Arguments)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = e.buildEnv()

	err := cmd.Run()

	output := strings.TrimSpace(stdout.String())
	if err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if ctx.Err() == context.DeadlineExceeded {
			return CallResult{
				ID:      call.ID,
				Output:  fmt.Sprintf("tool %q timed out after %s", call.Name, timeout),
				IsError: true,
			}
		}
		if errMsg != "" {
			if output != "" {
				output = output + "\n" + errMsg
			} else {
				output = errMsg
			}
		}
		if output == "" {
			output = fmt.Sprintf("tool %q failed: %v", call.Name, err)
		}
		return CallResult{
			ID:      call.ID,
			Output:  output,
			IsError: true,
		}
	}

	if output == "" {
		output = "(no output)"
	}
	return CallResult{
		ID:     call.ID,
		Output: output,
	}
}

// resolveTool finds a tool binary in tools.d/ then tools/.
// Tries the exact name first, then the original name with hyphens restored
// (since tool names are sanitized for API compatibility: hyphens → underscores).
func (e *Executor) resolveTool(name string) string {
	// Try exact name, then with underscores replaced by hyphens.
	candidates := []string{name}
	if strings.Contains(name, "_") {
		candidates = append(candidates, strings.ReplaceAll(name, "_", "-"))
	}
	for _, dir := range []string{
		filepath.Join(e.DataDir, "tools.d"),
		filepath.Join(e.DataDir, "tools"),
	} {
		for _, candidate := range candidates {
			path := filepath.Join(dir, candidate)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path
			}
		}
	}
	return ""
}

func (e *Executor) buildEnv() []string {
	env := []string{
		"HOME=" + e.HomeDir,
		"ALF_DATA_DIR=" + e.DataDir,
		"PATH=" + filepath.Join(e.DataDir, "tools.d") + ":" + filepath.Join(e.DataDir, "tools") + ":/usr/local/bin:/usr/bin:/bin",
	}
	env = append(env, e.Env...)
	return env
}
