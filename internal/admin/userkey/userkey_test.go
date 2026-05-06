package userkey

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// canonicalFixture is a stable byte sequence the test suite signs.
// It plays the role of a real canonical manifest in the integration
// path: alf sign canonicalises the bundle's manifest.toml and feeds
// the result here. We don't need TOML — what matters for the
// crypto-level tests is "any non-empty bytes round-trip cleanly".
var canonicalFixture = []byte(`{"alf_envelope_version":1,"id":"unit-test","kind":"skill","name":"x","version":"1"}`)

// validPassphrase is long enough to clear MinPassphraseBytes (12).
const validPassphrase = "correct-horse-battery-staple"

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return &Store{Path: filepath.Join(dir, "keys", "user-endorsed.json")}
}

// TestGenerate_Roundtrip pins the happy path: Generate writes a
// record, LoadPublic returns the same fingerprint, Sign produces a
// signature that envelope.VerifySignature accepts.
func TestGenerate_Roundtrip(t *testing.T) {
	s := newStore(t)
	pub, err := s.Generate([]byte(validPassphrase))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !s.Exists() {
		t.Fatalf("Exists() = false after Generate")
	}

	pubLoaded, err := s.LoadPublic()
	if err != nil {
		t.Fatalf("LoadPublic: %v", err)
	}
	if pubLoaded.ID != pub.ID {
		t.Fatalf("LoadPublic key ID: got %x, want %x", pubLoaded.ID, pub.ID)
	}
	if !bytes.Equal(pubLoaded.Key, pub.Key) {
		t.Fatalf("LoadPublic pub bytes: mismatch")
	}

	sig, signerID, err := s.Sign([]byte(validPassphrase), canonicalFixture)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if signerID != pub.ID {
		t.Fatalf("Sign signerID: got %x, want %x", signerID, pub.ID)
	}
	if err := envelope.VerifySignature(pub, canonicalFixture, sig); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
}

func TestGenerate_RejectsExisting(t *testing.T) {
	s := newStore(t)
	if _, err := s.Generate([]byte(validPassphrase)); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	_, err := s.Generate([]byte(validPassphrase))
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second Generate: got %v, want ErrAlreadyExists", err)
	}
}

func TestGenerate_RejectsShortPassphrase(t *testing.T) {
	s := newStore(t)
	short := strings.Repeat("a", MinPassphraseBytes-1)
	_, err := s.Generate([]byte(short))
	if !errors.Is(err, ErrPassphraseTooShort) {
		t.Fatalf("got %v, want ErrPassphraseTooShort", err)
	}
	if s.Exists() {
		t.Fatalf("file should not be created when passphrase rejected")
	}
}

