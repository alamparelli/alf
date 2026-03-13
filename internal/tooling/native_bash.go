package tooling

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"encoding/json"
)

const (
	bashMaxOutput      = 10_000
	bashDefaultTimeout = 30
)

// BashNativeTool executes shell commands.
type BashNativeTool struct {
	DataDir string // working directory for commands; defaults to /
}

func (BashNativeTool) ToolName() string { return "bash" }

func (BashNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "bash",
		Description: "Execute a shell command and return its output. Use for file operations, running scripts, checking system state, git operations, etc.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute.",
				},
				"timeout": map[string]any{
					"type":        []string{"integer", "null"},
					"description": fmt.Sprintf("Timeout in seconds (default %d, max 300). Null for default.", bashDefaultTimeout),
				},
			},
			"required":             []string{"command", "timeout"},
			"additionalProperties": false,
		},
	}
}

func (t BashNativeTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := args.Timeout
	if timeout <= 0 {
		timeout = bashDefaultTimeout
	}
	if timeout > 300 {
		timeout = 300
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", args.Command)
	if t.DataDir != "" {
		cmd.Dir = t.DataDir
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	output := buf.String()
	if len(output) > bashMaxOutput {
		output = output[:bashMaxOutput] + fmt.Sprintf("\n... (truncated, %d chars total)", len(output))
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out after %ds\n%s", timeout, output)
		}
		if output == "" {
			output = err.Error()
		}
		return "", fmt.Errorf("%s", output)
	}
	return output, nil
}
