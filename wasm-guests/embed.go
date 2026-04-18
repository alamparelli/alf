// Package wasmguests embeds reference WASM capabilities into the ALF
// binary so a baseline demonstration works on any deployment without
// file placement on the container.
//
// Artefacts must be built before `go build`:
//
//	bash wasm-guests/build.sh
//
// The build step is automated by scripts/dev-deploy.sh. If you run
// `go build ./...` standalone without having built the wasm artefacts,
// you will see an "embedded file not found" error — run build.sh first.
package wasmguests

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// BundledFS exposes the embedded manifests and wasm binaries. The daemon
// copies each bundled capability into a runtime scratch dir at startup
// (so the existing LoadManifest + InvokeTool code paths, which read from
// real filesystem paths, work unchanged).
//
//go:embed tool-demo/manifest.toml tool-demo/tool-demo.wasm
var BundledFS embed.FS

// ExtractTo copies every embedded capability into dest/<name>/, returning
// the list of manifest paths so the caller can register them. Idempotent:
// if dest already has the files they are overwritten with the current
// bundled version (supports upgrade scenarios).
func ExtractTo(dest string) ([]string, error) {
	var manifests []string
	err := fs.WalkDir(BundledFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == "." {
				return nil
			}
			return os.MkdirAll(filepath.Join(dest, path), 0o755)
		}
		data, err := BundledFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		outPath := filepath.Join(dest, path)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			return err
		}
		if filepath.Base(path) == "manifest.toml" {
			manifests = append(manifests, outPath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return manifests, nil
}
