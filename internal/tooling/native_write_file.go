package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileNativeTool writes content to a file, creating parent directories as needed.
type WriteFileNativeTool struct {
	DataDir string // base dir for resolving relative paths
}

func (WriteFileNativeTool) ToolName() string { return "write_file" }

func (WriteFileNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "write_file",
		Description: "Write content to a file. Creates the file and any missing parent directories. Overwrites existing content.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file to write.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Content to write to the file.",
				},
			},
			"required":             []string{"path", "content"},
			"additionalProperties": false,
		},
	}
}

func (t WriteFileNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if t.DataDir != "" && !filepath.IsAbs(args.Path) {
		args.Path = filepath.Join(t.DataDir, args.Path)
	}

	if err := os.MkdirAll(filepath.Dir(args.Path), 0755); err != nil {
		return "", fmt.Errorf("failed to create parent directories: %w", err)
	}

	if err := os.WriteFile(args.Path, []byte(args.Content), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("Written %d bytes to %s", len(args.Content), args.Path), nil
}
