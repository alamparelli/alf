package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	readFileMaxBytes = 100_000
	readFileMaxLines = 2_000
)

// ReadFileNativeTool reads file contents with optional line range.
type ReadFileNativeTool struct{}

func (ReadFileNativeTool) ToolName() string { return "read_file" }

func (ReadFileNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "read_file",
		Description: "Read the contents of a file. Supports optional line range to avoid reading huge files entirely.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file to read.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Line number to start reading from (1-based, default 1).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": fmt.Sprintf("Maximum number of lines to return (default %d).", readFileMaxLines),
				},
			},
			"required": []string{"path"},
		},
	}
}

func (ReadFileNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	info, err := os.Stat(args.Path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory, not a file")
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return "", err
	}

	offset := args.Offset
	if offset < 1 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 {
		limit = readFileMaxLines
	}

	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	startIdx := offset - 1
	if startIdx >= totalLines {
		return "", fmt.Errorf("offset %d exceeds file length (%d lines)", offset, totalLines)
	}

	endIdx := startIdx + limit
	if endIdx > totalLines {
		endIdx = totalLines
	}

	selected := strings.Join(lines[startIdx:endIdx], "\n")

	truncated := false
	if len(selected) > readFileMaxBytes {
		selected = selected[:readFileMaxBytes]
		truncated = true
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("File: %s (%d lines total)\n", args.Path, totalLines))
	if offset > 1 || endIdx < totalLines {
		sb.WriteString(fmt.Sprintf("Showing lines %d-%d\n", offset, startIdx+strings.Count(selected, "\n")+1))
	}
	sb.WriteString("---\n")
	sb.WriteString(selected)
	if truncated {
		sb.WriteString(fmt.Sprintf("\n... (truncated at %d bytes)", readFileMaxBytes))
	}
	return sb.String(), nil
}
