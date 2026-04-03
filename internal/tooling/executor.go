package tooling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Executor runs tools: native Go tools first, subprocess fallback for user tools.
type Executor struct {
	DataDir   string
	HomeDir   string
	Registry  *Registry       // optional: enables JSON→CLI arg conversion for user tools
	Integrity *IntegrityGuard // optional: hash-based integrity checking for user tools
	Env       []string        // base env vars to inject
	Timeout   time.Duration   // per-tool timeout; 0 = 30s
	natives   map[string]NativeTool
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
	log.Printf("tooling: executing tool %s args=%s", call.Name, call.Arguments)
	if n, ok := e.natives[call.Name]; ok {
		out, err := n.Run(ctx, call.Arguments)
		if err != nil {
			return CallResult{ID: call.ID, Output: err.Error(), IsError: true}
		}
		if out == "" {
			out = "(no output)"
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

	// Integrity check for user tools (not system tools.d/).
	if e.Integrity != nil && IsUserTool(toolPath, e.DataDir) {
		if err := e.Integrity.Check(toolPath); err != nil {
			return CallResult{
				ID:      call.ID,
				Output:  fmt.Sprintf("tool %q is quarantined: %v", call.Name, err),
				IsError: true,
			}
		}
	}

	// Convert JSON args to CLI arguments using schema conventions.
	cliArgs := e.jsonToCLI(call.Name, call.Arguments)
	cmd := exec.CommandContext(ctx, toolPath, cliArgs...)
	if len(cliArgs) == 0 {
		// No schema or conversion failed - fall back to JSON on stdin.
		cmd.Stdin = strings.NewReader(call.Arguments)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = e.buildEnvForTool(toolPath)

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
// Defense-in-depth: rejects names containing path separators or traversal sequences.
func (e *Executor) resolveTool(name string) string {
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return ""
	}
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
	return e.buildEnvForTool("")
}

// buildEnvForTool creates the environment for tool execution.
// If toolPath is within apps/{slug}/, ALF_APP_DATA_DIR is injected.
func (e *Executor) buildEnvForTool(toolPath string) []string {
	env := []string{
		"HOME=" + e.HomeDir,
		"ALF_DATA_DIR=" + e.DataDir,
		"PATH=" + filepath.Join(e.DataDir, "tools.d") + ":" + filepath.Join(e.DataDir, "tools") + ":/usr/local/bin:/usr/bin:/bin",
	}
	env = append(env, e.Env...)

	// Inject ALF_APP_DATA_DIR for marketplace app tools.
	if toolPath != "" {
		if slug := appSlugFromPath(toolPath); slug != "" {
			appDataDir := filepath.Join(e.DataDir, "apps", slug, "data")
			env = append(env, "ALF_APP_DATA_DIR="+appDataDir)
		}
	}

	return env
}

// appSlugFromPath extracts the app slug if toolPath resolves into apps/{slug}/.
func appSlugFromPath(toolPath string) string {
	resolved, err := filepath.EvalSymlinks(toolPath)
	if err != nil {
		return ""
	}
	// Look for /apps/{slug}/bin/ pattern in the resolved path.
	parts := strings.Split(filepath.ToSlash(resolved), "/")
	for i, p := range parts {
		if p == "apps" && i+2 < len(parts) && parts[i+2] == "bin" {
			return parts[i+1]
		}
	}
	return ""
}

// jsonToCLI converts JSON arguments to CLI arguments using the tool's schema.
// Uses x-positional from the schema to determine positional args vs flags.
//
// Convention:
//   - Fields listed in "x-positional" are emitted as positional args (in order).
//   - Remaining fields become --key value flags.
//   - Boolean true → --key (no value). Boolean false → omitted.
//   - Null/empty values → omitted.
//
// Example: {"action":"add","text":"hello","priority":"high"} with x-positional:["action","text"]
//   → ["add", "hello", "--priority", "high"]
func (e *Executor) jsonToCLI(toolName, argsJSON string) []string {
	if e.Registry == nil {
		return nil
	}
	schema, ok := e.Registry.Get(toolName)
	if !ok {
		return nil
	}

	// Parse the JSON arguments.
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil
	}
	if len(args) == 0 {
		return nil
	}

	// Extract x-positional from schema parameters.
	var positional []string
	if params, ok := schema.Parameters["x-positional"]; ok {
		if arr, ok := params.([]any); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					positional = append(positional, s)
				}
			}
		}
	}

	positionalSet := make(map[string]bool, len(positional))
	for _, p := range positional {
		positionalSet[p] = true
	}

	var result []string

	// Emit positional args in order.
	for _, key := range positional {
		val, exists := args[key]
		if !exists || val == nil {
			continue
		}
		s := formatValue(val)
		if s != "" {
			result = append(result, s)
		}
	}

	// Emit remaining fields as --key value flags.
	for key, val := range args {
		if positionalSet[key] || val == nil {
			continue
		}
		switch v := val.(type) {
		case bool:
			if v {
				result = append(result, "--"+key)
			}
		case string:
			if v != "" {
				result = append(result, "--"+key, v)
			}
		default:
			s := formatValue(val)
			if s != "" {
				result = append(result, "--"+key, s)
			}
		}
	}

	return result
}

func formatValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int(val)) {
			return fmt.Sprintf("%d", int(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", val)
	}
}