func TestSign_WrongPassphrase(t *testing.T) {
	s := newStore(t)
	if _, err := s.Generate([]byte(validPassphrase)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	_, _, err := s.Sign([]byte("not-the-passphrase-12chars"), canonicalFixture)
	if !errors.Is(err, ErrPassphrase) {
		t.Fatalf("got %v, want ErrPassphrase", err)
	}
}

// TestSign_TamperedAADRejected pins the AEAD binding: if an attacker
// swaps the public key field in the JSON record (hoping the operator's
// passphrase still decrypts the ciphertext somewhere), the AEAD AAD
// no longer matches and Open returns an error. We surface that as
// ErrPassphrase by design — the operator can't tell whether it's
// tamper or typo, which is the right UX for offline attack pressure.
func TestSign_TamperedAADRejected(t *testing.T) {
	s := newStore(t)
	if _, err := s.Generate([]byte(validPassphrase)); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Read, swap the pub_hex to a different valid pubkey, write back.
	rec, err := readRecord(s.Path)
	if err != nil {
		t.Fatalf("readRecord: %v", err)
	}
	otherPub, _, err := envelope.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	rec.PubHex = hexEncode(otherPub.Key)
	if err := persistRecord(s.Path, rec); err != nil {
		t.Fatalf("persistRecord: %v", err)
	}

	_, _, err = s.Sign([]byte(validPassphrase), canonicalFixture)
	if !errors.Is(err, ErrPassphrase) {
		t.Fatalf("got %v, want ErrPassphrase (AAD mismatch)", err)
	}
}

func TestSign_CorruptCiphertextSurface(t *testing.T) {
	s := newStore(t)
	if _, err := s.Generate([]byte(validPassphrase)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	rec, err := readRecord(s.Path)
	if err != nil {
		t.Fatalf("readRecord: %v", err)
	}
	rec.CiphertextB64 = "!!!not base64!!!"
	if err := persistRecord(s.Path, rec); err != nil {
		t.Fatalf("persistRecord: %v", err)
	}
	_, _, err = s.Sign([]byte(validPassphrase), canonicalFixture)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("got %v, want ErrCorrupt", err)
	}
}

func TestRead_RejectsPermissiveFile(t *testing.T) {
	s := newStore(t)
	if _, err := s.Generate([]byte(validPassphrase)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Loosen perms; readRecord must refuse.
	if err := os.Chmod(s.Path, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	_, err := s.LoadPublic()
	if err == nil || !strings.Contains(err.Error(), "permissive perms") {
		t.Fatalf("expected permissive-perms refusal, got: %v", err)
	}
}

func TestRead_RejectsUnknownSchemaVersion(t *testing.T) {
	s := newStore(t)
	if _, err := s.Generate([]byte(validPassphrase)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	rec, err := readRecord(s.Path)
	if err != nil {
		t.Fatalf("readRecord: %v", err)
	}
	rec.Version = 99
	if err := persistRecord(s.Path, rec); err != nil {
		t.Fatalf("persistRecord: %v", err)
	}
	_, err = s.LoadPublic()
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("got %v, want ErrUnsupportedSchema", err)
	}
}

func TestRead_RejectsUnknownKDF(t *testing.T) {
	s := newStore(t)
	if _, err := s.Generate([]byte(validPassphrase)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	rec, err := readRecord(s.Path)
	if err != nil {
		t.Fatalf("readRecord: %v", err)
	}
	rec.KDF = "scrypt"
	if err := persistRecord(s.Path, rec); err != nil {
		t.Fatalf("persistRecord: %v", err)
	}
	_, err = s.LoadPublic()
	if !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("got %v, want ErrUnsupportedSchema", err)
	}
}

func TestLoadPublic_NoFile(t *testing.T) {
	s := newStore(t)
	_, err := s.LoadPublic()
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestRemove_Idempotent(t *testing.T) {
	s := newStore(t)
	if _, err := s.Generate([]byte(validPassphrase)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := s.Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if s.Exists() {
		t.Fatalf("Exists() = true after Remove")
	}
	// Second Remove on a missing file must not error.
	if err := s.Remove(); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

// TestPersistRecord_AtomicAndPerms pins the disk hygiene: the parent
// dir is 0o700, the file is 0o600, and a partial write doesn't leave
// the target replaced (we trigger this indirectly: a successful
// Generate followed by a Stat).
func TestPersistRecord_AtomicAndPerms(t *testing.T) {
	s := newStore(t)
	if _, err := s.Generate([]byte(validPassphrase)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	finfo, err := os.Stat(s.Path)
	if err != nil {
		t.Fatalf("Stat file: %v", err)
	}
	if mode := finfo.Mode().Perm(); mode != 0o600 {
		t.Errorf("file perms: got %v, want 0600", mode)
	}
	dinfo, err := os.Stat(filepath.Dir(s.Path))
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if mode := dinfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("dir perms: got %v, want 0700", mode)
	}
}

// TestDefaultPath pins the layout convention: <dataDir>/keys/user-endorsed.json
// — sibling to the daemon key, so a single keys/ backup captures
// both Tier-2 and Tier-3 material.
func TestDefaultPath(t *testing.T) {
	got := DefaultPath("/var/lib/alf/data")
	want := "/var/lib/alf/data/keys/user-endorsed.json"
	if got != want {
		t.Errorf("DefaultPath: got %q, want %q", got, want)
	}
}

func hexEncode(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, x := range b {
		out[i*2] = hex[x>>4]
		out[i*2+1] = hex[x&0x0f]
	}
	return string(out)
}
