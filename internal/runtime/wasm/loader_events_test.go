package wasm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
	"github.com/alamparelli/alf/internal/runtime"
	"github.com/alamparelli/alf/internal/runtime/events"
)

// preSignSubscriberBundle pre-signs a subscriber manifest at
// <root>/<id>/manifest.sig so the loader skips the Tier-2 auto-sign
// path. SEC-004 makes the auto-signer refuse cross-flow
// subscriptions; in production, such bundles must be signed with a
// user-endorsed key. These tests simulate that with a manual
// pre-sign using the same trust-store key.
func preSignSubscriberBundle(t *testing.T, root, id, manifest string, wasm []byte, priv envelope.PrivateKey, now time.Time) {
	t.Helper()
	dir := filepath.Join(root, id)
	canonical, err := envelope.Canonicalize([]byte(manifest))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := envelope.Sign(priv, canonical)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(wasm)
	tc := envelope.TrustedComment{
		BundleID:   "manual-signed-" + hex.EncodeToString(hash[:4]),
		BundleHash: hex.EncodeToString(hash[:]),
		SignedAt:   now.UTC(),
	}
	sigFile, err := envelope.EncodeSignatureFile(priv, sig, envelope.BuildTrustedComment(tc))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.sig"), sigFile, 0o600); err != nil {
		t.Fatal(err)
	}
}

// newTestLoaderRuntimeWithEvents builds a fresh Instantiator wired
// with an events.Bus + events.MemoryRegistry, returning everything
// the loader needs to exercise the events forge path. Mirrors
// newTestLoaderRuntime but adds the events plumbing.
func newTestLoaderRuntimeWithEvents(t *testing.T) (*Runtime, *events.Bus, *events.MemoryRegistry, envelope.PrivateKey, envelope.TrustStore, *recordingRegistry) {
	t.Helper()

	handle.ResetMintForTesting()
	bus := events.New()
	registry := events.NewMemoryRegistry()
	inst := runtime.NewInstantiator(
		runtime.WithEventsBus(bus, bus),
		runtime.WithCrossFlowRegistry(registry),
	)
	rt, err := NewRuntime(context.Background(), inst)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	return rt, bus, registry, priv, store, &recordingRegistry{}
}

const publisherManifest = `alf_envelope_version = 1
id      = "pub-cap"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Publisher"

[[events.exports]]
topic = "chat.log"
`

const subscriberManifest = `alf_envelope_version = 1
id      = "sub-cap"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Subscriber"

[[events.subscribes]]
from  = "pub-cap"
topic = "chat.log"
`

// TestLoader_CrossFlowForged verifies §3.3 happy path: when both sides
// declare the cross-flow, the loader forges both handles and the bus
// routes a publish to the subscriber.
func TestLoader_CrossFlowForged(t *testing.T) {
	rt, _, registry, priv, store, reg := newTestLoaderRuntimeWithEvents(t)
	root := t.TempDir()
	snapshotDir := t.TempDir()

	writeBundle(t, root, "pub-cap", publisherManifest, minimalWasmBytes())
	writeBundle(t, root, "sub-cap", subscriberManifest, minimalWasmBytes())
	// SEC-004: cross-flow subscribers exceed the Tier-2 ceiling, so the
	// auto-signer refuses them. Pre-sign with the same trust-store key
	// to simulate the user-endorsed flow this test pre-dates.
	preSignSubscriberBundle(t, root, "sub-cap", subscriberManifest, minimalWasmBytes(), priv, fixedLoaderNow())

	logs := newLogCapture()
	l := &Loader{
		Runtime:     rt,
		Registry:    reg,
		DaemonPriv:  priv,
		TrustStore:  store,
		Logger:      logs.printf,
		Now:         fixedLoaderNow,
		CrossFlow:   registry,
		SnapshotDir: snapshotDir,
	}

	loaded, errs := l.LoadDir(context.Background(), root)
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	if len(loaded) != 2 {
		t.Errorf("loaded=%v, want 2", loaded)
	}

	// Cross-flow registered.
	if !registry.HasExport("pub-cap", "chat.log") {
		t.Error("registry missing pub-cap export")
	}

	// Boot-time log line surfaced (Option B UX).
	if !logs.contains(`[events] cross-flow established: sub-cap ← pub-cap:"chat.log"`) {
		t.Errorf("missing cross-flow log; got:\n%s", logs.joined())
	}

	// Snapshot file written.
	snap := filepath.Join(snapshotDir, "events", events.SnapshotFile)
	b, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var entries []events.FlowEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		t.Fatalf("snapshot json: %v", err)
	}
	if len(entries) != 1 || entries[0].Publisher != "pub-cap" || entries[0].Topic != "chat.log" || entries[0].Subscriber != "sub-cap" {
		t.Errorf("snapshot entries=%+v", entries)
	}
}

