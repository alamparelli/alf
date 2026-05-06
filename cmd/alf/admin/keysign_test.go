package admin

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/admin/userkey"
	"github.com/alamparelli/alf/internal/capability/envelope"
)

// adminTestEnv constructs an Env wired against tmp dirs and a
// scriptable passphrase reader so keygen / sign tests run without
// touching real os.Std* or a real TTY.
type adminTestEnv struct {
	*Env
	passphrases [][]byte // sequenced; each ReadPassword returns one
}

func newAdminTestEnv(t *testing.T, stdin string, passphrases ...string) (*adminTestEnv, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	pp := make([][]byte, len(passphrases))
	for i, p := range passphrases {
		pp[i] = []byte(p)
	}
	holder := &adminTestEnv{passphrases: pp}

	env := &Env{
		TrustDir:    filepath.Join(dir, "trust"),
		UserKeyPath: filepath.Join(dir, "keys", "user-endorsed.json"),
		Stdin:       strings.NewReader(stdin),
		Stdout:      stdout,
		Stderr:      stderr,
		IsTerminal:  func() bool { return true },
		Now:         func() time.Time { return time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC) },
		ReadPassword: func(prompt string) ([]byte, error) {
			if len(holder.passphrases) == 0 {
				return nil, errors.New("test: ReadPassword called more times than scripted")
			}
			out := holder.passphrases[0]
			holder.passphrases = holder.passphrases[1:]
			return out, nil
		},
	}
	holder.Env = env
	return holder, stdout, stderr
}

const goodPassphrase = "correct-horse-battery-staple"

func TestKeygen_HappyPath(t *testing.T) {
	env, stdout, _ := newAdminTestEnv(t, "", goodPassphrase, goodPassphrase)
	if err := Keygen(*env.Env, nil); err != nil {
		t.Fatalf("Keygen: %v", err)
	}
	if !strings.Contains(stdout.String(), "Fingerprint:") {
		t.Errorf("expected fingerprint in stdout, got: %s", stdout.String())
	}
	if _, err := os.Stat(env.UserKeyPath); err != nil {
		t.Errorf("user-endorsed.json not created: %v", err)
	}
}

func TestKeygen_RefusesNonTTY(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "", goodPassphrase, goodPassphrase)
	env.IsTerminal = func() bool { return false }
	err := Keygen(*env.Env, nil)
	if !errors.Is(err, ErrNonInteractive) {
		t.Fatalf("got %v, want ErrNonInteractive", err)
	}
}

func TestKeygen_RefusesExistingWithoutForce(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "", goodPassphrase, goodPassphrase)
	if err := Keygen(*env.Env, nil); err != nil {
		t.Fatalf("first Keygen: %v", err)
	}

	env2, _, _ := newAdminTestEnv(t, "", goodPassphrase, goodPassphrase)
	env2.UserKeyPath = env.UserKeyPath // reuse the existing file path
	err := Keygen(*env2.Env, nil)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("got %v, want already-exists error", err)
	}
}

func TestKeygen_OverwriteWithForce(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "", goodPassphrase, goodPassphrase)
	if err := Keygen(*env.Env, nil); err != nil {
		t.Fatalf("first Keygen: %v", err)
	}

	env2, stdout2, _ := newAdminTestEnv(t, "yes\n", "different-pass-pass-12", "different-pass-pass-12")
	env2.UserKeyPath = env.UserKeyPath
	if err := Keygen(*env2.Env, []string{"--force"}); err != nil {
		t.Fatalf("Keygen --force: %v", err)
	}
	if !strings.Contains(stdout2.String(), "Fingerprint:") {
		t.Errorf("expected fingerprint after force-regenerate, got: %s", stdout2.String())
	}
}

func TestKeygen_PassphraseMismatchRejected(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "", goodPassphrase, "different-pass-pass-12")
	err := Keygen(*env.Env, nil)
	if err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Fatalf("got %v, want passphrase-mismatch error", err)
	}
	if _, err := os.Stat(env.UserKeyPath); err == nil {
		t.Errorf("user-endorsed.json should not be created on mismatch")
	}
}

func TestKeygen_ShortPassphraseRejected(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "", "short", "short")
	err := Keygen(*env.Env, nil)
	if err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("got %v, want too-short error", err)
	}
}

