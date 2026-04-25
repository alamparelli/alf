package skills

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// signSkill emits a manifest.toml + manifest.sig pair signed with a
// freshly-generated key. The returned trust store contains that key,
// so a subsequent envelope.Verify call accepts the signature. Tests
// that need the signer rejected pass an empty trust store at load time.
func signSkill(t *testing.T, dir, manifestTOML, body string) envelope.TrustStore {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte(manifestTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	signer := NewDaemonAutoSigner(priv, func() time.Time {
		return time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	})
	sig, err := signer.Sign([]byte(manifestTOML), []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.sig"), sig, 0o644); err != nil {
		t.Fatal(err)
	}
	return store
}

const validSkillManifest = `alf_envelope_version = 1
id      = "test-skill"
kind    = "skill"
version = "0.1.0"
name    = "Test Skill"
description = "A skill used in unit tests"

[[tools.declares]]
id = "read-file"
`

func TestPrepareSkillBundle_HappyPath(t *testing.T) {
	dir := t.TempDir()
	store := signSkill(t, dir, validSkillManifest, "# Test Skill\n\nDo the thing.\n")

	b, err := prepareSkillBundle(dir, loadOptions{TrustStore: store})
	if err != nil {
		t.Fatalf("prepareSkillBundle: %v", err)
	}
	if !bytes.Equal(b.VerifyInput.ManifestTOML, []byte(validSkillManifest)) {
		t.Error("ManifestTOML bytes do not match what was written to disk")
	}
	if !bytes.Equal(b.VerifyInput.Bundle, []byte("# Test Skill\n\nDo the thing.\n")) {
		t.Error("Bundle bytes do not match SKILL.md")
	}
	if len(b.VerifyInput.Signature) == 0 {
		t.Error("Signature bytes empty")
	}
	if b.VerifyInput.TrustStore != store {
		t.Error("TrustStore not threaded into VerifyInput")
	}
	if b.ManifestPath != filepath.Join(dir, "manifest.toml") {
		t.Errorf("ManifestPath=%q", b.ManifestPath)
	}
	if b.BundlePath != filepath.Join(dir, "SKILL.md") {
		t.Errorf("BundlePath=%q", b.BundlePath)
	}
}

func TestPrepareSkillBundle_MissingManifestSoftError(t *testing.T) {
	dir := t.TempDir()
	// Only SKILL.md, no manifest.toml.
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := prepareSkillBundle(dir, loadOptions{TrustStore: envelope.NewMemoryTrustStore()})
	if !errors.Is(err, errSkillManifestNotFound) {
		t.Fatalf("want errSkillManifestNotFound, got %v", err)
	}
}

func TestPrepareSkillBundle_MissingSKILLBodyRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte(validSkillManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := prepareSkillBundle(dir, loadOptions{TrustStore: envelope.NewMemoryTrustStore()})
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want os.ErrNotExist for missing SKILL.md, got %v", err)
	}
}

func TestPrepareSkillBundle_MissingSigNoAutoSignerRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte(validSkillManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := prepareSkillBundle(dir, loadOptions{TrustStore: envelope.NewMemoryTrustStore()})
	if !errors.Is(err, envelope.ErrSigFileMalformed) {
		t.Fatalf("want ErrSigFileMalformed, got %v", err)
	}
}

func TestPrepareSkillBundle_AutoSignsAndPersists(t *testing.T) {
	dir := t.TempDir()
	body := "# Auto-Sign Skill\n\nThe daemon signs me at first boot.\n"
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte(validSkillManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)
	signer := NewDaemonAutoSigner(priv, func() time.Time {
		return time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	})

	b, err := prepareSkillBundle(dir, loadOptions{
		TrustStore: store,
		AutoSign:   signer,
	})
	if err != nil {
		t.Fatalf("prepareSkillBundle: %v", err)
	}
	if len(b.VerifyInput.Signature) == 0 {
		t.Error("Signature bytes empty after auto-sign")
	}
	// Sig file should now be persisted.
	if _, err := os.Stat(filepath.Join(dir, "manifest.sig")); err != nil {
		t.Errorf("manifest.sig not persisted: %v", err)
	}
}

// End-to-end integration of the prepared VerifyInput with
// envelope.Verify lives in the runtime instantiator tests (Étape 4
// LoadDir wiring) — the #388 archtest pins envelope.Verify's only
// runtime call site, and that is honored by routing through
// runtime.Instantiator.InstantiateVerified.
