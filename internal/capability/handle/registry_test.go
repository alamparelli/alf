package handle

import (
	"errors"
	"sync"
	"testing"
)

// mintForRegistryTest returns the minted runtime token for tests that
// need to mutate the registry. ResetMintForTesting clears the one-shot
// state between cases so each case can call MintRuntimeToken fresh.
func mintForRegistryTest(t *testing.T) RuntimeToken {
	t.Helper()
	ResetMintForTesting()
	return MintRuntimeToken()
}

func TestHandleRegistry_RegisterAndLookup(t *testing.T) {
	tok := mintForRegistryTest(t)
	r := NewHandleRegistry()

	if err := r.Register(tok, HandleKind{Namespace: AlfNamespace, ID: "fs"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := r.Lookup(AlfNamespace, "fs")
	if !ok {
		t.Fatal("Lookup(alf, fs)=!ok, want ok")
	}
	if got.Namespace != AlfNamespace || got.ID != "fs" {
		t.Errorf("Lookup got=%+v, want {alf, fs}", got)
	}
	if got.FullName() != "alf:fs" {
		t.Errorf("FullName=%q, want alf:fs", got.FullName())
	}
}

func TestHandleRegistry_LookupMiss(t *testing.T) {
	mintForRegistryTest(t)
	r := NewHandleRegistry()
	if _, ok := r.Lookup(AlfNamespace, "fs"); ok {
		t.Error("empty registry returned ok for alf:fs")
	}
}

// Token gate — a registry call without the minted token fails. The
// load-bearing pin: an attacker forging a RuntimeToken via composite
// literal of the public type cannot mutate the registry, because
// RuntimeToken's `key` field is unexported.
func TestHandleRegistry_TokenGateRejectsZeroToken(t *testing.T) {
	mintForRegistryTest(t)
	r := NewHandleRegistry()
	var bogus RuntimeToken
	err := r.Register(bogus, HandleKind{Namespace: AlfNamespace, ID: "fs"})
	if !errors.Is(err, ErrInvalidRegistryToken) {
		t.Fatalf("want ErrInvalidRegistryToken, got %v", err)
	}
}

// A Register before MintRuntimeToken has been called must also fail —
// even with a token-shaped argument, the registry has no minted token
// to compare against. Defends against use-before-init wiring bugs.
func TestHandleRegistry_TokenGateRejectsBeforeMint(t *testing.T) {
	ResetMintForTesting()
	r := NewHandleRegistry()
	var blank RuntimeToken
	err := r.Register(blank, HandleKind{Namespace: AlfNamespace, ID: "fs"})
	if !errors.Is(err, ErrInvalidRegistryToken) {
		t.Fatalf("want ErrInvalidRegistryToken, got %v", err)
	}
}

func TestHandleRegistry_EmptyNamespaceRejected(t *testing.T) {
	tok := mintForRegistryTest(t)
	r := NewHandleRegistry()
	err := r.Register(tok, HandleKind{Namespace: "", ID: "fs"})
	if !errors.Is(err, ErrRegistryEmptyNamespace) {
		t.Fatalf("want ErrRegistryEmptyNamespace, got %v", err)
	}
}

func TestHandleRegistry_EmptyIDRejected(t *testing.T) {
	tok := mintForRegistryTest(t)
	r := NewHandleRegistry()
	err := r.Register(tok, HandleKind{Namespace: AlfNamespace, ID: ""})
	if !errors.Is(err, ErrRegistryEmptyID) {
		t.Fatalf("want ErrRegistryEmptyID, got %v", err)
	}
}

func TestHandleRegistry_DuplicateRejected(t *testing.T) {
	tok := mintForRegistryTest(t)
	r := NewHandleRegistry()
	if err := r.Register(tok, HandleKind{Namespace: "abc123", ID: "bluetooth.scan"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(tok, HandleKind{Namespace: "abc123", ID: "bluetooth.scan"})
	if !errors.Is(err, ErrRegistryDuplicate) {
		t.Fatalf("want ErrRegistryDuplicate, got %v", err)
	}
}

// The reserved alf: namespace can ONLY hold ids from AlfCoreHandleIDs.
// A provider attempting to register a core-id-shaped name under alf:
// (e.g. alf:bluetooth.scan) is the load-bearing #392 invariant — the
// alf: namespace is the daemon's, not anybody else's. Even with the
// runtime token, only the documented core ids may register there.
func TestHandleRegistry_AlfReservedToCoreKinds(t *testing.T) {
	tok := mintForRegistryTest(t)
	r := NewHandleRegistry()
	err := r.Register(tok, HandleKind{Namespace: AlfNamespace, ID: "bluetooth.scan"})
	if !errors.Is(err, ErrRegistryReservedNS) {
		t.Fatalf("want ErrRegistryReservedNS, got %v", err)
	}
}

// Every documented core id must be acceptable under alf:.
func TestHandleRegistry_AlfAcceptsAllCoreIDs(t *testing.T) {
	tok := mintForRegistryTest(t)
	r := NewHandleRegistry()
	for _, id := range AlfCoreHandleIDs {
		if err := r.Register(tok, HandleKind{Namespace: AlfNamespace, ID: id}); err != nil {
			t.Errorf("alf:%s: %v", id, err)
		}
	}
}

func TestHandleRegistry_RegisterCoreSeedsAllAlfKinds(t *testing.T) {
	tok := mintForRegistryTest(t)
	r := NewHandleRegistry()
	if err := r.RegisterCore(tok); err != nil {
		t.Fatalf("RegisterCore: %v", err)
	}
	if got := r.Len(); got != len(AlfCoreHandleIDs) {
		t.Errorf("Len=%d, want %d", got, len(AlfCoreHandleIDs))
	}
	for _, id := range AlfCoreHandleIDs {
		if _, ok := r.Lookup(AlfNamespace, id); !ok {
			t.Errorf("after RegisterCore: alf:%s not found", id)
		}
	}
}

// Calling RegisterCore twice on the same registry fails — every entry
// duplicates the first call. This guards against wiring bugs where
// the daemon boot path runs the seed step more than once.
func TestHandleRegistry_RegisterCoreTwiceFails(t *testing.T) {
	tok := mintForRegistryTest(t)
	r := NewHandleRegistry()
	if err := r.RegisterCore(tok); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := r.RegisterCore(tok)
	if !errors.Is(err, ErrRegistryDuplicate) {
		t.Fatalf("want ErrRegistryDuplicate on second call, got %v", err)
	}
}

func TestHandleRegistry_ListSortedByFullName(t *testing.T) {
	tok := mintForRegistryTest(t)
	r := NewHandleRegistry()
	// Register out of order; List must come back sorted.
	in := []HandleKind{
		{Namespace: "zfp", ID: "later.kind"},
		{Namespace: "abc123", ID: "bluetooth.scan"},
		{Namespace: AlfNamespace, ID: "fs"},
	}
	for _, k := range in {
		if err := r.Register(tok, k); err != nil {
			t.Fatalf("Register %s: %v", k.FullName(), err)
		}
	}
	got := r.List()
	want := []string{"abc123:bluetooth.scan", "alf:fs", "zfp:later.kind"}
	if len(got) != len(want) {
		t.Fatalf("List len=%d, want %d", len(got), len(want))
	}
	for i, k := range got {
		if k.FullName() != want[i] {
			t.Errorf("List[%d]=%q, want %q", i, k.FullName(), want[i])
		}
	}
}

// List returns a fresh slice — mutating it does not affect registry state.
func TestHandleRegistry_ListReturnsCopy(t *testing.T) {
	tok := mintForRegistryTest(t)
	r := NewHandleRegistry()
	if err := r.Register(tok, HandleKind{Namespace: AlfNamespace, ID: "fs"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got := r.List()
	got[0].ID = "tampered"
	// Re-Lookup should still see the original.
	k, ok := r.Lookup(AlfNamespace, "fs")
	if !ok || k.ID != "fs" {
		t.Errorf("registry mutated via List result: got=%+v", k)
	}
}

// Concurrent Register + Lookup: registry's RWMutex must let many
// goroutines read while one writes. Catch-all sanity check —
// `go test -race` is the actual check.
func TestHandleRegistry_ConcurrentReadersWriter(t *testing.T) {
	tok := mintForRegistryTest(t)
	r := NewHandleRegistry()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			r.Lookup(AlfNamespace, "fs")
			r.Lookup("nope", "missing")
		}
	}()
	go func() {
		defer wg.Done()
		// Write each AlfCoreHandleIDs entry once. RegisterCore would
		// be the natural call but doing them individually exercises
		// the lock acquisition pattern under contention.
		for _, id := range AlfCoreHandleIDs {
			_ = r.Register(tok, HandleKind{Namespace: AlfNamespace, ID: id})
		}
	}()
	wg.Wait()
}

// Pin: the in-handle-package coreHandleIDs set agrees with
// envelope/schema.go's coreHandleIDs map. Drift between them is the
// bug class this test catches: a manifest declaring depends.handle =
// "alf:fs" passes envelope validation, then RegistryLookup fails at
// runtime — the user sees a confusing "alf:fs not found" instead of
// schema rejection. Stage 3's forge integration will fail loudly
// either way, but Stage 2 keeps the two lists synchronised at the
// package boundary.
//
// We test the runtime-side list directly here. The envelope-side
// pin lives in internal/archtest/raw_imports_classification_test.go
// (TestRawImportsClassificationPinned reads schema.go source).
func TestHandleRegistry_AlfCoreIDsCoverEveryDocumentedKind(t *testing.T) {
	expected := map[string]bool{
		"fs":         true,
		"http":       true,
		"exec":       true,
		"secrets":    true,
		"events.pub": true,
		"events.sub": true,
		"tool":       true,
	}
	if len(AlfCoreHandleIDs) != len(expected) {
		t.Errorf("AlfCoreHandleIDs len=%d, want %d", len(AlfCoreHandleIDs), len(expected))
	}
	for _, id := range AlfCoreHandleIDs {
		if !expected[id] {
			t.Errorf("AlfCoreHandleIDs contains unexpected id %q (drift from MANIFEST-SCHEMA §3.4)", id)
		}
		delete(expected, id)
	}
	for id := range expected {
		t.Errorf("AlfCoreHandleIDs missing documented id %q (drift from MANIFEST-SCHEMA §3.4)", id)
	}
}
