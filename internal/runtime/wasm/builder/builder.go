// Package builder compiles a Go source tree into a WASM reactor
// module using the standard Go toolchain. It is the in-daemon path
// referenced by ARCHITECTURE-SECURITY.md §4.1: the only supported
// way to produce a WASM capability bundle is through ALF, so the
// daemon can observe, sign, and log each build. The natively
// authored path lives in internal/tooling/native_wasm_build.go
// (step 8) — builder.Build is the plumbing it calls.
//
// See docs/WASM.md §4 for the ABI contract the source tree must
// honour (reactor mode, //go:wasmexport, //go:wasmimport).
package builder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Source is the input to Build. Files is keyed by relative path
// within the module root. Must include a go.mod declaring a valid
// module and at least one *.go file. The file set is materialised
// verbatim — no templating, no auto-injection — so the caller is
// responsible for providing the host ABI pragmas and a package main
// with an empty main (reactor mode runs _initialize, main is never
// called).
type Source struct {
	Files map[string][]byte
}

// BuildConfig controls the compilation. The zero value is a valid
// default: builds wasip1 / wasm / c-shared / CGO_ENABLED=0 using
// whatever `go` is on PATH. Callers override GoCmd for homelab
// builds against a pinned toolchain.
type BuildConfig struct {
	// GoCmd is the go binary to invoke. Defaults to "go" (from PATH).
	GoCmd string

	// ExtraEnv are KEY=VALUE pairs appended to the child env after
	// the mandatory GOOS/GOARCH/CGO_ENABLED settings. Used to pin
	// GOFLAGS or GOCACHE in constrained environments.
	ExtraEnv []string
}

// Errors surfaced by Build. Typed so callers (wasm_build_tool) can
// distinguish "your go.mod is bad" from "the go binary is missing".
var (
	ErrNoGoToolchain = errors.New("builder: go toolchain not found on PATH")
	ErrEmptySource   = errors.New("builder: source tree has no files")
	ErrNoGoMod       = errors.New("builder: source tree lacks go.mod")
	ErrBuildFailed   = errors.New("builder: go build failed")
)

// Build materialises Source into an isolated tempdir, runs the Go
// toolchain in reactor mode, and returns the resulting .wasm bytes.
// The tempdir is removed on return regardless of outcome.
//
// Environment set on the child process:
//
//	GOOS=wasip1
//	GOARCH=wasm
//	CGO_ENABLED=0
//
// Build flags:
//
//	-buildmode=c-shared   (reactor mode — exports _initialize, not _start)
//	-o out.wasm           (fixed output name, read back at the end)
//	-trimpath             (no host filesystem paths leak into the .wasm)
//
// Context cancellation kills the build process via exec.CommandContext.
func Build(ctx context.Context, src Source, cfg BuildConfig) ([]byte, error) {
	if len(src.Files) == 0 {
		return nil, ErrEmptySource
	}
	if _, ok := src.Files["go.mod"]; !ok {
		return nil, ErrNoGoMod
	}

	goCmd := cfg.GoCmd
	if goCmd == "" {
		goCmd = "go"
	}
	if _, err := exec.LookPath(goCmd); err != nil {
		return nil, fmt.Errorf("%w: %q (looked up on PATH)", ErrNoGoToolchain, goCmd)
	}

	workDir, err := os.MkdirTemp("", "alf-wasm-build-*")
	if err != nil {
		return nil, fmt.Errorf("builder: mkdir tempdir: %w", err)
	}
	defer os.RemoveAll(workDir)

	if err := materialise(workDir, src.Files); err != nil {
		return nil, err
	}

	outPath := filepath.Join(workDir, "out.wasm")

	cmd := exec.CommandContext(ctx, goCmd,
		"build",
		"-buildmode=c-shared",
		"-trimpath",
		"-o", outPath,
		".",
	)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"GOOS=wasip1",
		"GOARCH=wasm",
		"CGO_ENABLED=0",
	)
	cmd.Env = append(cmd.Env, cfg.ExtraEnv...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %v\nstderr:\n%s", ErrBuildFailed, err, stderr.String())
	}

	wasmBytes, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("builder: read output wasm: %w", err)
	}
	if len(wasmBytes) == 0 {
		return nil, fmt.Errorf("%w: output wasm is empty", ErrBuildFailed)
	}
	return wasmBytes, nil
}

// materialise writes every file in files into root, creating
// intermediate directories as needed. Paths must be relative;
// absolute paths are rejected to prevent a malicious source tree
// from writing outside the workdir.
func materialise(root string, files map[string][]byte) error {
	for rel, content := range files {
		if filepath.IsAbs(rel) {
			return fmt.Errorf("builder: source path %q is absolute", rel)
		}
		clean := filepath.Clean(rel)
		if strings.HasPrefix(clean, "..") || strings.Contains(clean, string(os.PathSeparator)+"..") {
			return fmt.Errorf("builder: source path %q escapes the source tree", rel)
		}
		dst := filepath.Join(root, clean)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return fmt.Errorf("builder: mkdir %s: %w", filepath.Dir(dst), err)
		}
		if err := os.WriteFile(dst, content, 0o600); err != nil {
			return fmt.Errorf("builder: write %s: %w", dst, err)
		}
	}
	return nil
}
