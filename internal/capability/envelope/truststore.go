package envelope

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TrustStore maps a KeyID to the public key authorised to verify
// signatures with that ID. The verify pipeline (step 5) looks up the
// signer BEFORE any cryptographic work — a key absent from the store
// is rejected as errSignerNotTrusted regardless of how well-formed its
// signature is.
//
// Revocation (CRL: key/signature time-bounded rejection) is NOT in
// this interface — it lands with #396 and wraps TrustStore without
// changing the core contract.
type TrustStore interface {
	// Lookup returns the PublicKey for id iff it is in the store. The
	// boolean is true on hit, false on miss; err is reserved for I/O
	// failures (file-backed stores).
	Lookup(id KeyID) (PublicKey, bool, error)

	// Keys returns every trusted KeyID. Used by admin CLI / status
	// surfaces, not by the verify hot path.
	Keys() []KeyID
}

// ErrTrustStoreCorrupt is returned by file-backed stores when a pubkey
// file exists but cannot be parsed. Distinct from "key not found" so
// operators can surface the correct remediation.
var ErrTrustStoreCorrupt = errors.New("truststore: pubkey file unreadable")

// Revoker is the optional interface a TrustStore implements to
// support time-bound key revocation per #396 deliverable 4. The
// envelope.Verify pipeline does a runtime type assertion on this
// interface; a TrustStore that does not satisfy it simply has no
// revocation surface (every verified bundle passes the time check).
//
// Semantics: RevokedAfter(id) returns the timestamp T such that any
// bundle with SignedAt >= T must be rejected, even if the key id is
// still in the store. T-zero / ok=false means "no revocation
// recorded" — the caller treats every signed-at as valid.
//
// The check is "signed-at strictly before T accepts" — equality
// rejects. This matches the operator mental model "key compromised
// at T; nothing signed at or after T is trustworthy".
type Revoker interface {
	RevokedAfter(id KeyID) (time.Time, bool)
}

// ErrSignerKeyRevoked is returned by Verify when the signer's key
// has a recorded not-valid-after timestamp and the bundle's signed-at
// is at or beyond it. Distinct from ErrSignerNotTrusted because the
// key IS in the store — operators may want different remediation
// flows ("compromised — replace bundle" vs "untrusted — install key").
var ErrSignerKeyRevoked = errors.New("envelope: signer key revoked at or before signed-at")

// MemoryTrustStore is a trivial in-memory store. Used by tests and by
// the daemon when bootstrapping with a single embedded key (tier 1,
// the release-signed daemon binary per §7.3).
type MemoryTrustStore struct {
	mu        sync.RWMutex
	keys      map[KeyID]PublicKey
	revokedAt map[KeyID]time.Time
}

// NewMemoryTrustStore constructs an empty in-memory store. Keys are
// added via Add(). Concurrency-safe.
func NewMemoryTrustStore() *MemoryTrustStore {
	return &MemoryTrustStore{
		keys:      make(map[KeyID]PublicKey),
		revokedAt: make(map[KeyID]time.Time),
	}
}

// Add registers a PublicKey. Overwrites any previous entry for the
// same ID — caller decides when that's legitimate (e.g., key rotation)
// and when it's an attempted replacement. The admin CLI (#395) gates
// this operation behind TTY/CC-session confirmation.
//
// Add CLEARS any previous revocation timestamp for the same KeyID. A
// fresh Add() = "operator decided this key is valid again from now"
// (e.g., a forensic re-trust after compromise was contained). The
// CLI must surface this in the confirmation prompt.
func (m *MemoryTrustStore) Add(pub PublicKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[pub.ID] = pub
	delete(m.revokedAt, pub.ID)
}

// Remove drops the entry for id. No-op if absent. Also clears any
// stored revocation timestamp — Removed keys cannot be queried, so
// the timestamp would dangle.
func (m *MemoryTrustStore) Remove(id KeyID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, id)
	delete(m.revokedAt, id)
}

// Revoke records a not-valid-after timestamp for id. Bundles signed
// at or after the timestamp will be rejected by envelope.Verify even
// if the key is still in the store. Calling Revoke a second time
// overwrites the previous timestamp — operators can shift the
// boundary (e.g. "compromise actually started earlier than I thought").
//
// Revoking an unknown key is a no-op (avoids the half-state where
// a revocation outlives the key it referred to). The Instantiator-
// side RevokeByKey (commit 2) is the natural pairing: the typical
// admin flow is "store.Revoke(fp, now); inst.RevokeByKey(fp)".
func (m *MemoryTrustStore) Revoke(id KeyID, notValidAfter time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.keys[id]; !ok {
		return
	}
	m.revokedAt[id] = notValidAfter
}

// RevokedAfter implements Revoker.
func (m *MemoryTrustStore) RevokedAfter(id KeyID) (time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.revokedAt[id]
	return t, ok
}

// Lookup implements TrustStore.
func (m *MemoryTrustStore) Lookup(id KeyID) (PublicKey, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pub, ok := m.keys[id]
	return pub, ok, nil
}

// Keys implements TrustStore.
func (m *MemoryTrustStore) Keys() []KeyID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]KeyID, 0, len(m.keys))
	for id := range m.keys {
		out = append(out, id)
	}
	return out
}

// DirTrustStore reads minisign-format .pub files from a directory,
// one key per file. The directory is snapshot at Load() time — the
// daemon reloads on SIGHUP / CC action, not on every Lookup, so the
// hot path stays lock-free after initial population.
//
// Each pubkey file's content is parsed via ParsePublicKeyFile. A
// malformed file fails Load() loudly — never silently skipped — so
// operators notice corruption before a verification attempt silently
// succeeds because a revoked key's file was truncated to an empty one.
type DirTrustStore struct {
	*MemoryTrustStore
	dir string
}

// NewDirTrustStore returns an empty store bound to dir. Call Load()
// to populate it from the filesystem. Kept as a two-step dance so a
// test can construct the store and inspect the directory independently.
func NewDirTrustStore(dir string) *DirTrustStore {
	return &DirTrustStore{
		MemoryTrustStore: NewMemoryTrustStore(),
		dir:              dir,
	}
}

// Load reads every .pub file under the store's directory and populates
// the in-memory cache. A missing directory is treated as "no trusted
// keys" (empty store); this is the correct posture for first boot.
// A directory that exists but is unreadable, or a .pub file that fails
// to parse, is a hard error.
func (d *DirTrustStore) Load() error {
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("truststore: read dir %s: %w", d.dir, err)
	}

	// Build a fresh map then swap it in, so a failed reload doesn't
	// leave the store half-populated.
	fresh := make(map[KeyID]PublicKey)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pub") {
			continue
		}
		path := filepath.Join(d.dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrTrustStoreCorrupt, path, err)
		}
		pub, err := ParsePublicKeyFile(raw)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrTrustStoreCorrupt, path, err)
		}
		if existing, collision := fresh[pub.ID]; collision {
			return fmt.Errorf("%w: key ID %s appears twice (existing pubkey size=%d, new %s)",
				ErrTrustStoreCorrupt, pub.ID.Hex(), len(existing.Key), path)
		}
		fresh[pub.ID] = pub
	}

	d.mu.Lock()
	d.keys = fresh
	d.mu.Unlock()
	return nil
}
