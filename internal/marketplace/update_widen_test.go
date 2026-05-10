package marketplace

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// signedBundleWithPermissions builds a fixture marketplace bundle
// signed by a key in the returned trust store, with the given perms
// declared in manifest.json. Mirrors signedMarketplaceBundle but
// lets the caller drive permissions for the diff path.
func signedBundleWithPermissions(t *testing.T, slug string, perms []string) (bundle, sig []byte, store *envelope.MemoryTrustStore) {
	t.Helper()

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store = envelope.NewMemoryTrustStore()
	store.Add(pub)

	// #420: marketplace-app retired, use wasm-app for new fixtures. The
	// update/widen tests are kind-agnostic — they exercise the diff +
	// ratifier flow, not the kind admission gate.
	manifestTOML := []byte(fmt.Sprintf(
		"alf_envelope_version = 1\n"+
			"id      = %q\n"+
			"kind    = \"wasm-app\"\n"+
			"version = \"0.1.0\"\n"+
			"name    = %q\n",
		slug, slug,
	))

	manifestJSON, err := json.Marshal(Manifest{
		Slug:        slug,
		Name:        slug,
		Version:     "0.1.0",
		Permissions: perms,
	})
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	tomlW, _ := zw.Create("manifest.toml")
	tomlW.Write(manifestTOML)
	jsonW, _ := zw.Create("manifest.json")
	jsonW.Write(manifestJSON)
	zw.Close()
	bundle = buf.Bytes()

	canonical, err := envelope.Canonicalize(manifestTOML)
	if err != nil {
		t.Fatal(err)
	}
	sigBlob, err := envelope.Sign(priv, canonical)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(bundle)
	tc := envelope.TrustedComment{
		BundleID:   slug,
		SignedAt:   time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC),
		BundleHash: hex.EncodeToString(hash[:]),
	}
	sig, err = envelope.EncodeSignatureFile(priv, sigBlob, envelope.BuildTrustedComment(tc))
	if err != nil {
		t.Fatal(err)
	}
	return bundle, sig, store
}

// installAppWithPermsForUpdate sets up a Manager with one app
// already installed at version 0.1.0 carrying `oldPerms` declared
// in its on-disk manifest.json. Returns the manager so the test
// can call Update against a server serving the new bundle.
func installAppWithPermsForUpdate(t *testing.T, slug string, oldPerms []string) (*Manager, string) {
	t.Helper()

	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "tools"), 0o755)

	// Seed the on-disk app dir as if v0.7.x had installed it: just a
	// manifest.json. activate() / deactivate() consume that file.
	appDir := filepath.Join(base, "apps", slug)
	os.MkdirAll(appDir, 0o755)
	old := Manifest{Slug: slug, Name: slug, Version: "0.1.0", Permissions: oldPerms}
	oldJSON, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(appDir, "manifest.json"), oldJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(base)
	m.mu.Lock()
	m.states[slug] = StateInstalled
	m.mu.Unlock()
	t.Cleanup(func() { m.unlockAppFiles(slug) })
	return m, base
}

// TestUpdate_NarrowingProceedsSilently pins #402's narrowing rule:
// an Update where the new manifest declares fewer permissions than
// the cached install is allowed to proceed without ratification —
// no callback invoked, no error, app updates normally.
func TestUpdate_NarrowingProceedsSilently(t *testing.T) {
	bundle, sig, store := signedBundleWithPermissions(t, "narrowapp", []string{"storage"})
	srv := serveSignedBundle(t, "narrowapp", bundle, sig)
	defer srv.Close()

	m, _ := installAppWithPermsForUpdate(t, "narrowapp", []string{"storage", "bash", "network"})
	m.registryURL = srv.URL
	m.SetTrustStore(store)

	// Ratifier wired but should NOT be called for narrowing.
	called := false
	m.SetPermissionRatifier(func(slug string, oldP, newP, added []string) (string, error) {
		called = true
		return "", nil
	})

	if err := m.Update("narrowapp"); err != nil {
		t.Fatalf("Update narrow: %v", err)
	}
	if called {
		t.Error("ratifier called on narrowing path; #402 says narrowing is silent")
	}
}

