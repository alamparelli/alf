package tooling

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolvePath resolves a path relative to dataDir (if relative) and cleans it.
func ResolvePath(dataDir, path string) string {
	if dataDir != "" && !filepath.IsAbs(path) {
		path = filepath.Join(dataDir, path)
	}
	return filepath.Clean(path)
}

// CheckBoundary verifies that path stays within dataDir after resolving symlinks.
// Returns the resolved real path or an error if it escapes the workspace.
func CheckBoundary(dataDir, path string) (string, error) {
	if dataDir == "" {
		return path, nil
	}

	// Resolve the real dataDir (it may itself be behind symlinks/mounts).
	realDataDir, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		realDataDir = filepath.Clean(dataDir)
	}

	// Resolve the target path. For new files that don't exist yet,
	// resolve the parent directory instead.
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("cannot resolve path: %w", err)
		}
		// File doesn't exist yet - resolve parent dir + keep the filename.
		parentReal, err2 := filepath.EvalSymlinks(filepath.Dir(path))
		if err2 != nil {
			// Parent also doesn't exist, fall back to lexical check.
			parentReal = filepath.Clean(filepath.Dir(path))
		}
		realPath = filepath.Join(parentReal, filepath.Base(path))
	}

	rel, err := filepath.Rel(realDataDir, realPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes workspace boundary: %s", path)
	}
	return realPath, nil
}
