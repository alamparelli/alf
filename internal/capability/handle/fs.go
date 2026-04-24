package handle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/alamparelli/alf/internal/capability"
)

// FSScope describes the read/write paths declared in a manifest. Paths are
// absolute, resolved at forge time; a trailing "/" means directory (prefix
// match), no trailing "/" means exact file.
type FSScope struct {
	Reads  []string
	Writes []string
}

// FSHandle grants scoped filesystem access. Non-serializable (see MarshalJSON).
// Revocation: Instance.Close flips revoked; in-flight ops also abort via
// lifecycleCtx.
//
// Paths passed to Read/Write may be absolute or relative. Relative paths are
// resolved against baseDir — this is the canonical coordinate system for the
// capability: its manifest declares scope relative to its bundle directory,
// and the guest addresses files by the same relative paths. An absolute
// path is also accepted and checked verbatim against scope.
type FSHandle struct {
	_ [0]noSerialize

	owner        capability.ID
	baseDir      string
	scope        FSScope
	lifecycleCtx context.Context
	revoked      atomic.Bool
}

// noSerialize is a zero-width marker preventing accidental serialization
// (#398 handle hygiene). Combined with MarshalJSON below.
type noSerialize struct{}

// NewFSHandle constructs a filesystem handle scoped to the given manifest.
// Paths are resolved against baseDir so manifests can declare relative paths.
func NewFSHandle(owner capability.ID, baseDir string, scope FSScope) *FSHandle {
	resolve := func(ps []string) []string {
		out := make([]string, 0, len(ps))
		for _, p := range ps {
			isDir := strings.HasSuffix(p, "/") || strings.HasSuffix(p, string(os.PathSeparator))
			if !filepath.IsAbs(p) {
				p = filepath.Join(baseDir, p)
			}
			p = filepath.Clean(p)
			if isDir {
				p += string(os.PathSeparator)
			}
			out = append(out, p)
		}
		return out
	}
	return &FSHandle{
		owner:   owner,
		baseDir: baseDir,
		scope: FSScope{
			Reads:  resolve(scope.Reads),
			Writes: resolve(scope.Writes),
		},
	}
}

// resolvePath maps a guest-supplied path into absolute form. Relative paths
// are resolved against the handle's baseDir; absolute paths are kept as-is.
// In both cases the result is filepath.Clean'd so it can be compared against
// scope rules byte-for-byte.
func (h *FSHandle) resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(h.baseDir, p))
}

// Errors returned by handle methods.
var (
	ErrRevoked    = errors.New("handle: revoked")
	ErrOutOfScope = errors.New("handle: path out of scope")
)

// MarshalJSON always fails; handles are not serializable by construction.
func (h *FSHandle) MarshalJSON() ([]byte, error) {
	return nil, errors.New("handle: FSHandle is not serializable")
}

// Read returns file contents if path is in the read scope.
func (h *FSHandle) Read(ctx context.Context, path string) ([]byte, error) {
	if err := h.preflight(ctx); err != nil {
		return nil, err
	}
	abs := h.resolvePath(path)
	if !scopeAllows(h.scope.Reads, abs) {
		return nil, ErrOutOfScope
	}
	// Honour both the caller ctx and the instance lifecycle.
	done := make(chan struct{})
	var data []byte
	var err error
	go func() {
		data, err = os.ReadFile(abs)
		close(done)
	}()
	select {
	case <-done:
		return data, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.lifecycleCtx.Done():
		return nil, ErrRevoked
	}
}

// Write stores data at path if path is in the write scope.
func (h *FSHandle) Write(ctx context.Context, path string, data []byte) error {
	if err := h.preflight(ctx); err != nil {
		return err
	}
	abs := h.resolvePath(path)
	if !scopeAllows(h.scope.Writes, abs) {
		return ErrOutOfScope
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	done := make(chan struct{})
	var err error
	go func() {
		err = os.WriteFile(abs, data, 0o644)
		close(done)
	}()
	select {
	case <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-h.lifecycleCtx.Done():
		return ErrRevoked
	}
}

// Scope returns the effective scope. Used by the WASM host-function layer
// to know whether to expose fs_read / fs_write.
func (h *FSHandle) Scope() FSScope {
	return h.scope
}

func (h *FSHandle) preflight(ctx context.Context) error {
	if h.revoked.Load() {
		return ErrRevoked
	}
	if h.lifecycleCtx == nil {
		// Forged outside an Instance — treat as revoked to avoid ambient use.
		return ErrRevoked
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := h.lifecycleCtx.Err(); err != nil {
		return ErrRevoked
	}
	return nil
}

func scopeAllows(rules []string, abs string) bool {
	for _, r := range rules {
		if strings.HasSuffix(r, string(os.PathSeparator)) || strings.HasSuffix(r, "/") {
			prefix := strings.TrimRight(r, "/")
			if abs == prefix || strings.HasPrefix(abs, prefix+string(os.PathSeparator)) {
				return true
			}
			continue
		}
		if abs == r {
			return true
		}
	}
	return false
}
