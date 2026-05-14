package tooling

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const helloReadManifest = `alf_envelope_version = 1
id      = "hello-read"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Hello Read"
description = "Reference WASM tool bundle — step 8 build smoke test."

[[fs.reads]]
path = "data/"
`

// A minimal reactor-mode guest source that compiles cleanly without
// any ABI exports. The boot loader (step 9) accepts guests that
// lack alf_invoke / alf_alloc — they're treated as degraded until a
// later upgrade adds them. For the build tool's purposes, this is
// enough to exercise the full build + install path.
const helloReadMainGo = `package main

func main() {}
`

const helloReadGoMod = `module alf/hello-read

go 1.24
`

func TestWASMBuildTool_ToolNameAndSchema(t *testing.T) {
	var tool WASMBuildNativeTool
	if tool.ToolName() != "wasm_build_tool" {
		t.Errorf("ToolName=%q, want wasm_build_tool", tool.ToolName())
	}
	s := tool.Schema()
	if s.Name != "wasm_build_tool" {
		t.Errorf("Schema.Name=%q", s.Name)
	}
	if s.Description == "" {
		t.Error("Schema.Description empty")
	}
}

func TestWASMBuildTool_MissingDataDirRejected(t *testing.T) {
	var tool WASMBuildNativeTool // DataDir empty
	args, _ := json.Marshal(wasmBuildArgs{
		ManifestTOML: helloReadManifest,
		Sources: map[string]string{
			"go.mod":  helloReadGoMod,
			"main.go": helloReadMainGo,
		},
	})
	_, err := tool.Run(context.Background(), string(args))
	if err == nil || !strings.Contains(err.Error(), "DataDir") {
		t.Errorf("err=%v, want DataDir-missing error", err)
	}
}

func TestWASMBuildTool_MissingManifestRejected(t *testing.T) {
	tool := WASMBuildNativeTool{DataDir: t.TempDir()}
	args, _ := json.Marshal(wasmBuildArgs{
		Sources: map[string]string{"go.mod": "x"},
	})
	_, err := tool.Run(context.Background(), string(args))
	if err == nil || !strings.Contains(err.Error(), "manifest_toml") {
		t.Errorf("err=%v, want manifest_toml-required error", err)
	}
}

func TestWASMBuildTool_MissingSourcesRejected(t *testing.T) {
	tool := WASMBuildNativeTool{DataDir: t.TempDir()}
	args, _ := json.Marshal(wasmBuildArgs{
		ManifestTOML: helloReadManifest,
	})
	_, err := tool.Run(context.Background(), string(args))
	if err == nil || !strings.Contains(err.Error(), "sources") {
		t.Errorf("err=%v, want sources-empty error", err)
	}
}

func TestWASMBuildTool_InvalidKindRejected(t *testing.T) {
	tool := WASMBuildNativeTool{DataDir: t.TempDir()}
	manifest := strings.Replace(helloReadManifest, `kind    = "wasm-tool"`, `kind    = "skill"`, 1)
	args, _ := json.Marshal(wasmBuildArgs{
		ManifestTOML: manifest,
		Sources: map[string]string{
			"go.mod":  helloReadGoMod,
			"main.go": helloReadMainGo,
		},
	})
	_, err := tool.Run(context.Background(), string(args))
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("err=%v, want kind-rejected error", err)
	}
}

