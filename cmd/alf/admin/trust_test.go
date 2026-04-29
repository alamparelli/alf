package admin

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// fixedTime is the canonical "now" for tests so revoke timestamps
// are deterministic across runs.
var fixedTime = time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)

// newTestEnv constructs a TrustEnv backed by tmp dirs + buffers so a
// test can drive a full subcommand without touching real os.Std* or
// the real install layout.
func newTestEnv(t *testing.T, stdin string) (*TrustEnv, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "trust")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	env := &TrustEnv{
		TrustDir:   dir,
		Stdin:      strings.NewReader(stdin),
		Stdout:     stdout,
		Stderr:     stderr,
		IsTerminal: func() bool { return true },
		Now:        func() time.Time { return fixedTime },
	}
	return env, stdout, stderr
}

// genPubFile mints a fresh keypair and writes a minisign .pub file at
// path so the trust add path can be exercised end-to-end.
func genPubFile(t *testing.T, path, comment string) envelope.PublicKey {
	t.Helper()
	pub, _, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := envelope.EncodePublicKeyFile(pub, comment)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestTrustList_Empty(t *testing.T) {
	env, stdout, _ := newTestEnv(t, "")
	if err := Trust(*env, []string{"list"}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(stdout.String(), "No operator-managed keys") {
		t.Errorf("expected empty-store message, got: %s", stdout.String())
	}
}

func TestTrustAdd_Confirms(t *testing.T) {
	env, stdout, _ := newTestEnv(t, "yes\n")
	pubFile := filepath.Join(t.TempDir(), "key.pub")
	pub := genPubFile(t, pubFile, "operator key")

	if err := Trust(*env, []string{"add", pubFile}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(stdout.String(), pub.ID.Hex()) {
		t.Errorf("expected fingerprint in confirmation output, got: %s", stdout.String())
	}
	// Round-trip via list.
	stdout.Reset()
	if err := Trust(*env, []string{"list"}); err != nil {
		t.Fatalf("list-after-add: %v", err)
	}
	if !strings.Contains(stdout.String(), pub.ID.Hex()) {
		t.Errorf("list did not show the key: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "trusted") {
		t.Errorf("list status should be 'trusted', got: %s", stdout.String())
	}
}

func TestTrustAdd_RefusesWithoutTTY(t *testing.T) {
	env, _, _ := newTestEnv(t, "yes\n")
	env.IsTerminal = func() bool { return false }
	pubFile := filepath.Join(t.TempDir(), "key.pub")
	genPubFile(t, pubFile, "k")

	err := Trust(*env, []string{"add", pubFile})
	if !errors.Is(err, ErrNonInteractive) {
		t.Errorf("want ErrNonInteractive, got %v", err)
	}
}

func TestTrustAdd_AbortedOnNonYes(t *testing.T) {
	env, _, _ := newTestEnv(t, "no\n")
	pubFile := filepath.Join(t.TempDir(), "key.pub")
	genPubFile(t, pubFile, "k")

	err := Trust(*env, []string{"add", pubFile})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Errorf("want aborted error, got %v", err)
	}
	// File must NOT have been persisted.
	entries, _ := os.ReadDir(env.TrustDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".pub") {
			t.Errorf("a .pub file was persisted despite abort: %s", e.Name())
		}
	}
}

func TestTrustRemove_RoundTrip(t *testing.T) {
	env, stdout, _ := newTestEnv(t, "yes\n")
	pubFile := filepath.Join(t.TempDir(), "key.pub")
	pub := genPubFile(t, pubFile, "k")

	if err := Trust(*env, []string{"add", pubFile}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	env.Stdin = strings.NewReader("yes\n")
	if err := Trust(*env, []string{"remove", pub.ID.Hex()}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !strings.Contains(stdout.String(), "Removed") {
		t.Errorf("expected Removed line, got: %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(env.TrustDir, pub.ID.Hex()+".pub")); !os.IsNotExist(err) {
		t.Errorf("pub file still present after remove, err=%v", err)
	}
}

func TestTrustRemove_RejectsUnknownFingerprint(t *testing.T) {
	env, _, _ := newTestEnv(t, "yes\n")
	err := Trust(*env, []string{"remove", "0123456789abcdef"})
	if err == nil || !strings.Contains(err.Error(), "no key with fingerprint") {
		t.Errorf("want 'no key' error, got %v", err)
	}
}

func TestTrustRemove_RejectsMalformedFingerprint(t *testing.T) {
	env, _, _ := newTestEnv(t, "yes\n")
	err := Trust(*env, []string{"remove", "not-hex"})
	if err == nil || !strings.Contains(err.Error(), "16 hex chars") {
		t.Errorf("want hex-length error, got %v", err)
	}
}

func TestTrustRevoke_DefaultsToNow(t *testing.T) {
	env, stdout, _ := newTestEnv(t, "yes\n")
	pubFile := filepath.Join(t.TempDir(), "key.pub")
	pub := genPubFile(t, pubFile, "k")
	if err := Trust(*env, []string{"add", pubFile}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	env.Stdin = strings.NewReader("yes\n")
	if err := Trust(*env, []string{"revoke", pub.ID.Hex()}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	want := fixedTime.Format(time.RFC3339)
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("expected default timestamp %s in output, got: %s", want, stdout.String())
	}
	// .revoked sidecar present.
	if _, err := os.Stat(filepath.Join(env.TrustDir, pub.ID.Hex()+".revoked")); err != nil {
		t.Errorf(".revoked sidecar missing: %v", err)
	}
}

func TestTrustRevoke_HonoursAtFlag(t *testing.T) {
	env, stdout, _ := newTestEnv(t, "yes\n")
	pubFile := filepath.Join(t.TempDir(), "key.pub")
	pub := genPubFile(t, pubFile, "k")
	if err := Trust(*env, []string{"add", pubFile}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	env.Stdin = strings.NewReader("yes\n")
	at := "2026-01-01T00:00:00Z"
	if err := Trust(*env, []string{"revoke", pub.ID.Hex(), "--at", at}); err != nil {
		t.Fatalf("revoke --at: %v", err)
	}
	if !strings.Contains(stdout.String(), at) {
		t.Errorf("expected %s in output, got: %s", at, stdout.String())
	}
}

func TestTrustRevoke_RejectsBadTimestamp(t *testing.T) {
	env, _, _ := newTestEnv(t, "yes\n")
	pubFile := filepath.Join(t.TempDir(), "key.pub")
	pub := genPubFile(t, pubFile, "k")
	if err := Trust(*env, []string{"add", pubFile}); err != nil {
		t.Fatal(err)
	}
	err := Trust(*env, []string{"revoke", pub.ID.Hex(), "--at", "not-a-time"})
	if err == nil || !strings.Contains(err.Error(), "--at") {
		t.Errorf("want --at parse error, got %v", err)
	}
}

func TestTrustList_ShowsRevokedStatus(t *testing.T) {
	env, stdout, _ := newTestEnv(t, "yes\n")
	pubFile := filepath.Join(t.TempDir(), "key.pub")
	pub := genPubFile(t, pubFile, "k")
	if err := Trust(*env, []string{"add", pubFile}); err != nil {
		t.Fatal(err)
	}
	env.Stdin = strings.NewReader("yes\n")
	if err := Trust(*env, []string{"revoke", pub.ID.Hex()}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := Trust(*env, []string{"list"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "revoked@") {
		t.Errorf("list should show revoked status, got: %s", stdout.String())
	}
}

func TestTrust_UnknownSubcommand(t *testing.T) {
	env, _, stderr := newTestEnv(t, "")
	err := Trust(*env, []string{"frobnicate"})
	if err == nil {
		t.Fatal("want error on unknown subcommand")
	}
	if !strings.Contains(stderr.String(), "Unknown trust subcommand") {
		t.Errorf("expected usage banner in stderr, got: %s", stderr.String())
	}
}

func TestTrust_HelpExitsZero(t *testing.T) {
	env, stdout, _ := newTestEnv(t, "")
	if err := Trust(*env, []string{"help"}); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: alf trust") {
		t.Errorf("expected usage banner, got: %s", stdout.String())
	}
}

func TestParseFingerprint_AcceptsPrefix(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"0123456789ABCDEF", "0123456789ABCDEF"},
		{"0x0123456789abcdef", "0123456789ABCDEF"},
		{" 0X0123456789AbCdEf ", "0123456789ABCDEF"},
	}
	for _, tc := range tests {
		got, err := parseFingerprint(tc.in)
		if err != nil {
			t.Errorf("parseFingerprint(%q): %v", tc.in, err)
			continue
		}
		if got.Hex() != tc.want {
			t.Errorf("parseFingerprint(%q) = %s, want %s", tc.in, got.Hex(), tc.want)
		}
	}
}
