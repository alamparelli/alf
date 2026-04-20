// Package exec is the subprocess-execution facet of Sandbox: it hardens
// exec.Cmd invocations with chroot / bwrap / setuid (Linux) or a no-op
// fallback (other OSes), and exposes path helpers that keep every
// filesystem access inside the capability data directory.
//
// Moved from internal/tooling during #339 (Step 3). Tracked as a
// transitional strategy: the acceptance criteria for #339 say this whole
// facet can be retired in favour of wazero once WASM lands.
package exec

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

	// Deny writes to internal daemon directories — LLM must not tamper with
	// integrity state, quarantine files, or daemon internals.
	for _, deny := range []string{".daemon", "logs/tool-changes.log"} {
		if strings.HasPrefix(rel, deny) {
			return "", fmt.Errorf("path is protected (internal system directory): %s", path)
		}
	}

	return realPath, nil
}
