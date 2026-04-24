package envelope

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
