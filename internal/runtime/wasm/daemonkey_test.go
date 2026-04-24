package wasm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrGenerateDaemonKey_GeneratesOnFirstBoot(t *testing.T) {
	dataDir := t.TempDir()

	pub, priv, err := LoadOrGenerateDaemonKey(dataDir)
	if err != nil {
		t.Fatalf("LoadOrGenerateDaemonKey: %v", err)
	}
	if len(pub.Key) == 0 || len(priv.Key) == 0 {
		t.Fatal("key material empty")
	}

	// File must exist with 0o600.
	path := filepath.Join(dataDir, daemonKeyFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perms=%v, want 0o600", info.Mode().Perm())
	}
}

func TestLoadOrGenerateDaemonKey_PersistsAcrossCalls(t *testing.T) {
	dataDir := t.TempDir()

	pub1, priv1, err := LoadOrGenerateDaemonKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	pub2, priv2, err := LoadOrGenerateDaemonKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	if pub1.ID != pub2.ID {
		t.Error("PublicKey.ID changed between calls")
	}
	if string(pub1.Key) != string(pub2.Key) {
		t.Error("PublicKey.Key changed between calls")
	}
	if string(priv1.Key) != string(priv2.Key) {
		t.Error("PrivateKey.Key changed between calls")
	}
}

func TestLoadOrGenerateDaemonKey_RejectsPermissiveFile(t *testing.T) {
	dataDir := t.TempDir()

	if _, _, err := LoadOrGenerateDaemonKey(dataDir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, daemonKeyFile)
	// Simulate permission drift.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := LoadOrGenerateDaemonKey(dataDir)
	if err == nil {
		t.Fatal("want error for permissive daemon key file, got nil")
	}
}

func TestLoadOrGenerateDaemonKey_RejectsMalformedFile(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, daemonKeyFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := LoadOrGenerateDaemonKey(dataDir)
	if err == nil {
		t.Fatal("want error for malformed daemon key, got nil")
	}
}

func TestLoadOrGenerateDaemonKey_EmptyDataDirRejected(t *testing.T) {
	_, _, err := LoadOrGenerateDaemonKey("")
	if err == nil {
		t.Fatal("want error for empty DataDir")
	}
}
