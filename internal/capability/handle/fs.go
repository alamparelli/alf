package handle

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"

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

	// ErrSymlinkRefused signals SEC-006: the resolved path is a
	// symbolic link or contains one as the final component. FSHandle
	// refuses to follow symlinks out of an abundance of caution —
	// a co-tenant or operator who placed a symlink inside the
	// capability's scope could otherwise leak data outside it
	// (or, on writes, clobber arbitrary files).
	ErrSymlinkRefused = errors.New("handle: symlink in path refused")
)

// MarshalJSON always fails; handles are not serializable by construction.
func (h *FSHandle) MarshalJSON() ([]byte, error) {
	return nil, errors.New("handle: FSHandle is not serializable")
}

// Read returns file contents if path is in the read scope. SEC-006:
// the file is opened with O_NOFOLLOW so a symlink installed in scope
// cannot redirect the read to a path outside scope. ELOOP / "too
// many levels of symbolic links" surfaces as ErrSymlinkRefused.
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
		data, err = readFileNoFollow(abs)
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

// Write stores data at path if path is in the write scope. SEC-006:
// the file is opened with O_NOFOLLOW + mode 0o600 so (a) a symlink
// installed in scope cannot redirect the write outside scope and
// (b) the resulting file is not world-readable in shared-volume
// container deployments.
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
		err = writeFileNoFollow(abs, data, 0o600)
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

// readFileNoFollow mirrors os.ReadFile but opens with O_NOFOLLOW so
// the final path component being a symlink fails with ELOOP rather
// than transparently redirecting the read to the symlink target.
// Translates ELOOP to ErrSymlinkRefused so callers can distinguish
// from generic I/O errors. Lstat is also checked because some
// platforms surface symlink-traversal differently (Linux: ELOOP at
// open; macOS: behaves consistently with O_NOFOLLOW).
func readFileNoFollow(abs string) ([]byte, error) {
	// Pre-check: refuse if the leaf is a symlink. Defends against
	// platforms where O_NOFOLLOW semantics drift, and surfaces a
	// clear error to the caller.
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSymlinkRefused
	}
	f, err := os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if isSymlinkErr(err) {
			return nil, ErrSymlinkRefused
		}
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// writeFileNoFollow mirrors os.WriteFile but uses O_NOFOLLOW so the
// final path component cannot be a symlink to an arbitrary location.
// File mode is enforced to mode (caller passes 0o600).
func writeFileNoFollow(abs string, data []byte, mode os.FileMode) error {
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return ErrSymlinkRefused
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, mode)
	if err != nil {
		if isSymlinkErr(err) {
			return ErrSymlinkRefused
		}
		return err
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		return werr
	}
	return f.Close()
}

// isSymlinkErr returns true when err indicates the OS refused an
// open due to a symlink in the path (with O_NOFOLLOW). Linux raises
// syscall.ELOOP; macOS / BSD use the same constant. Wrapped errors
// from os.OpenFile carry the underlying syscall.Errno.
func isSymlinkErr(err error) bool {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if errno, ok := pathErr.Err.(syscall.Errno); ok && errno == syscall.ELOOP {
			return true
		}
	}
	return false
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
