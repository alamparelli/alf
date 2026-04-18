package wasm

import (
	"fmt"
	"os"
	"path/filepath"
)

// DiscoveredCapability represents a WASM capability found on disk.
// It is returned by ScanDir and consumed by tooling/native_wasm adapters.
type DiscoveredCapability struct {
	ManifestPath string    // absolute path to manifest.toml
	Manifest     *Manifest // parsed manifest
}

// ScanDir walks one level into root and returns every <root>/<slug>/manifest.toml
// it can parse and validate. Unreadable or invalid manifests are logged and
// skipped — one bad capability never breaks discovery of the others.
//
// Returns nil if root does not exist (common case for a fresh install with
// no user-placed WASM tools/apps).
func ScanDir(root string) ([]DiscoveredCapability, error) {
	st, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", root, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}

	var found []DiscoveredCapability
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mPath := filepath.Join(root, e.Name(), "manifest.toml")
		if _, err := os.Stat(mPath); err != nil {
			continue // no manifest in this subdir
		}
		m, err := LoadManifest(mPath)
		if err != nil {
			// Intentionally not fatal — a bad manifest must not break
			// discovery of the others.
			fmt.Fprintf(os.Stderr, "[wasm-discovery] skipping %s: %v\n", mPath, err)
			continue
		}
		abs, _ := filepath.Abs(mPath)
		found = append(found, DiscoveredCapability{ManifestPath: abs, Manifest: m})
	}
	return found, nil
}

// ScanDirs aggregates multiple roots in priority order. Later entries
// override earlier ones by name (so user-placed capabilities at
// /home/alf/data/wasm-tools/<name> beat a bundled one of the same name).
func ScanDirs(roots ...string) []DiscoveredCapability {
	byName := make(map[string]DiscoveredCapability)
	var order []string
	for _, r := range roots {
		caps, err := ScanDir(r)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[wasm-discovery] %s: %v\n", r, err)
			continue
		}
		for _, c := range caps {
			if _, seen := byName[c.Manifest.Name]; !seen {
				order = append(order, c.Manifest.Name)
			}
			byName[c.Manifest.Name] = c
		}
	}
	out := make([]DiscoveredCapability, 0, len(byName))
	for _, n := range order {
		out = append(out, byName[n])
	}
	return out
}