// TestLoader_SubscriberWithoutPublisherSkipped covers §3.3 deny-by-default:
// a subscriber whose publisher is not installed gets no handle forged
// and the loader logs the unresolved cross-flow.
func TestLoader_SubscriberWithoutPublisherSkipped(t *testing.T) {
	rt, _, registry, priv, store, reg := newTestLoaderRuntimeWithEvents(t)
	root := t.TempDir()
	writeBundle(t, root, "sub-cap", subscriberManifest, minimalWasmBytes())
	// SEC-004: pre-sign because the subscriber's [[events.subscribes]]
	// exceeds the Tier-2 auto-sign ceiling.
	preSignSubscriberBundle(t, root, "sub-cap", subscriberManifest, minimalWasmBytes(), priv, fixedLoaderNow())

	logs := newLogCapture()
	l := &Loader{
		Runtime:    rt,
		Registry:   reg,
		DaemonPriv: priv,
		TrustStore: store,
		Logger:     logs.printf,
		Now:        fixedLoaderNow,
		CrossFlow:  registry,
	}

	loaded, errs := l.LoadDir(context.Background(), root)
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	if len(loaded) != 1 {
		t.Errorf("loaded=%v, want sub-cap only", loaded)
	}
	// No flows registered.
	if registry.HasExport("pub-cap", "chat.log") {
		t.Error("registry should not have pub-cap export")
	}
	// Deny-by-default log line surfaced.
	if !logs.contains(`subscribe ignored (publisher not installed or topic not exported): sub-cap wants pub-cap:"chat.log"`) {
		t.Errorf("missing deny log; got:\n%s", logs.joined())
	}
}

// TestLoader_PublisherBeforeSubscriber_TwoPassWorks verifies the
// two-pass design: even if alphabetical scan order puts subscriber
// first, the publisher's exports are registered in pass 1 before any
// subscriber is forged in pass 2. (sub-cap < pub-cap alphabetically.)
func TestLoader_TwoPassResolvesRegardlessOfOrder(t *testing.T) {
	rt, _, registry, priv, store, reg := newTestLoaderRuntimeWithEvents(t)
	root := t.TempDir()

	// sub-cap directory sorts BEFORE pub-cap in alphabetical scan.
	// Without the two-pass design, sub-cap would be forged first when
	// pub-cap's exports aren't yet registered, and the cross-flow
	// would be silently lost.
	writeBundle(t, root, "sub-cap", subscriberManifest, minimalWasmBytes())
	writeBundle(t, root, "pub-cap", publisherManifest, minimalWasmBytes())
	// SEC-004: pre-sign cross-flow subscriber.
	preSignSubscriberBundle(t, root, "sub-cap", subscriberManifest, minimalWasmBytes(), priv, fixedLoaderNow())

	logs := newLogCapture()
	l := &Loader{
		Runtime:    rt,
		Registry:   reg,
		DaemonPriv: priv,
		TrustStore: store,
		Logger:     logs.printf,
		Now:        fixedLoaderNow,
		CrossFlow:  registry,
	}

	loaded, errs := l.LoadDir(context.Background(), root)
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	if len(loaded) != 2 {
		t.Errorf("loaded=%v, want 2", loaded)
	}
	if !logs.contains(`cross-flow established: sub-cap ← pub-cap:"chat.log"`) {
		t.Errorf("two-pass should have resolved cross-flow despite scan order; got:\n%s", logs.joined())
	}
}

// TestLoader_NoEventsWiringStillWorks regression-protects the legacy
// path: a Loader without CrossFlow registry / SnapshotDir set still
// loads bundles correctly. Events declarations in manifests parse but
// no handles are forged (no bus to forge against in this Instantiator).
func TestLoader_NoEventsWiringStillWorks(t *testing.T) {
	// Use the non-events runtime constructor — no bus, no registry.
	rt, priv, store, reg := newTestLoaderRuntime(t)
	root := t.TempDir()
	writeBundle(t, root, "pub-cap", publisherManifest, minimalWasmBytes())

	l := &Loader{
		Runtime:    rt,
		Registry:   reg,
		DaemonPriv: priv,
		TrustStore: store,
		Now:        fixedLoaderNow,
		// CrossFlow + SnapshotDir intentionally unset.
	}

	loaded, errs := l.LoadDir(context.Background(), root)
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	if len(loaded) != 1 {
		t.Errorf("loaded=%v, want 1", loaded)
	}
}

// --- helpers ---

func fixedLoaderNow() time.Time { return loaderTestNow }

var loaderTestNow = parseFixedNow()

func parseFixedNow() time.Time {
	t, _ := time.Parse(time.RFC3339, "2026-04-25T18:30:00Z")
	return t
}

type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func newLogCapture() *logCapture { return &logCapture{} }

func (l *logCapture) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logCapture) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func (l *logCapture) contains(needle string) bool {
	return strings.Contains(l.joined(), needle)
}