func TestKeygen_ExportPub(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "", goodPassphrase, goodPassphrase)
	pubFile := filepath.Join(t.TempDir(), "user.pub")
	if err := Keygen(*env.Env, []string{"--export-pub", pubFile, "--comment", "test"}); err != nil {
		t.Fatalf("Keygen: %v", err)
	}
	raw, err := os.ReadFile(pubFile)
	if err != nil {
		t.Fatalf("read pub: %v", err)
	}
	pub, err := envelope.ParsePublicKeyFile(raw)
	if err != nil {
		t.Fatalf("parse pub: %v", err)
	}
	// Cross-check: the exported pub matches the in-store pub.
	store := &userkey.Store{Path: env.UserKeyPath}
	stored, err := store.LoadPublic()
	if err != nil {
		t.Fatalf("LoadPublic: %v", err)
	}
	if pub.ID != stored.ID {
		t.Errorf("exported pub ID %x != stored %x", pub.ID, stored.ID)
	}
}

// TestSign_RoundTripVerifies pins the integration: sign a real skill
// manifest with the user-endorsed key, then run envelope.Verify
// against a trust store containing the user-endorsed pub. End-to-end
// proof that the produced manifest.sig is well-formed and accepted
// by the same code path the daemon runs at load time.
func TestSign_RoundTripVerifies(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "",
		goodPassphrase, goodPassphrase, // keygen
		goodPassphrase, // sign
	)
	if err := Keygen(*env.Env, nil); err != nil {
		t.Fatalf("Keygen: %v", err)
	}

	// Build a minimal skill bundle: just manifest.toml.
	bundleDir := t.TempDir()
	manifestBytes := []byte(`alf_envelope_version = 1
id          = "test-skill"
kind        = "skill"
version     = "1"
name        = "Test"
description = "test fixture"
`)
	if err := os.WriteFile(filepath.Join(bundleDir, "manifest.toml"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Sign(*env.Env, []string{bundleDir}); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	sigBytes, err := os.ReadFile(filepath.Join(bundleDir, "manifest.sig"))
	if err != nil {
		t.Fatalf("read manifest.sig: %v", err)
	}

	store := &userkey.Store{Path: env.UserKeyPath}
	pub, err := store.LoadPublic()
	if err != nil {
		t.Fatalf("LoadPublic: %v", err)
	}

	// Verify against the underlying primitives directly. The
	// archtest TestOneVerifyCallSite reserves envelope.Verify for the
	// single runtime consumer; CLI-produced signatures still get the
	// same crypto check via the lower-level pipeline pieces (parse +
	// canonicalize + main sig + global sig) — that's exactly what
	// envelope.Verify chains internally.
	if err := verifyCLISignature(t, manifestBytes, sigBytes, pub); err != nil {
		t.Fatalf("verifyCLISignature: %v", err)
	}
}

func TestSign_RoundTripWithWASMArtefact(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "",
		goodPassphrase, goodPassphrase,
		goodPassphrase,
	)
	if err := Keygen(*env.Env, nil); err != nil {
		t.Fatalf("Keygen: %v", err)
	}

	bundleDir := t.TempDir()
	manifestBytes := []byte(`alf_envelope_version = 1
id      = "hello-read"
kind    = "wasm-tool"
version = "0.1.0"
name    = "Hello"
description = "test fixture"
`)
	if err := os.WriteFile(filepath.Join(bundleDir, "manifest.toml"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	wasmBytes := []byte("\x00asm\x01\x00\x00\x00") // minimal wasm header — real bytes don't matter for the signing test
	if err := os.WriteFile(filepath.Join(bundleDir, "tool.wasm"), wasmBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Sign(*env.Env, []string{bundleDir}); err != nil {
		t.Fatalf("Sign: %v", err)
	}

	sigBytes, err := os.ReadFile(filepath.Join(bundleDir, "manifest.sig"))
	if err != nil {
		t.Fatalf("read manifest.sig: %v", err)
	}
	store := &userkey.Store{Path: env.UserKeyPath}
	pub, _ := store.LoadPublic()

	if err := verifyCLISignature(t, manifestBytes, sigBytes, pub); err != nil {
		t.Fatalf("verifyCLISignature: %v", err)
	}
	// Verify the bundle hash field landed in the trusted comment, so a
	// downstream loader's bundle cross-check would succeed.
	_, trustedComment, _, _ := envelope.ParseSignatureFile(sigBytes)
	tc, err := envelope.ParseTrustedComment(trustedComment)
	if err != nil {
		t.Fatalf("ParseTrustedComment: %v", err)
	}
	if tc.BundleHash == "" {
		t.Errorf("BundleHash empty in trusted comment for wasm-tool bundle")
	}
}

func TestSign_RefusesNonTTY(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "")
	env.IsTerminal = func() bool { return false }
	bundleDir := makeMinimalSkillBundle(t)
	err := Sign(*env.Env, []string{bundleDir})
	if !errors.Is(err, ErrNonInteractive) {
		t.Fatalf("got %v, want ErrNonInteractive", err)
	}
}

func TestSign_NoKey(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "")
	bundleDir := makeMinimalSkillBundle(t)
	err := Sign(*env.Env, []string{bundleDir})
	if err == nil || !strings.Contains(err.Error(), "no user-endorsed key") {
		t.Fatalf("got %v, want no-key error", err)
	}
}

func TestSign_WrongPassphrase(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "",
		goodPassphrase, goodPassphrase,
		"wrong-pass-pass-pass",
	)
	if err := Keygen(*env.Env, nil); err != nil {
		t.Fatalf("Keygen: %v", err)
	}
	bundleDir := makeMinimalSkillBundle(t)
	err := Sign(*env.Env, []string{bundleDir})
	if !errors.Is(err, userkey.ErrPassphrase) {
		t.Fatalf("got %v, want ErrPassphrase", err)
	}
}

