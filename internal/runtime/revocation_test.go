package runtime

import (
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// sha256HexLocal returns the lowercase hex SHA-256 digest of b. Mirrors
// the inline helper in instantiator_verified_test.go's signBundle.
func sha256HexLocal(b []byte) string {
	h := sha256.Sum256(b)
	const hex = "0123456789abcdef"
	out := make([]byte, 64)
	for i, v := range h {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out)
}

// recordingLogger is a thread-safe revocation-log capture used to
// assert the audit lines emitted by RevokeByKey.
type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (r *recordingLogger) printf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Crude format: we only need substring matching in the assertions.
	r.lines = append(r.lines, fmtArgs(format, args...))
}

func (r *recordingLogger) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

// fmtArgs is a minimal stand-in for fmt.Sprintf — avoids the whole
// stdlib formatting cost per call. Good enough for substring asserts.
func fmtArgs(format string, args ...any) string {
	// We accept that this loses precise format-spec semantics; tests
	// substring-match on stable parts (instance ID, key fingerprint).
	return format + ":" + spew(args...)
}

func spew(args ...any) string {
	var sb strings.Builder
	for i, a := range args {
		if i > 0 {
			sb.WriteString(",")
		}
		switch v := a.(type) {
		case string:
			sb.WriteString(v)
		default:
			sb.WriteString("?")
		}
	}
	return sb.String()
}

// signWithStoreRT is a runtime-package helper mirroring the E2E test's
// helper — sign a manifest against a pre-existing key + store.
func signWithStoreRT(t *testing.T, priv envelope.PrivateKey, store envelope.TrustStore, manifestTOML, bundle string) envelope.VerifyInput {
	t.Helper()
	canonical, err := envelope.Canonicalize([]byte(manifestTOML))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := envelope.Sign(priv, canonical)
	if err != nil {
		t.Fatal(err)
	}
	tc := envelope.TrustedComment{
		BundleID: "rt-bundle",
		SignedAt: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
	}
	if bundle != "" {
		// Compute SHA-256 inline without importing crypto/sha256 here:
		// reuse the verify pipeline's expectation by deferring to the
		// signing helper's existing path. We just embed a fake hash
		// when bundle bytes are nil; for non-empty we call out.
		// Easier: sign with bytes via signBundle's flow — but signBundle
		// generates a fresh keypair. So we replicate its hash step.
		tc.BundleHash = sha256HexLocal([]byte(bundle))
	}
	sigFile, err := envelope.EncodeSignatureFile(priv, sig, envelope.BuildTrustedComment(tc))
	if err != nil {
		t.Fatal(err)
	}
	in := envelope.VerifyInput{
		ManifestTOML: []byte(manifestTOML),
		Signature:    sigFile,
		TrustStore:   store,
	}
	if bundle != "" {
		in.Bundle = []byte(bundle)
	}
	return in
}

const revokeProducerManifest = `alf_envelope_version = 1
id      = "rev-cap-a"
kind    = "wasm-tool"
version = "0.1.0"
name    = "RevA"

[[fs.reads]]
path = "data/"
`

const revokeProducerBManifest = `alf_envelope_version = 1
id      = "rev-cap-b"
kind    = "wasm-tool"
version = "0.1.0"
name    = "RevB"

[[fs.reads]]
path = "data/"
`

