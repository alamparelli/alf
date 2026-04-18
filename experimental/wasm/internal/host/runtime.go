package host

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Runtime holds a wazero runtime and the per-spike data root.
// It is safe to instantiate many guest modules from the same Runtime.
type Runtime struct {
	wazero    wazero.Runtime
	dataRoot  string
	httpC     *http.Client
	closeOnce bool
}

// New creates a wazero Runtime with WASI preview 1 pre-instantiated.
// dataRoot is the host directory under which per-capability storage lives.
func New(ctx context.Context, dataRoot string) (*Runtime, error) {
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir dataRoot: %w", err)
	}
	rt := wazero.NewRuntime(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("instantiate wasi: %w", err)
	}
	return &Runtime{
		wazero:   rt,
		dataRoot: dataRoot,
		httpC:    &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// Close releases all runtime resources. Idempotent.
func (r *Runtime) Close(ctx context.Context) error {
	if r.closeOnce {
		return nil
	}
	r.closeOnce = true
	return r.wazero.Close(ctx)
}

// InvokeTool loads a Tool manifest + wasm file, runs it to completion as a
// WASI command, and returns stdout, stderr, exit code. The guest sees only
// the host capabilities granted by its manifest.
func (r *Runtime) InvokeTool(ctx context.Context, manifestPath string, stdin io.Reader, env map[string]string) (stdout, stderr []byte, exitCode uint32, err error) {
	m, err := Load(manifestPath)
	if err != nil {
		return nil, nil, 0, err
	}
	if m.Kind != KindTool {
		return nil, nil, 0, fmt.Errorf("manifest kind is %q, expected tool", m.Kind)
	}
	return r.run(ctx, m, manifestPath, stdin, env)
}

// InvokeApp loads an App manifest + wasm file and runs it once per HTTP
// request (CGI-style). Request method, path and body are passed as env
// vars; stdout is the response body, stderr is logged for debugging.
//
// This is intentionally simple for the spike. A production runtime would use
// reactor-module exports with wasi-http for sub-millisecond warm paths.
func (r *Runtime) InvokeApp(ctx context.Context, manifestPath string, method, path string, body []byte) (status int, respBody []byte, err error) {
	m, err := Load(manifestPath)
	if err != nil {
		return 0, nil, err
	}
	if m.Kind != KindApp {
		return 0, nil, fmt.Errorf("manifest kind is %q, expected app", m.Kind)
	}
	env := map[string]string{
		"ALF_METHOD":      method,
		"ALF_PATH":        path,
		"ALF_BODY_LENGTH": fmt.Sprintf("%d", len(body)),
	}
	out, errOut, code, err := r.run(ctx, m, manifestPath, bytes.NewReader(body), env)
	if err != nil {
		return 0, nil, err
	}
	if code != 0 {
		return 500, append([]byte("app error: "), errOut...), nil
	}
	return 200, out, nil
}

// run is the shared invocation path for tools and apps.
func (r *Runtime) run(ctx context.Context, m *Manifest, manifestPath string, stdin io.Reader, env map[string]string) ([]byte, []byte, uint32, error) {
	wasmPath := m.Entry
	if !filepath.IsAbs(wasmPath) {
		wasmPath = filepath.Join(filepath.Dir(manifestPath), wasmPath)
	}
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("read wasm: %w", err)
	}

	policy := PolicyFromManifest(m)

	// Per-capability storage directory.
	var storage *Storage
	if policy.StorageEnabled {
		s, err := NewStorage(r.dataRoot, m.Name)
		if err != nil {
			return nil, nil, 0, err
		}
		storage = s
	}

	// Host "alf" module gated by Policy — every subsequent instantiation of
	// this capability will link against this module instance.
	// We build it fresh per-call so the policy is always in sync with the
	// manifest (which could have changed on disk between calls).
	if err := BuildHostModule(ctx, r.wazero, m.Name, policy, storage, r.httpC); err != nil {
		return nil, nil, 0, err
	}
	defer ClearStash(m.Name)
	defer func() {
		// Unload the host module so a subsequent call can rebuild cleanly
		// (otherwise wazero complains about duplicate "alf" module names).
		hm := r.wazero.Module("alf")
		if hm != nil {
			_ = hm.Close(ctx)
		}
	}()

	// Compile the guest wasm. In a production runtime we would cache this.
	compiled, err := r.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("compile wasm: %w", err)
	}
	defer compiled.Close(ctx)

	var stdoutBuf, stderrBuf bytes.Buffer
	cfg := wazero.NewModuleConfig().
		WithName(m.Name).
		WithStdout(&stdoutBuf).
		WithStderr(&stderrBuf).
		WithStdin(stdin).
		WithStartFunctions("_start")
	for k, v := range env {
		cfg = cfg.WithEnv(k, v)
	}

	mod, err := r.wazero.InstantiateModule(ctx, compiled, cfg)
	if err != nil {
		// Link failure from a missing host import = the guest violated its
		// manifest. Surface a clear message, not a wazero internal stack.
		return stdoutBuf.Bytes(), stderrBuf.Bytes(), 1,
			fmt.Errorf("instantiate guest %q: %w (likely: guest called a host capability not declared in manifest.permissions)", m.Name, err)
	}
	defer mod.Close(ctx)

	// WASI command modules exit via proc_exit, caught as sys.ExitError.
	// If we reach here without error, the module finished cleanly.
	return stdoutBuf.Bytes(), stderrBuf.Bytes(), 0, nil
}
