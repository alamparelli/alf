package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RemoveNativeTool deletes files or directories with post-deletion verification.
type RemoveNativeTool struct {
	DataDir string // base dir for resolving relative paths
}

func (RemoveNativeTool) ToolName() string { return "remove" }

func (RemoveNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "remove",
		Description: "Delete a file or directory. Verifies deletion succeeded. Refuses to delete protected paths (/, /home, data root).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the file or directory to delete.",
				},
				"recursive": map[string]any{
					"type":        "boolean",
					"description": "Remove directories and their contents recursively. Required for non-empty directories.",
				},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
	}
}

// protectedPaths that must never be deleted.
var protectedPaths = map[string]bool{
	"/":     true,
	"/home": true,
	"/root": true,
	"/etc":  true,
	"/var":  true,
	"/usr":  true,
	"/bin":  true,
	"/opt":  true,
	"/tmp":  true,
}

func (t RemoveNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Resolve relative paths against DataDir.
	if t.DataDir != "" && !filepath.IsAbs(args.Path) {
		args.Path = filepath.Join(t.DataDir, args.Path)
	}
	args.Path = filepath.Clean(args.Path)

	// Block protected paths.
	if protectedPaths[args.Path] {
		return "", fmt.Errorf("refusing to delete protected path: %s", args.Path)
	}
	// Block deletion of the data root itself.
	if t.DataDir != "" && args.Path == filepath.Clean(t.DataDir) {
		return "", fmt.Errorf("refusing to delete data root: %s", args.Path)
	}
	// Block paths outside DataDir when DataDir is set.
	if t.DataDir != "" {
		rel, err := filepath.Rel(t.DataDir, args.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("refusing to delete path outside workspace: %s", args.Path)
		}
	}

	// Check target exists.
	info, err := os.Lstat(args.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("path does not exist: %s", args.Path)
		}
		return "", fmt.Errorf("cannot stat path: %w", err)
	}

	isDir := info.IsDir()

	// Require recursive flag for directories.
	if isDir && !args.Recursive {
		return "", fmt.Errorf("%s is a directory — set recursive=true to delete it and its contents", args.Path)
	}

	// Perform deletion.
	if isDir {
		err = os.RemoveAll(args.Path)
	} else {
		err = os.Remove(args.Path)
	}
	if err != nil {
		return "", fmt.Errorf("deletion failed: %w", err)
	}

	// Verify deletion.
	if _, err := os.Lstat(args.Path); err == nil {
		return "", fmt.Errorf("deletion verification failed: %s still exists", args.Path)
	}

	kind := "file"
	if isDir {
		kind = "directory"
	}
	return fmt.Sprintf("Deleted %s: %s", kind, args.Path), nil
}