// TestUpdate_WideningWithRatifierEnqueues pins the happy widening
// path: the new manifest adds a perm, the daemon-supplied ratifier
// receives the diff and returns a queue ID, Update returns
// ErrPermissionWideningPending with the ID embedded in the message.
//
// Critical assertion (#402 acceptance): m.perms[slug] must NOT
// reflect the widened set. The on-disk app dir must be untouched
// — the previous version keeps running until ratification.
func TestUpdate_WideningWithRatifierEnqueues(t *testing.T) {
	newPerms := []string{"storage", "bash", "network"}
	bundle, sig, store := signedBundleWithPermissions(t, "wideapp", newPerms)
	srv := serveSignedBundle(t, "wideapp", bundle, sig)
	defer srv.Close()

	oldPerms := []string{"storage"}
	m, base := installAppWithPermsForUpdate(t, "wideapp", oldPerms)
	m.registryURL = srv.URL
	m.SetTrustStore(store)

	var seenSlug string
	var seenAdded []string
	m.SetPermissionRatifier(func(slug string, oldP, newP, added []string) (string, error) {
		seenSlug = slug
		seenAdded = added
		return "0000000000042", nil
	})

	err := m.Update("wideapp")
	if err == nil {
		t.Fatal("Update widening returned nil; want ErrPermissionWideningPending")
	}
	if !errors.Is(err, ErrPermissionWideningPending) {
		t.Errorf("got err %v, want ErrPermissionWideningPending", err)
	}
	if seenSlug != "wideapp" {
		t.Errorf("ratifier slug=%q, want wideapp", seenSlug)
	}
	wantAdded := []string{"bash", "network"}
	if len(seenAdded) != 2 || seenAdded[0] != wantAdded[0] || seenAdded[1] != wantAdded[1] {
		t.Errorf("ratifier added=%v, want %v", seenAdded, wantAdded)
	}

	// Critical: m.perms[slug] must NOT reflect the widened state.
	m.mu.Lock()
	cachedPerms := m.perms["wideapp"]
	m.mu.Unlock()
	for _, p := range []string{"bash", "network"} {
		for _, c := range cachedPerms {
			if c == p {
				t.Errorf("widened perm %q reached m.perms[wideapp] = %v", p, cachedPerms)
			}
		}
	}

	// Equally critical: the on-disk manifest.json still reflects the
	// OLD perms — the bundle was not extracted.
	onDisk := filepath.Join(base, "apps", "wideapp", "manifest.json")
	raw, rerr := os.ReadFile(onDisk)
	if rerr != nil {
		t.Fatalf("read on-disk manifest: %v", rerr)
	}
	var onDiskManifest Manifest
	if jerr := json.Unmarshal(raw, &onDiskManifest); jerr != nil {
		t.Fatalf("parse on-disk manifest: %v", jerr)
	}
	for _, want := range []string{"bash", "network"} {
		for _, got := range onDiskManifest.Permissions {
			if got == want {
				t.Errorf("widened perm %q reached on-disk manifest before ratification", want)
			}
		}
	}
}

// TestUpdate_WideningWithoutRatifierIsRefused pins the "no fallback
// to silent widening" rule: a Manager that hasn't had
// SetPermissionRatifier called refuses any widening Update with
// ErrPermissionWideningRefused.
func TestUpdate_WideningWithoutRatifierIsRefused(t *testing.T) {
	bundle, sig, store := signedBundleWithPermissions(t, "loneapp", []string{"storage", "bash"})
	srv := serveSignedBundle(t, "loneapp", bundle, sig)
	defer srv.Close()

	m, _ := installAppWithPermsForUpdate(t, "loneapp", []string{"storage"})
	m.registryURL = srv.URL
	m.SetTrustStore(store)
	// SetPermissionRatifier intentionally NOT called.

	err := m.Update("loneapp")
	if !errors.Is(err, ErrPermissionWideningRefused) {
		t.Errorf("got %v, want ErrPermissionWideningRefused", err)
	}
}

// TestUpdate_RatifierEnqueueErrorPropagates pins that an enqueue
// failure (disk full / pending dir broken) surfaces as a wrapped
// error to the operator — Update does NOT proceed silently.
func TestUpdate_RatifierEnqueueErrorPropagates(t *testing.T) {
	bundle, sig, store := signedBundleWithPermissions(t, "brokenq", []string{"storage", "bash"})
	srv := serveSignedBundle(t, "brokenq", bundle, sig)
	defer srv.Close()

	m, _ := installAppWithPermsForUpdate(t, "brokenq", []string{"storage"})
	m.registryURL = srv.URL
	m.SetTrustStore(store)

	queueErr := errors.New("disk full")
	m.SetPermissionRatifier(func(slug string, oldP, newP, added []string) (string, error) {
		return "", queueErr
	})

	err := m.Update("brokenq")
	if err == nil {
		t.Fatal("Update returned nil; expected wrapped enqueue error")
	}
	if !errors.Is(err, queueErr) {
		t.Errorf("got %v, want wrapped %v", err, queueErr)
	}
}

// _ unused imports guard — keeps httptest in the import list when
// helpers move around without a manual edit.
var _ = httptest.NewServer