// TestRevokeByKey_ClosesOnlyMatching pins the core behaviour: a
// RevokeByKey call with the daemon key fingerprint closes every
// Instance signed by THAT key, and leaves Instances signed by a
// different key alive. Two keys, two stores merged into one Instantiator
// store; one revocation; assert split.
func TestRevokeByKey_ClosesOnlyMatching(t *testing.T) {
	handle.ResetMintForTesting()

	pubA, privA, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pubB, privB, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pubA)
	store.Add(pubB)

	logger := &recordingLogger{}
	inst := NewInstantiator(WithRevocationLogger(logger.printf))

	// Two caps signed by key A.
	inA1, err := inst.InstantiateVerified(context.Background(), signWithStoreRT(t, privA, store, revokeProducerManifest, "bundle-a-1"), "")
	if err != nil {
		t.Fatalf("instantiate A1: %v", err)
	}
	inA2, err := inst.InstantiateVerified(context.Background(), signWithStoreRT(t, privA, store, revokeProducerBManifest, "bundle-a-2"), "")
	if err != nil {
		t.Fatalf("instantiate A2: %v", err)
	}
	// One cap signed by key B.
	inB1, err := inst.InstantiateVerified(context.Background(), signWithStoreRT(t, privB, store, revokeProducerManifest, "bundle-b-1"), "")
	if err != nil {
		t.Fatalf("instantiate B1: %v", err)
	}

	if got := inst.LiveCount(); got != 3 {
		t.Fatalf("LiveCount=%d, want 3", got)
	}

	// Sanity — both keys come back distinct (this is what the
	// caller would see).
	if inA1.SignerID == inB1.SignerID {
		t.Fatal("test setup: SignerIDs collided")
	}

	// Revoke key A — both rev-cap-a and rev-cap-b instances signed
	// by it must close. The B-signed instance must stay alive.
	closed := inst.RevokeByKey(inA1.SignerID)
	if len(closed) != 2 {
		t.Fatalf("RevokeByKey returned %d ids, want 2: %v", len(closed), closed)
	}

	// Both A-signed lifecycles cancelled; B-signed still alive.
	if inA1.Instance.Context().Err() == nil {
		t.Error("A1 ctx not cancelled by RevokeByKey")
	}
	if inA2.Instance.Context().Err() == nil {
		t.Error("A2 ctx not cancelled by RevokeByKey")
	}
	if inB1.Instance.Context().Err() != nil {
		t.Errorf("B1 ctx cancelled but key B was not revoked: %v", inB1.Instance.Context().Err())
	}

	// Audit log: one line per closed instance, each mentioning the
	// instance ID and a key fingerprint substring.
	lines := logger.all()
	if len(lines) != 2 {
		t.Errorf("expected 2 audit lines, got %d: %v", len(lines), lines)
	}
	for _, l := range lines {
		if !strings.Contains(l, "[revocation]") {
			t.Errorf("audit line missing tag: %q", l)
		}
	}

	// Watcher goroutines need to observe the cancelled ctx and
	// drop their entries. Give them up to 200ms.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if inst.LiveCount() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := inst.LiveCount(); got != 1 {
		t.Errorf("LiveCount after revocation=%d, want 1", got)
	}
}

// TestRevokeByKey_UnknownKeyIsNoOp pins that revoking a fingerprint
// that doesn't match any live Instance returns an empty slice and
// touches nothing.
func TestRevokeByKey_UnknownKeyIsNoOp(t *testing.T) {
	handle.ResetMintForTesting()

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	logger := &recordingLogger{}
	inst := NewInstantiator(WithRevocationLogger(logger.printf))

	in, err := inst.InstantiateVerified(context.Background(), signWithStoreRT(t, priv, store, revokeProducerManifest, "x"), "")
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}

	// Use a clearly-not-matching fingerprint.
	var ghost envelope.KeyID
	for i := range ghost {
		ghost[i] = 0xff
	}
	closed := inst.RevokeByKey(ghost)
	if len(closed) != 0 {
		t.Errorf("RevokeByKey on unknown key returned %v, want empty", closed)
	}
	if in.Instance.Context().Err() != nil {
		t.Error("Instance lifecycle cancelled despite key mismatch")
	}
	if len(logger.all()) != 0 {
		t.Errorf("expected no audit lines on no-op revoke, got %v", logger.all())
	}
	if inst.LiveCount() != 1 {
		t.Errorf("LiveCount=%d after no-op revoke, want 1", inst.LiveCount())
	}
}

