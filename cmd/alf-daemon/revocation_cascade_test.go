package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
	"github.com/alamparelli/alf/internal/tooling"
)

// TestSetupRevocationCascade_NilWasmRtReturnsNilCascade pins the
// degraded-boot path: when the WASM subsystem failed to come up,
// the cascader cannot be wired (no live registry to cascade into),
// and the daemon must continue without it.
func TestSetupRevocationCascade_NilWasmRtReturnsNilCascade(t *testing.T) {
	cascader, onApply := setupRevocationCascade(context.Background(), nil, nil)
	if cascader != nil {
		t.Errorf("nil wasmRt: cascader=%v, want nil", cascader)
	}
	if onApply != nil {
		t.Error("nil wasmRt: onApply non-nil, want nil")
	}
}

// TestSetupRevocationCascade_HappyPathWiresCascader pins the boot
// happy path: a valid wasmRt yields a cascader bound to the trust
// store and a non-nil onApply callback. The callback is what the
// CRL Refresher will invoke after each successful ApplyCRL.
func TestSetupRevocationCascade_HappyPathWiresCascader(t *testing.T) {
	handle.ResetMintForTesting()

	dataDir := t.TempDir()
	skillsDir := t.TempDir()
	reg := &recordingRegistry{}
	logs := captureLogs()

	wr, err := setupWASMLoader(context.Background(), dataDir, skillsDir, reg, tooling.NewRegistry(dataDir), logs.printf)
	if err != nil {
		t.Fatalf("setupWASMLoader: %v", err)
	}
	defer wr.Close(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cascader, onApply := setupRevocationCascade(ctx, wr, logs.printf)
	if cascader == nil {
		t.Fatal("cascader nil for happy path")
	}
	if onApply == nil {
		t.Fatal("onApply nil for happy path")
	}

	// onApply is the void-returning shape the Refresher consumes; it
	// must call cascader.Refresh() under the hood. Calling it on a
	// no-change baseline is a no-op (returns no closed IDs); calling
	// it after an in-memory Revoke fires a transition.
	pub, _, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	wr.TrustStore.Add(pub)
	if err := wr.TrustStore.PersistRevoke(pub.ID, time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("PersistRevoke: %v", err)
	}

	// onApply itself doesn't return; rely on the cascader's audit log
	// line to confirm the transition was processed.
	onApply()
	if !strings.Contains(logs.joined(), "newly revoked") {
		t.Errorf("expected 'newly revoked' audit line, got:\n%s", logs.joined())
	}
}

// TestSetupRevocationCascade_SIGHUPReloadsAndCascades pins the
// operator-path closing of #396 D8: write a `.revoked` sidecar
// directly under the trust dir (simulating `alf trust revoke`),
// send SIGHUP, observe that the trust store reloaded the sidecar
// and the cascader emitted the transition.
//
// The signal-handling goroutine is daemon-internal; we drive it
// by sending a real SIGHUP to ourselves. The test asserts on
// audit-log presence rather than a sync handshake — using a poll
// loop with a generous deadline matching daemon test convention.
func TestSetupRevocationCascade_SIGHUPReloadsAndCascades(t *testing.T) {
	handle.ResetMintForTesting()

	dataDir := t.TempDir()
	skillsDir := t.TempDir()
	reg := &recordingRegistry{}
	logs := captureLogs()

	wr, err := setupWASMLoader(context.Background(), dataDir, skillsDir, reg, tooling.NewRegistry(dataDir), logs.printf)
	if err != nil {
		t.Fatalf("setupWASMLoader: %v", err)
	}
	defer wr.Close(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cascader, _ := setupRevocationCascade(ctx, wr, logs.printf)
	if cascader == nil {
		t.Fatal("cascader nil for SIGHUP test")
	}

	// Operator path: pubkey + .revoked sidecar land on disk.
	pub, _, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := wr.TrustStore.Persist(pub, "test-key"); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	revokedPath := filepath.Join(wr.TrustStore.Dir(), pub.ID.Hex()+".revoked")
	body := []byte(time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano) + "\n")
	if err := os.WriteFile(revokedPath, body, 0o644); err != nil {
		t.Fatalf("write revoked sidecar: %v", err)
	}

	// Drive the SIGHUP handler. Sending the signal to ourselves is
	// what the Go runtime delivers to signal.Notify subscribers in
	// this same process.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}

	// Poll for the audit line — generous deadline to absorb scheduler
	// jitter on busy CI.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(logs.joined(), "SIGHUP reload:") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(logs.joined(), "SIGHUP reload:") {
		t.Fatalf("SIGHUP audit line never appeared; got:\n%s", logs.joined())
	}
	if !strings.Contains(logs.joined(), "newly revoked") {
		t.Errorf("cascade transition not emitted on SIGHUP path; got:\n%s", logs.joined())
	}
}

// TestSetupRevocationCascade_ContextCancelStopsHandler pins clean
// shutdown: cancelling the wiring context stops the SIGHUP handler
// goroutine. We register a sibling no-op subscriber on SIGHUP for
// the duration of the test so the cancelled-then-signalled flow
// doesn't fall through to the default "terminate process"
// disposition; the assertion is that the daemon's handler emits
// no further audit lines once cancelled.
func TestSetupRevocationCascade_ContextCancelStopsHandler(t *testing.T) {
	handle.ResetMintForTesting()

	dataDir := t.TempDir()
	skillsDir := t.TempDir()
	reg := &recordingRegistry{}
	logs := captureLogs()

	wr, err := setupWASMLoader(context.Background(), dataDir, skillsDir, reg, tooling.NewRegistry(dataDir), logs.printf)
	if err != nil {
		t.Fatalf("setupWASMLoader: %v", err)
	}
	defer wr.Close(context.Background())

	// Sibling subscriber so a SIGHUP during the test cleanup window
	// never reaches the default "terminate" disposition. Drain on
	// teardown.
	keepalive := make(chan os.Signal, 4)
	signal.Notify(keepalive, syscall.SIGHUP)
	defer signal.Stop(keepalive)

	ctx, cancel := context.WithCancel(context.Background())
	cascader, _ := setupRevocationCascade(ctx, wr, logs.printf)
	if cascader == nil {
		t.Fatal("cascader nil")
	}

	cancel()
	// Give the handler goroutine a beat to exit on ctx.Done.
	time.Sleep(50 * time.Millisecond)

	// Sending a SIGHUP after cancel must not produce another audit
	// line — the handler is stopped.
	preLen := len(logs.joined())
	_ = syscall.Kill(syscall.Getpid(), syscall.SIGHUP)
	time.Sleep(100 * time.Millisecond)
	// Drain the keepalive channel so we don't leak signal queue depth.
	select {
	case <-keepalive:
	default:
	}
	if len(logs.joined()) > preLen+200 {
		t.Errorf("post-cancel SIGHUP produced log activity:\n%s", logs.joined()[preLen:])
	}
}
