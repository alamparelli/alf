package tlsgen

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateSelfSigned(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")

	if err := GenerateSelfSigned(cert, key); err != nil {
		t.Fatalf("GenerateSelfSigned failed: %v", err)
	}

	// Verify files exist
	if _, err := os.Stat(cert); err != nil {
		t.Fatalf("cert.pem not created: %v", err)
	}
	if _, err := os.Stat(key); err != nil {
		t.Fatalf("key.pem not created: %v", err)
	}

	// Verify key permissions are restrictive
	info, _ := os.Stat(key)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key.pem perms = %o, want 0600", info.Mode().Perm())
	}

	// Verify it's a valid TLS pair
	if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
		t.Fatalf("invalid TLS pair: %v", err)
	}
}

func TestGenerateSelfSigned_Idempotent(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "cert.pem")
	key := filepath.Join(dir, "key.pem")

	if err := GenerateSelfSigned(cert, key); err != nil {
		t.Fatalf("first call failed: %v", err)
	}

	// Get modification time
	info1, _ := os.Stat(cert)

	// Call again — should not overwrite
	if err := GenerateSelfSigned(cert, key); err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	info2, _ := os.Stat(cert)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Error("cert was overwritten on second call")
	}
}
