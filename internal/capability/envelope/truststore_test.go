package envelope

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryTrustStore_AddLookupRemove(t *testing.T) {
	s := NewMemoryTrustStore()
	pub, _ := mustGenKey(t)

	if _, ok, _ := s.Lookup(pub.ID); ok {
		t.Fatal("empty store should not have the key yet")
	}

	s.Add(pub)
	got, ok, err := s.Lookup(pub.ID)
	if err != nil || !ok {
		t.Fatalf("Lookup after Add: ok=%v err=%v", ok, err)
	}
	if got.ID != pub.ID {
		t.Errorf("ID mismatch after round-trip")
	}

	s.Remove(pub.ID)
	if _, ok, _ := s.Lookup(pub.ID); ok {
		t.Error("Remove failed — key still present")
	}
}

func TestMemoryTrustStore_Keys(t *testing.T) {
	s := NewMemoryTrustStore()
	a, _ := mustGenKey(t)
	b, _ := mustGenKey(t)
	s.Add(a)
	s.Add(b)

	keys := s.Keys()
	if len(keys) != 2 {
		t.Fatalf("Keys()=%d, want 2", len(keys))
	}
	have := map[KeyID]bool{a.ID: false, b.ID: false}
	for _, k := range keys {
		have[k] = true
	}
	for id, seen := range have {
		if !seen {
			t.Errorf("key %s missing from Keys()", id.Hex())
		}
	}
}

func TestDirTrustStore_LoadMissingDirIsEmpty(t *testing.T) {
	s := NewDirTrustStore(filepath.Join(t.TempDir(), "absent"))
	if err := s.Load(); err != nil {
		t.Fatalf("Load on missing dir should not error: %v", err)
	}
	if len(s.Keys()) != 0 {
		t.Errorf("missing dir should yield empty store")
	}
}

func TestDirTrustStore_LoadReadsPubFiles(t *testing.T) {
	dir := t.TempDir()
	pubA, _ := mustGenKey(t)
	pubB, _ := mustGenKey(t)

	writePubFile(t, dir, "a.pub", pubA, "key a")
	writePubFile(t, dir, "b.pub", pubB, "key b")
	// Non-.pub files are ignored.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := NewDirTrustStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(s.Keys()) != 2 {
		t.Errorf("loaded %d keys, want 2", len(s.Keys()))
	}
	if got, ok, _ := s.Lookup(pubA.ID); !ok || got.ID != pubA.ID {
		t.Error("pubA not found or wrong")
	}
	if got, ok, _ := s.Lookup(pubB.ID); !ok || got.ID != pubB.ID {
		t.Error("pubB not found or wrong")
	}
}

func TestDirTrustStore_LoadRejectsMalformedFile(t *testing.T) {
	dir := t.TempDir()
	pub, _ := mustGenKey(t)
	writePubFile(t, dir, "good.pub", pub, "ok")

	// Corrupted file: present but unparseable.
	if err := os.WriteFile(filepath.Join(dir, "bad.pub"), []byte("not a minisign pubkey"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := NewDirTrustStore(dir)
	if err := s.Load(); !errors.Is(err, ErrTrustStoreCorrupt) {
		t.Fatalf("want ErrTrustStoreCorrupt, got %v", err)
	}

	// Confirm the store wasn't left half-populated.
	if len(s.Keys()) != 0 {
		t.Errorf("failed Load should leave store empty, got %d keys", len(s.Keys()))
	}
}

func TestDirTrustStore_LoadRejectsDuplicateKeyID(t *testing.T) {
	dir := t.TempDir()
	pub, _ := mustGenKey(t)
	writePubFile(t, dir, "first.pub", pub, "first file")
	writePubFile(t, dir, "second.pub", pub, "dup of first")

	s := NewDirTrustStore(dir)
	err := s.Load()
	if !errors.Is(err, ErrTrustStoreCorrupt) {
		t.Fatalf("want ErrTrustStoreCorrupt on duplicate ID, got %v", err)
	}
}

func TestDirTrustStore_ReloadReplacesOldKeys(t *testing.T) {
	dir := t.TempDir()
	pubOld, _ := mustGenKey(t)
	writePubFile(t, dir, "old.pub", pubOld, "old")

	s := NewDirTrustStore(dir)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Lookup(pubOld.ID); !ok {
		t.Fatal("setup: old key should be present")
	}

	// Remove old, add new, reload.
	if err := os.Remove(filepath.Join(dir, "old.pub")); err != nil {
		t.Fatal(err)
	}
	pubNew, _ := mustGenKey(t)
	writePubFile(t, dir, "new.pub", pubNew, "new")

	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Lookup(pubOld.ID); ok {
		t.Error("reload should have dropped old key")
	}
	if _, ok, _ := s.Lookup(pubNew.ID); !ok {
		t.Error("reload should have picked up new key")
	}
}

// helper
func writePubFile(t *testing.T, dir, filename string, pub PublicKey, comment string) {
	t.Helper()
	raw, err := EncodePublicKeyFile(pub, comment)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDirTrustStore_PersistRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "trust") // dir does not exist yet
	s := NewDirTrustStore(dir)
	pub, _ := mustGenKey(t)

	if err := s.Persist(pub, "alf operator key"); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if _, ok, _ := s.Lookup(pub.ID); !ok {
		t.Error("in-memory entry missing after Persist")
	}
	// File present at the deterministic name.
	want := filepath.Join(dir, pub.ID.Hex()+".pub")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("on-disk file missing: %v", err)
	}

	// A fresh store loads the same key.
	s2 := NewDirTrustStore(dir)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok, _ := s2.Lookup(pub.ID); !ok {
		t.Error("reload-after-Persist did not see the key")
	}
}

