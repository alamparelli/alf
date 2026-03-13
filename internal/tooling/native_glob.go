package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

const globMaxResults = 500

// GlobNativeTool lists files matching a glob pattern.
type GlobNativeTool struct {
	DataDir string // base dir; used as default when path is empty
}

func (GlobNativeTool) ToolName() string { return "glob" }

func (GlobNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "glob",
		Description: "List files matching a glob pattern. Returns file paths sorted by modification time (newest first). Use to discover files before reading them.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern to match (e.g. '**/*.go', 'src/**/*.ts', '*.md').",
				},
				"path": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Base directory to search in. Null for current directory.",
				},
			},
			"required":             []string{"pattern", "path"},
			"additionalProperties": false,
		},
	}
}

func (t GlobNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	base := args.Path
	if base == "" {
		if t.DataDir != "" {
			base = t.DataDir
		} else {
			base = "."
		}
	} else if t.DataDir != "" && !filepath.IsAbs(base) {
		base = filepath.Join(t.DataDir, base)
	}

	type entry struct {
		path    string
		modTime int64
	}
	var entries []entry

	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}
		matched, _ := filepath.Match(args.Pattern, rel)
		if !matched {
			matched = doubleStarMatch(args.Pattern, rel)
		}
		if matched && !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				entries = append(entries, entry{path: path})
				return nil
			}
			entries = append(entries, entry{path: path, modTime: info.ModTime().Unix()})
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	if len(entries) == 0 {
		return "No files found.", nil
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime > entries[j].modTime
	})

	truncated := false
	if len(entries) > globMaxResults {
		entries = entries[:globMaxResults]
		truncated = true
	}

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.path)
		sb.WriteByte('\n')
	}
	if truncated {
		sb.WriteString(fmt.Sprintf("... (truncated at %d results)", globMaxResults))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

func doubleStarMatch(pattern, name string) bool {
	pattern = filepath.ToSlash(pattern)
	name = filepath.ToSlash(name)

	parts := strings.Split(pattern, "/**/")
	if len(parts) == 1 {
		if strings.HasPrefix(pattern, "**/") {
			base := filepath.Base(name)
			matched, _ := filepath.Match(pattern[3:], base)
			return matched
		}
		matched, _ := filepath.Match(pattern, name)
		return matched
	}

	head := parts[0]
	tail := parts[len(parts)-1]

	if head != "" {
		if !strings.HasPrefix(name, head+"/") {
			return false
		}
		name = name[len(head)+1:]
	}

	base := filepath.Base(name)
	matched, _ := filepath.Match(tail, base)
	return matched
}
