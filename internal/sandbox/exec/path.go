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
	// walk ancestors until we find one that does exist and
	// EvalSymlinks *that* — otherwise a symlink sitting on the
	// existing prefix (e.g. /workspace/link -> /etc) is never resolved
	// and a write through a deep non-existent tail escapes the
	// workspace (see #385-7).
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("cannot resolve path: %w", err)
		}
		existingReal, tail := resolveExistingAncestor(path)
		realPath = filepath.Join(append([]string{existingReal}, tail...)...)
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

// resolveExistingAncestor walks path's ancestors until one exists,
// EvalSymlinks that (so any symlink on the existing prefix is
// resolved), and returns (realExistingPath, tailSegments) where
// tailSegments is the non-existent suffix in original order.
//
// Centralising this lets CheckBoundary treat "path doesn't exist yet"
// safely: a symlink further up the chain can no longer smuggle the
// target outside the boundary (#385-7).
func resolveExistingAncestor(path string) (string, []string) {
	cursor := filepath.Clean(path)
	var tail []string
	for {
		if _, err := os.Lstat(cursor); err == nil {
			break
		} else if !os.IsNotExist(err) {
			// Permission denied or similar — fall back to lexical.
			return filepath.Clean(path), nil
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			// Reached filesystem root with nothing existing — fall back
			// to lexical resolution of the full path.
			return filepath.Clean(path), nil
		}
		tail = append([]string{filepath.Base(cursor)}, tail...)
		cursor = parent
	}
	real, err := filepath.EvalSymlinks(cursor)
	if err != nil {
		real = filepath.Clean(cursor)
	}
	return real, tail
}
