package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// stubInstantiate is a test InstantiateFn that bypasses the verify
// pipeline (#388 archtest pins it to runtime/) and the runtime-token
// forge gate. It simulates a successful instantiate by schema-
// validating the manifest locally and constructing a no-grants
// Instance through handle.NewInstance — the un-gated constructor.
// Real production wiring routes through Instantiator.InstantiateVerified.
func stubInstantiate(_ *testing.T) (InstantiateFn, *sync.Map) {
	calls := &sync.Map{}
	return func(_ context.Context, in envelope.VerifyInput, baseDir string) (*handle.Instance, *envelope.Manifest, error) {
		m, err := envelope.Validate(in.ManifestTOML)
		if err != nil {
			return nil, nil, fmt.Errorf("stub validate: %w", err)
		}
		inst := handle.NewInstance(context.Background(), capability.ID(m.ID), handle.Grants{})
		calls.Store(baseDir, m.ID)
		return inst, m, nil
	}, calls
}

// signSkillWithKey is signSkill but with an externally-provided key
// pair. Lets a test sign multiple skills against one trust store.
func signSkillWithKey(t *testing.T, dir, manifestTOML, body string, priv envelope.PrivateKey) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "manifest.toml"), []byte(manifestTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
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
}

func writeSkillWithKey(t *testing.T, root, id, body string, priv envelope.PrivateKey) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`alf_envelope_version = 1
id      = "%s"
kind    = "skill"
version = "0.1.0"
name    = "%s"
`, id, id)
	signSkillWithKey(t, dir, manifest, body, priv)
	return dir
}

func TestLoadDir_HappyPath_TwoSkills_SharedKey(t *testing.T) {
	root := t.TempDir()
	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)
	writeSkillWithKey(t, root, "alpha", "# Alpha\n\nBody A.\n", priv)
	writeSkillWithKey(t, root, "beta", "# Beta\n\nBody B.\n", priv)

	inst, _ := stubInstantiate(t)
	loaded, errs := LoadDir(context.Background(), LoadOptions{
		Dirs:        []string{root},
		TrustStore:  store,
		Instantiate: inst,
	})
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded=%d, want 2", len(loaded))
	}
	if loaded[0].Manifest.ID != "alpha" || loaded[1].Manifest.ID != "beta" {
		t.Errorf("loaded ids=%q,%q want alpha,beta",
			loaded[0].Manifest.ID, loaded[1].Manifest.ID)
	}
	defer CloseAll(loaded)
}

func TestLoadDir_LegacyYAMLOnlySkillsSkipped(t *testing.T) {
	root := t.TempDir()
	// Drop a legacy SKILL.md without a manifest.toml.
	dir := filepath.Join(root, "legacy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: legacy\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst, _ := stubInstantiate(t)
	loaded, errs := LoadDir(context.Background(), LoadOptions{
		Dirs:        []string{root},
		TrustStore:  envelope.NewMemoryTrustStore(),
		Instantiate: inst,
	})
	if len(errs) != 0 {
		t.Errorf("legacy skill should be silently skipped, got errs=%v", errs)
	}
	if len(loaded) != 0 {
		t.Errorf("legacy skill should not surface, got %d loaded", len(loaded))
	}
}

func TestLoadDir_OverrideShadowsAndRevokes(t *testing.T) {
	shipped := t.TempDir()
	user := t.TempDir()

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	writeSkillWithKey(t, shipped, "heartbeat", "# Shipped heartbeat\n", priv)
	writeSkillWithKey(t, user, "heartbeat", "# User heartbeat\n", priv)

	var logs []string
	inst, _ := stubInstantiate(t)
	loaded, errs := LoadDir(context.Background(), LoadOptions{
		Dirs:        []string{shipped, user}, // user wins (later dir)
		TrustStore:  store,
		Instantiate: inst,
		Logger: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	})
	if len(errs) != 0 {
		t.Fatalf("errs=%v", errs)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded=%d, want 1 (override should collapse to one)", len(loaded))
	}

	// The surviving skill is the user copy: its Skill.Dir points at user.
	if !strings.HasPrefix(loaded[0].Skill.Dir, user) {
		t.Errorf("override winner Dir=%q, want under %q", loaded[0].Skill.Dir, user)
	}

	// Override log line emitted.
	overrideSeen := false
	for _, line := range logs {
		if strings.Contains(line, "overridden") && strings.Contains(line, "heartbeat") {
			overrideSeen = true
			break
		}
	}
	if !overrideSeen {
		t.Errorf("override log line not emitted; logs=%v", logs)
	}

	defer CloseAll(loaded)
}

func TestLoadDir_PerSkillFailureDoesNotAbortLoad(t *testing.T) {
	root := t.TempDir()
	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()
	store.Add(pub)

	// Good skill.
	writeSkillWithKey(t, root, "alpha", "# Alpha\n", priv)

	// Bad skill: tampered SKILL.md after signing.
	badDir := writeSkillWithKey(t, root, "broken", "# Original\n", priv)
	if err := os.WriteFile(filepath.Join(badDir, "SKILL.md"), []byte("# Tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use a real instantiate that exercises envelope.Verify so the
	// hash-mismatch surfaces — but we cannot import runtime here.
	// Instead, route through a stub that mimics InstantiateVerified by
	// failing on bundle hash. We'll simulate the check directly.
	inst, _ := stubInstantiate(t)
	wrapped := func(ctx context.Context, in envelope.VerifyInput, baseDir string) (*handle.Instance, *envelope.Manifest, error) {
		// Manual replay of the bundle-hash check to simulate what
		// envelope.Verify catches in the real runtime path.
		if in.Bundle == nil {
			return inst(ctx, in, baseDir)
		}
		// Look for "Tampered" — flagged as bad bytes for this test.
		if strings.Contains(string(in.Bundle), "Tampered") {
			return nil, nil, envelope.ErrBundleHashMismatch
		}
		return inst(ctx, in, baseDir)
	}

	loaded, errs := LoadDir(context.Background(), LoadOptions{
		Dirs:        []string{root},
		TrustStore:  store,
		Instantiate: wrapped,
	})
	if len(loaded) != 1 || loaded[0].Manifest.ID != "alpha" {
		t.Errorf("good skill should load; got %d skills", len(loaded))
	}
	if len(errs) != 1 || !errors.Is(errs[0], envelope.ErrBundleHashMismatch) {
		t.Errorf("bad skill should surface in errs; got %v", errs)
	}
	defer CloseAll(loaded)
}

func TestLoadDir_NoInstantiateRejected(t *testing.T) {
	_, errs := LoadDir(context.Background(), LoadOptions{
		Dirs:       []string{},
		TrustStore: envelope.NewMemoryTrustStore(),
	})
	if len(errs) != 1 {
		t.Fatalf("want exactly 1 error, got %v", errs)
	}
	if !strings.Contains(errs[0].Error(), "non-nil Instantiate") {
		t.Errorf("error should name the missing field, got %v", errs[0])
	}
}

func TestLoadDir_EmptyDirsHandled(t *testing.T) {
	inst, _ := stubInstantiate(t)
	loaded, errs := LoadDir(context.Background(), LoadOptions{
		Dirs:        []string{"/nonexistent/path/skills"},
		TrustStore:  envelope.NewMemoryTrustStore(),
		Instantiate: inst,
	})
	if len(errs) != 0 {
		t.Errorf("nonexistent dir should be a soft skip, got errs=%v", errs)
	}
	if len(loaded) != 0 {
		t.Errorf("loaded=%d, want 0", len(loaded))
	}
}
