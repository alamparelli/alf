package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/runtime"
)

// setupRevocationCascade ties the runtime cascade engine
// (Instantiator.RevokeByKey, shipped in #392 Stage 5) to the two
// discovery channels described in §8 of ARCHITECTURE-SECURITY.md:
//
//  1. Operator path — `alf trust revoke <fp>` writes a `<keyid>.revoked`
//     sidecar; SIGHUP triggers wasmRt.TrustStore.Load() then
//     cascader.Refresh().
//  2. CRL path — crl.Refresher fetches a signed CRL, applies it via
//     MemoryTrustStore.ApplyCRL, then fires its OnApply callback —
//     wired straight to cascader.Refresh().
//
// The returned function is what setupCRL needs as its onApply
// argument. The SIGHUP handler runs as a goroutine on subCtx so
// daemon shutdown stops it cleanly via context cancellation.
//
// Returns nil cascader when wasmRt is nil (degraded boot path —
// the rest of the daemon limps without WASM, no cascade to wire).
//
// Closes #396 deliverable 2 (provider revocation cascade) and
// completes deliverable 8 (`alf trust revoke` end-to-end without
// requiring `alf restart`).
func setupRevocationCascade(ctx context.Context, wasmRt *wasmRuntime, logf func(string, ...any)) (*runtime.RevocationCascader, func()) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if wasmRt == nil || wasmRt.Inst == nil || wasmRt.TrustStore == nil {
		return nil, nil
	}

	cascader := runtime.NewRevocationCascader(
		wasmRt.Inst,
		wasmRt.TrustStore.AllRevoked,
		logf,
	)
	logf("[cascade] revocation cascader wired: trust dir=%s", wasmRt.TrustStore.Dir())

	// SIGHUP handler — operator path. Buffered channel so a fast
	// double-HUP doesn't get coalesced; signal.Notify drops on full
	// channels but a buffer of 4 covers normal "click click click"
	// from a bewildered operator while still propagating the
	// transition to the cascader on each visit.
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGHUP)

	go func() {
		for {
			select {
			case <-ctx.Done():
				signal.Stop(sigCh)
				return
			case <-sigCh:
				if err := wasmRt.TrustStore.Load(); err != nil {
					logf("[cascade] SIGHUP reload failed: %v", err)
					continue
				}
				closed := cascader.Refresh()
				logf("[cascade] SIGHUP reload: trust dir=%s revoked=%d cascaded=%d",
					wasmRt.TrustStore.Dir(), countRevoked(wasmRt.TrustStore), len(closed))
			}
		}
	}()

	// Wrap Refresh into the void-returning shape crl.Refresher.OnApply
	// expects. The closed capability IDs are still surfaced to the
	// operator via the cascader's per-transition logf line.
	onApply := func() { _ = cascader.Refresh() }
	return cascader, onApply
}

// countRevoked is a tiny helper so the SIGHUP audit line carries
// the post-reload revoked count without exposing AllRevoked at
// the daemon-package boundary. Kept here rather than added to
// envelope/ to avoid a one-call-site method.
func countRevoked(store *envelope.DirTrustStore) int {
	if store == nil {
		return 0
	}
	return len(store.AllRevoked())
}
