package envelope

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// MemoryTrustStore is a trivial in-memory store. Used by tests and by
// the daemon when bootstrapping with a single embedded key (tier 1,
// the release-signed daemon binary per §7.3).
type MemoryTrustStore struct {
	mu   sync.RWMutex
	keys map[KeyID]PublicKey
}

// NewMemoryTrustStore constructs an empty in-memory store. Keys are
// added via Add(). Concurrency-safe.
func NewMemoryTrustStore() *MemoryTrustStore {
	return &MemoryTrustStore{keys: make(map[KeyID]PublicKey)}
}

// Add registers a PublicKey. Overwrites any previous entry for the
// same ID — caller decides when that's legitimate (e.g., key rotation)
// and when it's an attempted replacement. The admin CLI (#395) gates
// this operation behind TTY/CC-session confirmation.
func (m *MemoryTrustStore) Add(pub PublicKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[pub.ID] = pub
}

// Remove drops the entry for id. No-op if absent.
func (m *MemoryTrustStore) Remove(id KeyID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, id)
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