func TestDirTrustStore_PersistOverwriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	s := NewDirTrustStore(dir)
	pub, _ := mustGenKey(t)

	if err := s.Persist(pub, "v1"); err != nil {
		t.Fatal(err)
	}
	// Re-persist with a different comment must succeed and not leave
	// stale tmp files behind.
	if err := s.Persist(pub, "v2"); err != nil {
		t.Fatalf("re-Persist: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || hasSubstr(e.Name(), ".tmp-") {
			t.Errorf("stale tmp file leftover: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 file after overwrite, got %d", len(entries))
	}
}

func TestDirTrustStore_PersistRemoveIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := NewDirTrustStore(dir)
	pub, _ := mustGenKey(t)

	if err := s.Persist(pub, "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistRemove(pub.ID); err != nil {
		t.Fatalf("PersistRemove: %v", err)
	}
	if _, ok, _ := s.Lookup(pub.ID); ok {
		t.Error("in-memory entry survived PersistRemove")
	}
	if _, err := os.Stat(filepath.Join(dir, pub.ID.Hex()+".pub")); !os.IsNotExist(err) {
		t.Errorf("file should be gone, got err=%v", err)
	}
	// Second call is a no-op.
	if err := s.PersistRemove(pub.ID); err != nil {
		t.Errorf("second PersistRemove should be no-op, got %v", err)
	}
}

func TestDirTrustStore_PersistRevokeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewDirTrustStore(dir)
	pub, _ := mustGenKey(t)
	if err := s.Persist(pub, "k"); err != nil {
		t.Fatal(err)
	}

	when := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	if err := s.PersistRevoke(pub.ID, when); err != nil {
		t.Fatalf("PersistRevoke: %v", err)
	}
	gotT, ok := s.RevokedAfter(pub.ID)
	if !ok || !gotT.Equal(when) {
		t.Errorf("RevokedAfter=%v ok=%v, want %v ok=true", gotT, ok, when)
	}

	// Reload picks up the revocation from disk.
	s2 := NewDirTrustStore(dir)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	gotT, ok = s2.RevokedAfter(pub.ID)
	if !ok || !gotT.Equal(when) {
		t.Errorf("after reload: RevokedAfter=%v ok=%v, want %v ok=true", gotT, ok, when)
	}
}

func TestDirTrustStore_PersistRevokeRejectsUnknownKey(t *testing.T) {
	s := NewDirTrustStore(t.TempDir())
	var id KeyID
	id[0] = 0xab
	err := s.PersistRevoke(id, time.Now())
	if !errors.Is(err, ErrKeyNotInStore) {
		t.Errorf("want ErrKeyNotInStore, got %v", err)
	}
}

func TestDirTrustStore_PersistClearsStaleRevoked(t *testing.T) {
	dir := t.TempDir()
	s := NewDirTrustStore(dir)
	pub, _ := mustGenKey(t)
	if err := s.Persist(pub, "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistRevoke(pub.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Re-Persist should clear the operator-set revocation (matches Add).
	if err := s.Persist(pub, "v2-fresh-trust"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.RevokedAfter(pub.ID); ok {
		t.Error("re-Persist should have cleared the operator-set revoked entry")
	}
	if _, err := os.Stat(filepath.Join(dir, pub.ID.Hex()+".revoked")); !os.IsNotExist(err) {
		t.Errorf(".revoked sidecar should be gone, got %v", err)
	}
}

func TestDirTrustStore_LoadRejectsMalformedRevoked(t *testing.T) {
	dir := t.TempDir()
	pub, _ := mustGenKey(t)
	writePubFile(t, dir, pub.ID.Hex()+".pub", pub, "k")
	if err := os.WriteFile(filepath.Join(dir, pub.ID.Hex()+".revoked"), []byte("not-a-timestamp"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewDirTrustStore(dir)
	if err := s.Load(); !errors.Is(err, ErrTrustStoreCorrupt) {
		t.Errorf("want ErrTrustStoreCorrupt on malformed .revoked, got %v", err)
	}
}

func hasSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
