package wasm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Runtime wraps a wazero runtime and adds a compile cache so warm
// invocations skip the expensive JIT step. This is the main production
// concern beyond the spike.
//
// A Runtime is safe for concurrent use. Callers should Close() it at
// program shutdown.
type Runtime struct {
	wazero   wazero.Runtime
	dataRoot string
	vault    VaultClient
	notifier Notifier

	cacheMu sync.Mutex
	cache   map[string]wazero.CompiledModule // key: sha256(wasm bytes)

	closed bool
}

// Options configures a new Runtime.
type Options struct {
	// DataRoot is the host directory under which per-capability storage lives.
	DataRoot string

	// Vault is the VaultClient implementation. If nil, a DefaultVaultClient
	// pointing at public URLs is used (useful for demos and tests).
	Vault VaultClient

	// Notifier receives guest log lines. If nil, output is silent.
	Notifier Notifier

	// HTTPClient is used by DefaultVaultClient when Vault is nil. If nil,
	// a 30s-timeout client is created.
	HTTPClient *http.Client
}

// New creates a Runtime with WASI preview 1 instantiated and an empty
// compile cache.
func New(ctx context.Context, opts Options) (*Runtime, error) {
	if opts.DataRoot == "" {
		return nil, fmt.Errorf("DataRoot is required")
	}
	if err := os.MkdirAll(opts.DataRoot, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir DataRoot: %w", err)
	}

	rt := wazero.NewRuntime(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("instantiate wasi: %w", err)
	}

	vault := opts.Vault
	if vault == nil {
		hc := opts.HTTPClient
		if hc == nil {
			hc = &http.Client{Timeout: 30 * time.Second}
		}
		vault = &DefaultVaultClient{HTTP: hc}
	}

	return &Runtime{
		wazero:   rt,
		dataRoot: opts.DataRoot,
		vault:    vault,
		notifier: opts.Notifier,
		cache:    make(map[string]wazero.CompiledModule),
	}, nil
}

// Close releases resources. Idempotent.
func (r *Runtime) Close(ctx context.Context) error {
	r.cacheMu.Lock()
	if r.closed {
		r.cacheMu.Unlock()
		return nil
	}
	r.closed = true
	for _, cm := range r.cache {
		_ = cm.Close(ctx)
	}
	r.cache = nil
	r.cacheMu.Unlock()
	return r.wazero.Close(ctx)
}

// InvokeTool runs a Tool manifest as a WASI command and returns its stdout,
// stderr and exit code. The guest sees only the host capabilities its
// manifest granted.
func (r *Runtime) InvokeTool(ctx context.Context, manifestPath string, stdin io.Reader, env map[string]string) (stdout, stderr []byte, exitCode uint32, err error) {
	m, err := LoadManifest(manifestPath)
	if err != nil {
		return nil, nil, 0, err
	}
	if m.Kind != KindTool {
		return nil, nil, 0, fmt.Errorf("manifest kind is %q, expected tool", m.Kind)
	}
	return r.run(ctx, m, manifestPath, stdin, env)
}

// InvokeApp runs an App manifest once per HTTP request, CGI-style. The
// request method/path/body-length are passed as env vars (ALF_METHOD,
// ALF_PATH, ALF_BODY_LENGTH); the request body is piped into stdin;
// stdout becomes the response body; exit code 0 ⇒ 200 OK, non-zero ⇒ 500.
func (r *Runtime) InvokeApp(ctx context.Context, manifestPath string, method, path string, body []byte) (status int, respBody []byte, err error) {
	m, err := LoadManifest(manifestPath)
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

// run is the shared invocation path for tools and apps. It reads the .wasm
// file, compiles it (cached), builds a Policy-gated host module, and
// instantiates the guest as a WASI command.
func (r *Runtime) run(ctx context.Context, m *Manifest, manifestPath string, stdin io.Reader, env map[string]string) ([]byte, []byte, uint32, error) {
	wasmPath := m.Entry
	if !filepath.IsAbs(wasmPath) {
		wasmPath = filepath.Join(filepath.Dir(manifestPath), wasmPath)
	}
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("read wasm: %w", err)
	}

	compiled, err := r.compile(ctx, wasmBytes)
	if err != nil {
		return nil, nil, 0, err
	}

	policy := PolicyFromManifest(m)

	var storage *Storage
	if policy.StorageEnabled {
		s, err := NewStorage(r.dataRoot, m.Name)
		if err != nil {
			return nil, nil, 0, err
		}
		storage = s
	}

	// Build the per-invocation host module. It carries the policy snapshot
	// so each invocation reflects the current manifest on disk.
	if err := BuildHostModule(ctx, r.wazero, m.Name, policy, storage, r.vault, r.notifier); err != nil {
		return nil, nil, 0, err
	}
	defer clearStash(m.Name)
	defer func() {
		if hm := r.wazero.Module("alf"); hm != nil {
			_ = hm.Close(ctx)
		}
	}()

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
		return stdoutBuf.Bytes(), stderrBuf.Bytes(), 1,
			fmt.Errorf("instantiate guest %q: %w (likely: guest called a host capability not declared in manifest.permissions)", m.Name, err)
	}
	defer mod.Close(ctx)

	return stdoutBuf.Bytes(), stderrBuf.Bytes(), 0, nil
}

// compile returns a cached CompiledModule or compiles + caches wasmBytes.
// The cache key is the sha256 of the bytes so updates invalidate naturally.
func (r *Runtime) compile(ctx context.Context, wasmBytes []byte) (wazero.CompiledModule, error) {
	sum := sha256.Sum256(wasmBytes)
	key := hex.EncodeToString(sum[:])

	r.cacheMu.Lock()
	if cm, ok := r.cache[key]; ok {
		r.cacheMu.Unlock()
		return cm, nil
	}
	r.cacheMu.Unlock()

	cm, err := r.wazero.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("compile wasm: %w", err)
	}

	r.cacheMu.Lock()
	if existing, ok := r.cache[key]; ok {
		// Lost the race — keep the existing entry and drop ours.
		r.cacheMu.Unlock()
		_ = cm.Close(ctx)
		return existing, nil
	}
	r.cache[key] = cm
	r.cacheMu.Unlock()
	return cm, nil
}
