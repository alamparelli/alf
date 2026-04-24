package builder

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBuild_ErrEmptySource(t *testing.T) {
	_, err := Build(context.Background(), Source{}, BuildConfig{})
	if !errors.Is(err, ErrEmptySource) {
		t.Errorf("err=%v, want ErrEmptySource", err)
	}
}

func TestBuild_ErrNoGoMod(t *testing.T) {
	src := Source{Files: map[string][]byte{
		"main.go": []byte(`package main
func main() {}
`),
	}}
	_, err := Build(context.Background(), src, BuildConfig{})
	if !errors.Is(err, ErrNoGoMod) {
		t.Errorf("err=%v, want ErrNoGoMod", err)
	}
}

func TestBuild_ErrNoGoToolchain(t *testing.T) {
	src := Source{Files: map[string][]byte{
		"go.mod": []byte("module x\ngo 1.24\n"),
	}}
	_, err := Build(context.Background(), src, BuildConfig{
		GoCmd: "/definitely/not/a/real/binary/nope-go",
	})
	if !errors.Is(err, ErrNoGoToolchain) {
		t.Errorf("err=%v, want ErrNoGoToolchain", err)
	}
}

func TestMaterialise_RejectsAbsolutePath(t *testing.T) {
	src := Source{Files: map[string][]byte{
		"go.mod":       []byte("module x\ngo 1.24\n"),
		"/etc/passwd":  []byte("evil"),
	}}
	// Materialise is called inside Build — expect the absolute-path
	// error to surface via Build's wrapping.
	_, err := Build(context.Background(), src, BuildConfig{})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("err=%v, want absolute-path error", err)
	}
}

func TestMaterialise_RejectsParentEscape(t *testing.T) {
	src := Source{Files: map[string][]byte{
		"go.mod":       []byte("module x\ngo 1.24\n"),
		"../escape.go": []byte("package x\n"),
	}}
	_, err := Build(context.Background(), src, BuildConfig{})
	if err == nil || !strings.Contains(err.Error(), "escape") {
		t.Errorf("err=%v, want escape error", err)
	}
}

// TestBuild_CompilesMinimalReactor exercises the real toolchain
// end-to-end. Skipped when GOOS=wasip1 / GOARCH=wasm is unavailable
// (unlikely on dev machines but useful for constrained CI).
func TestBuild_CompilesMinimalReactor(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	// Minimal reactor-mode guest: empty main, no imports. Reactor
	// mode wraps _initialize automatically when -buildmode=c-shared
	// is set. Go 1.24+ is required — this test documents that
	// requirement by failing with a clear compile error otherwise.
	src := Source{Files: map[string][]byte{
		"go.mod": []byte("module alf/testbuild\ngo 1.24\n"),
		"main.go": []byte(`package main

func main() {}
`),
	}}

	// Builds can take seconds; bound it so a stuck subprocess fails
	// the test deterministically.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	wasm, err := Build(ctx, src, BuildConfig{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(wasm) < 8 {
		t.Fatalf("wasm output suspiciously small: %d bytes", len(wasm))
	}
	// Magic + version bytes per WASM spec.
	if wasm[0] != 0x00 || wasm[1] != 0x61 || wasm[2] != 0x73 || wasm[3] != 0x6d {
		t.Fatalf("wasm output does not start with magic \\0asm: %v", wasm[:8])
	}
}

func TestBuild_SurfacesStderrOnCompileFailure(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	// Intentionally broken source: missing semicolon equivalent —
	// "func main( { }" is a syntax error.
	src := Source{Files: map[string][]byte{
		"go.mod":  []byte("module alf/testbuild\ngo 1.24\n"),
		"main.go": []byte("package main\nfunc main( { }\n"),
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := Build(ctx, src, BuildConfig{})
	if !errors.Is(err, ErrBuildFailed) {
		t.Fatalf("err=%v, want ErrBuildFailed", err)
	}
	if !strings.Contains(err.Error(), "stderr:") {
		t.Errorf("err=%v, want stderr in message", err)
	}
}