// TestRevokeByKey_ConcurrentWithExternalClose pins safety: a
// concurrent user-driven Close + RevokeByKey on the same Instance
// must not deadlock, double-close, or panic. The Instance.closeOnce
// guard makes Close idempotent at the handle layer; this test pins
// that the registry pruning paths interleave safely.
func TestRevokeByKey_ConcurrentWithExternalClose(t *testing.T) {
	handle.ResetMintForTesting()

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	inst := NewInstantiator()

	const N = 20
	results := make([]*VerifiedInstantiation, N)
	for i := 0; i < N; i++ {
		results[i], err = inst.InstantiateVerified(context.Background(), signWithStoreRT(t, priv, store, revokeProducerManifest, "b"+string(rune('a'+i))), "")
		if err != nil {
			t.Fatalf("instantiate %d: %v", i, err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	// Goroutine 1: external Close on every other Instance.
	go func() {
		defer wg.Done()
		for i := 0; i < N; i += 2 {
			results[i].Instance.Close()
		}
	}()
	// Goroutine 2: RevokeByKey races against the external Closes.
	go func() {
		defer wg.Done()
		_ = inst.RevokeByKey(results[0].SignerID)
	}()

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Close + RevokeByKey deadlock or hang detected")
	}

	// All Instances must have a cancelled ctx (revoke covered the
	// rest, external Close covered the evens — together = everyone).
	for i, r := range results {
		if r.Instance.Context().Err() == nil {
			t.Errorf("instance %d still alive after concurrent Close + Revoke", i)
		}
	}

	// Wait for the watcher goroutines to drain.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if inst.LiveCount() == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if inst.LiveCount() != 0 {
		t.Errorf("LiveCount=%d after all Closed, want 0", inst.LiveCount())
	}
}

// TestRevokeByKey_InFlightOpsCancelWithinBudget composes #396 deliverable 1
// (lifecycleCtx unwind under 100ms) with deliverable 3 (key-based
// revoke). Spawns an Instance with a stalling tool invoker, calls
// RevokeByKey, asserts the in-flight Invoke returns within the budget.
func TestRevokeByKey_InFlightOpsCancelWithinBudget(t *testing.T) {
	handle.ResetMintForTesting()

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	stallInv := &runtimeStallingInvoker{started: make(chan struct{})}
	inst := NewInstantiator(WithToolInvoker(stallInv))

	const manifest = `alf_envelope_version = 1
id      = "rev-tool-cap"
kind    = "wasm-tool"
version = "0.1.0"
name    = "RevTool"

[[tools.declares]]
id = "target"
`
	in, err := inst.InstantiateVerified(context.Background(), signWithStoreRT(t, priv, store, manifest, "b"), "")
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}

	if in.Instance.Tool == nil {
		t.Fatal("tool handle nil — invoker not wired through")
	}

	invokeDone := make(chan struct{})
	var invokeErr atomic.Value
	go func() {
		defer close(invokeDone)
		_, err := in.Instance.Tool.Invoke(context.Background(), "target", nil)
		if err != nil {
			invokeErr.Store(err)
		}
	}()
	<-stallInv.started

	start := time.Now()
	closed := inst.RevokeByKey(in.SignerID)
	if len(closed) != 1 {
		t.Fatalf("RevokeByKey closed %d, want 1", len(closed))
	}

	select {
	case <-invokeDone:
		elapsed := time.Since(start)
		if elapsed > 200*time.Millisecond {
			t.Errorf("RevokeByKey unwind took %v — budget 200ms (#396 deliverables 1+3)", elapsed)
		}
		if invokeErr.Load() == nil {
			t.Error("Invoke returned nil error after revoke")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Invoke did not unwind within 2s after RevokeByKey")
	}
}

// TestRevokeByKey_RegistryPrunedOnExternalClose pins that the watcher
// goroutine drops a live entry as soon as the user calls Close()
// directly, without going through RevokeByKey.
func TestRevokeByKey_RegistryPrunedOnExternalClose(t *testing.T) {
	handle.ResetMintForTesting()
	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)
	inst := NewInstantiator()

	in, err := inst.InstantiateVerified(context.Background(), signWithStoreRT(t, priv, store, revokeProducerManifest, "b"), "")
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	if inst.LiveCount() != 1 {
		t.Fatalf("LiveCount before Close=%d", inst.LiveCount())
	}

	in.Instance.Close()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if inst.LiveCount() == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if inst.LiveCount() != 0 {
		t.Errorf("LiveCount=%d after Close, want 0", inst.LiveCount())
	}
}

// TestRevokeByKey_VerifiedInstantiationCarriesIDs pins the surface
// extension: VerifiedInstantiation now exposes SignerID + SignedAt
// so loaders + audit code can correlate without re-parsing the
// envelope.
func TestRevokeByKey_VerifiedInstantiationCarriesIDs(t *testing.T) {
	handle.ResetMintForTesting()
	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)
	inst := NewInstantiator()

	in, err := inst.InstantiateVerified(context.Background(), signWithStoreRT(t, priv, store, revokeProducerManifest, "b"), "")
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}

	var zero envelope.KeyID
	if in.SignerID == zero {
		t.Error("VerifiedInstantiation.SignerID is zero — should equal the trust-store fingerprint")
	}
	if in.SignedAt.IsZero() {
		t.Error("VerifiedInstantiation.SignedAt is zero — should be the trusted-comment timestamp")
	}
	want := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	if !in.SignedAt.Equal(want) {
		t.Errorf("SignedAt=%v, want %v", in.SignedAt, want)
	}
}

// runtimeStallingInvoker is a ToolInvoker that blocks on Invoke until
// the caller's ctx is cancelled.
type runtimeStallingInvoker struct {
	started   chan struct{}
	startOnce sync.Once
}

func (s *runtimeStallingInvoker) Invoke(ctx context.Context, target capability.ID, in capability.Input) (capability.Output, error) {
	s.startOnce.Do(func() { close(s.started) })
	<-ctx.Done()
	return capability.Output{}, ctx.Err()
}