func TestSign_MissingManifest(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "")
	dir := t.TempDir()
	err := Sign(*env.Env, []string{dir})
	if err == nil || !strings.Contains(err.Error(), "manifest.toml") {
		t.Fatalf("got %v, want manifest-missing error", err)
	}
}

func TestSign_AmbiguousWASM(t *testing.T) {
	env, _, _ := newAdminTestEnv(t, "",
		goodPassphrase, goodPassphrase,
		goodPassphrase,
	)
	if err := Keygen(*env.Env, nil); err != nil {
		t.Fatalf("Keygen: %v", err)
	}
	bundleDir := t.TempDir()
	manifestBytes := []byte(`alf_envelope_version = 1
id = "x"
kind = "wasm-tool"
version = "1"
name = "X"
description = "x"
`)
	_ = os.WriteFile(filepath.Join(bundleDir, "manifest.toml"), manifestBytes, 0o644)
	_ = os.WriteFile(filepath.Join(bundleDir, "a.wasm"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(bundleDir, "b.wasm"), []byte("b"), 0o644)

	err := Sign(*env.Env, []string{bundleDir})
	if err == nil || !strings.Contains(err.Error(), "multiple .wasm") {
		t.Fatalf("got %v, want ambiguous-bundle error", err)
	}
}

// makeMinimalSkillBundle writes a manifest.toml fixture and returns
// the bundle dir. Used by tests that don't care about Sign succeeding.
func makeMinimalSkillBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	manifestBytes := []byte(`alf_envelope_version = 1
id = "x"
kind = "skill"
version = "1"
name = "X"
description = "x"
`)
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// verifyCLISignature parses the CLI-produced manifest.sig and runs
// the same crypto checks envelope.Verify chains internally — without
// reaching into envelope.Verify itself, which TestOneVerifyCallSite
// reserves for the single runtime consumer. We do: parse sig file,
// canonicalize manifest, verify main signature, verify global comment
// signature. Any of those failing means alf sign produced a bundle
// the daemon would reject at load time.
func verifyCLISignature(t *testing.T, manifestBytes, sigBytes []byte, pub envelope.PublicKey) error {
	t.Helper()
	sigBlob, trustedComment, globalSig, err := envelope.ParseSignatureFile(sigBytes)
	if err != nil {
		return err
	}
	canonical, err := envelope.Canonicalize(manifestBytes)
	if err != nil {
		return err
	}
	if err := envelope.VerifySignature(pub, canonical, sigBlob); err != nil {
		return err
	}
	if err := envelope.VerifyGlobalComment(pub, sigBlob, trustedComment, globalSig); err != nil {
		return err
	}
	return nil
}

// silenceWriter swallows test prompt output so the test runner stays
// quiet for the verbose round-trip tests. Not used today but kept
// here as a hook for future scenarios needing a discard writer.
var _ io.Writer = io.Discard
