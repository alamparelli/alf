package wasm

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// LoadAll is the §4.1 lockdown entry point for the WASM loader (#420).
// It scans the two on-disk capability roots the doctrine admits — and
// only those:
//
//   - <dataDir>/tools/<id>/   → wasm-tool bundles
//   - <dataDir>/apps/<slug>/ → wasm-app bundles
//
// Before scanning, it runs a one-time migration that moves any bundle
// still living at the legacy <dataDir>/skills.d/wasm/<id>/ location to
// the new path determined by its manifest kind. The migration is
// idempotent: re-runs are no-ops once the destination exists.
//
// LoadAll returns the union of loaded capability ids and per-bundle
// errors from both scans plus the migration log. Callers replace
// the previous direct LoadDir(skills.d/wasm) call.
func (l *Loader) LoadAll(ctx context.Context, dataDir string) ([]string, []error) {
	if l == nil {
		return nil, []error{errors.New("wasm: loader not initialised")}
	}
	if l.Logger == nil {
		l.Logger = func(format string, args ...any) {}
	}

	migrationErrs := l.migrateLegacyBundles(dataDir)

	var allLoaded []string
	var allErrs []error
	allErrs = append(allErrs, migrationErrs...)

	for _, sub := range []string{"tools", "apps"} {
		root := filepath.Join(dataDir, sub)
		loaded, errs := l.LoadDir(ctx, root)
		allLoaded = append(allLoaded, loaded...)
		allErrs = append(allErrs, errs...)
		l.Logger("[wasm-loader] %s: %d bundles loaded, %d errors", root, len(loaded), len(errs))
	}

	return allLoaded, allErrs
}

// migrateLegacyBundles walks <dataDir>/skills.d/wasm/<id>/ and moves
// each bundle to the correct new path based on its manifest kind.
// Idempotent: bundles whose destination already exists are skipped.
// Bundles whose manifest cannot be read or whose kind is unsupported
// are left in place; the loader scan will then refuse them (or skip
// them) with a clear log line.
func (l *Loader) migrateLegacyBundles(dataDir string) []error {
	legacyRoot := filepath.Join(dataDir, "skills.d", "wasm")
	entries, err := os.ReadDir(legacyRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // nothing to migrate, fresh install or already cleaned
		}
		return []error{err}
	}

	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		srcDir := filepath.Join(legacyRoot, e.Name())
		manBytes, err := os.ReadFile(filepath.Join(srcDir, "manifest.toml"))
		if err != nil {
			l.Logger("[wasm-loader] migrate skip %s: cannot read manifest.toml: %v", e.Name(), err)
			continue
		}
		man, err := envelope.Validate(manBytes)
		if err != nil {
			l.Logger("[wasm-loader] migrate skip %s: invalid manifest: %v", e.Name(), err)
			continue
		}
		var destSub string
		switch man.Kind {
		case envelope.KindWASMTool:
			destSub = "tools"
		case envelope.KindWASMApp:
			destSub = "apps"
		default:
			l.Logger("[wasm-loader] migrate skip %s: kind %q not migratable (§4.1 only wasm-tool/wasm-app from disk)", e.Name(), man.Kind)
			continue
		}
		destDir := filepath.Join(dataDir, destSub, man.ID)
		if _, err := os.Stat(destDir); err == nil {
			l.Logger("[wasm-loader] migrate skip %s: destination %s already exists", e.Name(), destDir)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Rename(srcDir, destDir); err != nil {
			l.Logger("[wasm-loader] migrate %s: rename failed: %v", e.Name(), err)
			errs = append(errs, err)
			continue
		}
		l.Logger("[wasm-loader] migrated %s → %s/%s (kind=%s)", srcDir, destSub, man.ID, man.Kind)
	}

	// Best-effort cleanup of the now-empty legacy root. Failure is
	// non-fatal — the directory may still hold bundles that failed to
	// migrate (unreadable manifest, unmigratable kind), or sibling
	// metadata the migrator chose not to touch.
	_ = os.Remove(legacyRoot)
	return errs
}
