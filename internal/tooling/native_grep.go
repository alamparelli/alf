package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const grepMaxOutput = 10_000

// GrepNativeTool searches file contents using ripgrep with regex support.
type GrepNativeTool struct {
	DataDir string // base dir for resolving relative paths
}

func (GrepNativeTool) ToolName() string { return "grep" }

func (GrepNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "grep",
		Description: "Search file contents using regex. Returns matching lines with file paths and line numbers. Uses ripgrep (rg) when available, falls back to grep.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Regex pattern to search for.",
				},
				"path": map[string]any{
					"type":        []string{"string", "null"},
					"description": "File or directory to search in. Null for current directory.",
				},
				"glob": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Glob pattern to filter files (e.g. '*.go', '**/*.md'). Null for all files.",
				},
				"case_insensitive": map[string]any{
					"type":        []string{"boolean", "null"},
					"description": "Case-insensitive search. Null for false.",
				},
				"context_lines": map[string]any{
					"type":        []string{"integer", "null"},
					"description": "Lines of context before and after each match. Null for 0.",
				},
				"files_only": map[string]any{
					"type":        []string{"boolean", "null"},
					"description": "Return only file paths, not matching lines. Null for false.",
				},
			},
			"required":             []string{"pattern", "path", "glob", "case_insensitive", "context_lines", "files_only"},
			"additionalProperties": false,
		},
	}
}

func (t GrepNativeTool) Run(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Pattern         string `json:"pattern"`
		Path            string `json:"path"`
		Glob            string `json:"glob"`
		CaseInsensitive bool   `json:"case_insensitive"`
		ContextLines    int    `json:"context_lines"`
		FilesOnly       bool   `json:"files_only"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if t.DataDir != "" && args.Path != "" && !filepath.IsAbs(args.Path) {
		args.Path = filepath.Join(t.DataDir, args.Path)
	} else if args.Path == "" && t.DataDir != "" {
		args.Path = t.DataDir
	}

	rgArgs := []string{"--color=never"}
	if args.FilesOnly {
		rgArgs = append(rgArgs, "--files-with-matches")
	} else {
		rgArgs = append(rgArgs, "--line-number", "--no-heading")
		if args.ContextLines > 0 {
			rgArgs = append(rgArgs, fmt.Sprintf("--context=%d", args.ContextLines))
		}
	}
	if args.CaseInsensitive {
		rgArgs = append(rgArgs, "--ignore-case")
	}
	if args.Glob != "" {
		rgArgs = append(rgArgs, "--glob", args.Glob)
	}
	rgArgs = append(rgArgs, args.Pattern)
	if args.Path != "" {
		rgArgs = append(rgArgs, args.Path)
	}

	cmd := exec.CommandContext(ctx, "rg", rgArgs...)
	out, err := cmd.Output()
	output := string(out)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "No matches found.", nil
		}
		if strings.Contains(err.Error(), "executable file not found") {
			return grepFallback(ctx, args.Pattern, args.Path, args.Glob, args.CaseInsensitive, args.FilesOnly)
		}
		stderr := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
		}
		return "", fmt.Errorf("%s %s", strings.TrimSpace(stderr), strings.TrimSpace(output))
	}

	if len(output) > grepMaxOutput {
		lines := strings.Split(output[:grepMaxOutput], "\n")
		if len(lines) > 1 {
			lines = lines[:len(lines)-1]
		}
		output = strings.Join(lines, "\n") + "\n... (truncated, use a narrower pattern or glob)"
	}
	if output == "" {
		return "No matches found.", nil
	}
	return output, nil
}

func grepFallback(ctx context.Context, pattern, path, glob string, caseInsensitive, filesOnly bool) (string, error) {
	flag := "-rn"
	if filesOnly {
		flag = "-rl"
	}
	grepArgs := []string{flag}
	if glob != "" {
		grepArgs = append(grepArgs, "--include="+glob)
	}
	if caseInsensitive {
		grepArgs = append(grepArgs, "-i")
	}
	grepArgs = append(grepArgs, pattern)
	if path != "" {
		grepArgs = append(grepArgs, path)
	} else {
		grepArgs = append(grepArgs, ".")
	}

	cmd := exec.CommandContext(ctx, "grep", grepArgs...)
	out, err := cmd.Output()
	output := string(out)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "No matches found.", nil
		}
		return "", fmt.Errorf("grep fallback failed: %w", err)
	}
	if len(output) > grepMaxOutput {
		output = output[:grepMaxOutput] + "\n... (truncated)"
	}
	return output, nil
}
