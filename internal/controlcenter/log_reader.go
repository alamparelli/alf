package controlcenter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// fileLogReader implements LogReader by reading log files from a directory.
type fileLogReader struct {
	dir       string
	allowlist map[string]bool
}

// NewFileLogReader creates a LogReader that reads from logDir.
// Only files in allowlist can be read. If allowlist is nil, auto-discovers .log files.
func NewFileLogReader(logDir string, allowlist []string) LogReader {
	allow := make(map[string]bool, len(allowlist))
	for _, name := range allowlist {
		allow[name] = true
	}
	return &fileLogReader{dir: logDir, allowlist: allow}
}

func (r *fileLogReader) Tail(name string, n int) ([]string, error) {
	// Validate: no path traversal, must be in allowlist or discoverable.
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return nil, fmt.Errorf("invalid log name %q", name)
	}

	if len(r.allowlist) > 0 && !r.allowlist[name] {
		return nil, fmt.Errorf("log %q not in allowlist", name)
	}

	path := filepath.Join(r.dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("read log %q: %w", name, err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if n <= 0 {
		n = 200
	}
	if n > len(lines) {
		n = len(lines)
	}
	return lines[len(lines)-n:], nil
}

func (r *fileLogReader) Available() []string {
	if len(r.allowlist) > 0 {
		names := make([]string, 0, len(r.allowlist))
		for name := range r.allowlist {
			names = append(names, name)
		}
		return names
	}

	// Auto-discover .log files.
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	return names
}
