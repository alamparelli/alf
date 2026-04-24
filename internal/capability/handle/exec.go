package handle

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"sync/atomic"

	"github.com/alamparelli/alf/internal/capability"
)

// ExecScope declares the set of binaries a capability may spawn. Entries
// are absolute, filepath.Clean'd paths — byte-for-byte compared at exec
// time. No globbing, no PATH resolution, no symlink following: the
// manifest names the exact executable, the capability calls the exact
// executable, anything else fails closed.
type ExecScope struct {
	Binaries []string
}

// Allows reports whether the given binary path is in scope. The input
// path is Cleaned before comparison to defeat "./a/../b" style tricks.
func (s ExecScope) Allows(binary string) bool {
	if binary == "" {
		return false
	}
	clean := filepath.Clean(binary)
	if !filepath.IsAbs(clean) {
		return false
	}
	for _, allowed := range s.Binaries {
		if filepath.Clean(allowed) == clean {
			return true
		}
	}
	return false
}

// ExecResult carries the aggregated output of a successful exec. Stdout
// and Stderr are capped internally by the handle — capabilities do not
// stream arbitrary subprocess output into host memory (prevents a fork-
// bomb style DOS via log flooding). Cap is 1 MiB each for now; tune later.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

const execOutputCap = 1 << 20 // 1 MiB per stream

// ExecHandle grants scoped process spawn. Non-serializable. Revocation:
// Instance.Close flips revoked; in-flight processes get their context
// cancelled via lifecycleCtx, which os/exec converts into SIGKILL.
type ExecHandle struct {
	_ [0]noSerialize

	owner        capability.ID
	scope        ExecScope
	lifecycleCtx context.Context
	revoked      atomic.Bool
}

// NewExecHandle constructs an exec handle scoped to the given binaries.
// lifecycleCtx is bound by Instance wiring.
func NewExecHandle(owner capability.ID, scope ExecScope) *ExecHandle {
	return &ExecHandle{owner: owner, scope: scope}
}

// Run spawns the given binary with args and stdin, waiting for completion.
// The binary must be in scope; the handle must not be revoked. The caller
// ctx is merged with lifecycleCtx so Instance.Close() kills the process.
func (h *ExecHandle) Run(ctx context.Context, binary string, args []string, stdin []byte) (ExecResult, error) {
	if h.revoked.Load() {
		return ExecResult{}, ErrRevoked
	}
	if !h.scope.Allows(binary) {
		return ExecResult{}, ErrOutOfScope
	}

	opCtx, cancel := mergeContexts(ctx, h.lifecycleCtx)
	defer cancel()

	cmd := exec.CommandContext(opCtx, binary, args...)
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &cappedWriter{W: &outBuf, Cap: execOutputCap}
	cmd.Stderr = &cappedWriter{W: &errBuf, Cap: execOutputCap}

	err := cmd.Run()
	result := ExecResult{
		Stdout:   outBuf.Bytes(),
		Stderr:   errBuf.Bytes(),
		ExitCode: cmd.ProcessState.ExitCode(),
	}

	// A non-zero exit is reported via ExitCode — not as a Run error — so
	// capabilities can distinguish "ran to completion, exited non-zero"
	// from "could not start / was cancelled / scope violation".
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return result, nil
		}
		return result, err
	}
	return result, nil
}

// Owner returns the capability ID this handle was forged for.
func (h *ExecHandle) Owner() capability.ID { return h.owner }

// MarshalJSON implements §4.2 invariant 1.
func (h *ExecHandle) MarshalJSON() ([]byte, error) {
	return nil, ErrHandleNonSerializable
}

// attachLifecycle is the package-private hook used by Instance to bind an
// ExecHandle to its lifecycle context.
func (h *ExecHandle) attachLifecycle(ctx context.Context) { h.lifecycleCtx = ctx }

// cappedWriter stops accepting bytes once Cap is reached. Extra bytes are
// silently discarded — the subprocess is not informed. This bounds memory
// without requiring the caller to inspect the stream.
type cappedWriter struct {
	W      io.Writer
	Cap    int
	Writen int
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.Writen >= c.Cap {
		return len(p), nil // pretend-accept so the subprocess keeps running
	}
	remaining := c.Cap - c.Writen
	if len(p) <= remaining {
		n, err := c.W.Write(p)
		c.Writen += n
		return n, err
	}
	n, err := c.W.Write(p[:remaining])
	c.Writen += n
	if err != nil {
		return n, err
	}
	return len(p), nil
}
