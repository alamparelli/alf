package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/handle"
)

type recordingRegistry struct {
	mu   sync.Mutex
	caps []capability.ID
}

func (r *recordingRegistry) Register(c capability.Capability) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.caps = append(r.caps, c.Manifest().ID)
	return nil
}

// loaderManifest mirrors the fixture used in internal/runtime/wasm/loader_test.go.
const loaderManifest = `alf_envelope_version = 1
id      = "loader-test"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Loader Test"
`

// minimalWasm is a valid empty module: magic + version. Same fixture as
// the wasm package's loader test — instantiation succeeds with no
// imports and no _initialize.
func minimalWasmBytes() []byte {
	return []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
}

func writeWASMBundle(t *testing.T, skillsDir, id, manifest string, wasm []byte) {
	t.Helper()
	dir := filepath.Join(skillsDir, "wasm", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".wasm"), wasm, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSetupWASMLoader_RegistersBundle(t *testing.T) {
	handle.ResetMintForTesting()

	dataDir := t.TempDir()
	skillsDir := t.TempDir()
	writeWASMBundle(t, skillsDir, "loader-test", loaderManifest, minimalWasmBytes())

	reg := &recordingRegistry{}
	logs := captureLogs()

	wr, err := setupWASMLoader(context.Background(), dataDir, skillsDir, reg, logs.printf)
	if err != nil {
		t.Fatalf("setupWASMLoader: %v", err)
	}
	defer wr.Close(context.Background())

	if len(reg.caps) != 1 || reg.caps[0] != "loader-test" {
		t.Errorf("registered caps=%v, want [loader-test]", reg.caps)
	}
	// Daemon key persisted with strict perms (§7.3 Tier 2 — audited by daemonkey.go).
	keyPath := filepath.Join(dataDir, "keys", "daemon.json")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("daemon key missing: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("daemon key perms=%v, want 0o600 or stricter", info.Mode().Perm())
	}
	// Boot summary line must be emitted regardless of bundle outcome.
	if !logs.contains("scanned") || !logs.contains("1 bundles loaded") {
		t.Errorf("missing summary log; got:\n%s", logs.joined())
	}
}

func TestSetupWASMLoader_MissingRootIsNotAnError(t *testing.T) {
	handle.ResetMintForTesting()

	dataDir := t.TempDir()
	skillsDir := t.TempDir() // no skills.d/wasm/ subdir created

	reg := &recordingRegistry{}
	logs := captureLogs()

	wr, err := setupWASMLoader(context.Background(), dataDir, skillsDir, reg, logs.printf)
	if err != nil {
		t.Fatalf("setupWASMLoader: %v", err)
	}
	defer wr.Close(context.Background())

	if len(reg.caps) != 0 {
		t.Errorf("registered caps=%v, want empty", reg.caps)
	}
	if !logs.contains("0 bundles loaded, 0 errors") {
		t.Errorf("expected zero-bundle summary; got:\n%s", logs.joined())
	}
}

func TestSetupWASMLoader_BadBundleSkippedNotFatal(t *testing.T) {
	handle.ResetMintForTesting()

	dataDir := t.TempDir()
	skillsDir := t.TempDir()
	// Manifest names id "wrong-id" but no matching .wasm — loader will
	// fail to read the wasm payload and accumulate the error.
	manifest := `alf_envelope_version = 1
id      = "wrong-id"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Wrong"
`
	dir := filepath.Join(skillsDir, "wasm", "wrong-id")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	// no <id>.wasm written

	reg := &recordingRegistry{}
	logs := captureLogs()

	wr, err := setupWASMLoader(context.Background(), dataDir, skillsDir, reg, logs.printf)
	if err != nil {
		t.Fatalf("setupWASMLoader: %v", err)
	}
	defer wr.Close(context.Background())

	if len(reg.caps) != 0 {
		t.Errorf("registered caps=%v, want empty (bundle was bad)", reg.caps)
	}
	if !logs.contains("0 bundles loaded, 1 errors") {
		t.Errorf("expected 1-error summary; got:\n%s", logs.joined())
	}
}

func TestSetupWASMLoader_EmptyDataDirIsAnError(t *testing.T) {
	handle.ResetMintForTesting()

	reg := &recordingRegistry{}
	_, err := setupWASMLoader(context.Background(), "", t.TempDir(), reg, nil)
	if err == nil {
		t.Fatal("expected error for empty dataDir, got nil")
	}
	if !strings.Contains(err.Error(), "daemon key") {
		t.Errorf("error %q does not surface daemon-key context", err)
	}
}

// --- helpers ---

type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func captureLogs() *logCapture { return &logCapture{} }

func (l *logCapture) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logCapture) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func (l *logCapture) contains(needle string) bool {
	return strings.Contains(l.joined(), needle)
}