// TestWASMBuildTool_WASMAppInstallsInApps pins the §4.1 lockdown split:
// kind=wasm-app must land under <DataDir>/apps/<id>/, not the legacy
// skills.d/wasm/<id>/ path. The loader's LoadAll scans both data/tools
// and data/apps, so the destination is determined purely by manifest
// kind. A regression here would silently route apps into the tool
// surface (and worse, miss the supervisor's per-app layout
// expectations downstream).
func TestWASMBuildTool_WASMAppInstallsInApps(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	dataDir := t.TempDir()
	tool := WASMBuildNativeTool{DataDir: dataDir}

	manifest := strings.Replace(helloReadManifest, `id      = "hello-read"`, `id      = "hello-app"`, 1)
	manifest = strings.Replace(manifest, `kind    = "wasm-tool"`, `kind    = "wasm-app"`, 1)

	args, _ := json.Marshal(wasmBuildArgs{
		ManifestTOML: manifest,
		Sources: map[string]string{
			"go.mod":  helloReadGoMod,
			"main.go": helloReadMainGo,
		},
	})
	out, err := tool.Run(context.Background(), string(args))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var res wasmBuildResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	wantDir := filepath.Join(dataDir, "apps", "hello-app")
	if res.BundleDir != wantDir {
		t.Errorf("BundleDir=%q, want %q", res.BundleDir, wantDir)
	}
	if _, err := os.Stat(filepath.Join(wantDir, "manifest.toml")); err != nil {
		t.Errorf("manifest.toml missing at apps/ install path: %v", err)
	}

	// And confirm the legacy path was NOT used — a regression test
	// for any future code that re-adds the skills.d/wasm fallback.
	legacy := filepath.Join(dataDir, "skills.d", "wasm", "hello-app")
	if _, err := os.Stat(legacy); err == nil {
		t.Errorf("legacy path %s should NOT exist", legacy)
	}
}

func TestWASMBuildTool_BadManifestRejected(t *testing.T) {
	tool := WASMBuildNativeTool{DataDir: t.TempDir()}
	// Missing alf_envelope_version — schema rejects.
	manifest := `id = "x"
kind = "wasm-tool"
version = "0.1.0"
name = "X"
`
	args, _ := json.Marshal(wasmBuildArgs{
		ManifestTOML: manifest,
		Sources: map[string]string{
			"go.mod":  helloReadGoMod,
			"main.go": helloReadMainGo,
		},
	})
	_, err := tool.Run(context.Background(), string(args))
	if err == nil || !strings.Contains(err.Error(), "manifest validation") {
		t.Errorf("err=%v, want manifest-validation error", err)
	}
}

// TestWASMBuildTool_FullBuildAndInstall exercises the full path
// end-to-end: validate → build → install. Skipped when Go toolchain
// is not on PATH.
func TestWASMBuildTool_FullBuildAndInstall(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	dataDir := t.TempDir()
	tool := WASMBuildNativeTool{DataDir: dataDir}

	args, _ := json.Marshal(wasmBuildArgs{
		ManifestTOML: helloReadManifest,
		Sources: map[string]string{
			"go.mod":  helloReadGoMod,
			"main.go": helloReadMainGo,
		},
	})

	out, err := tool.Run(context.Background(), string(args))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var res wasmBuildResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal result: %v\nraw: %s", err, out)
	}

	if res.ID != "hello-read" {
		t.Errorf("ID=%q", res.ID)
	}
	if res.Kind != "wasm-tool" {
		t.Errorf("Kind=%q", res.Kind)
	}
	if !res.Unsigned {
		t.Error("Unsigned=false, want true")
	}
	if len(res.WasmSHA256) != 64 {
		t.Errorf("WasmSHA256 length=%d, want 64 hex", len(res.WasmSHA256))
	}

	// Bundle dir layout — per §4.1 the install path is
	// data/tools/<id>/ for wasm-tool (hello-read manifest declares
	// kind=wasm-tool). The legacy skills.d/wasm/<id>/ path is no
	// longer the install target — the loader's migrateLegacyBundles
	// only moves pre-#420 installs, it never creates new ones.
	bundleDir := filepath.Join(dataDir, "tools", "hello-read")
	if res.BundleDir != bundleDir {
		t.Errorf("BundleDir=%q, want %q", res.BundleDir, bundleDir)
	}

	manifestPath := filepath.Join(bundleDir, "manifest.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest.toml missing: %v", err)
	}
	wasmPath := filepath.Join(bundleDir, "hello-read.wasm")
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read hello-read.wasm: %v", err)
	}
	if len(wasmBytes) < 8 || wasmBytes[0] != 0x00 || wasmBytes[1] != 0x61 ||
		wasmBytes[2] != 0x73 || wasmBytes[3] != 0x6d {
		t.Fatalf("wasm output does not start with magic \\0asm")
	}
	if res.WasmBytes != len(wasmBytes) {
		t.Errorf("WasmBytes=%d, file has %d", res.WasmBytes, len(wasmBytes))
	}

	// Permissions on the installed files are restrictive (0o600).
	info, err := os.Stat(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("wasm file world/group perms=%v, want 0o600", info.Mode().Perm())
	}
}
