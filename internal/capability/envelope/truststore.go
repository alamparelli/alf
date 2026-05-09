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
//
// Two separate revocation maps:
//   - revokedAt: operator-set via Revoke() (admin CLI / #395 path)
//   - crlRevokedAt: set by ApplyCRL() from a signed alf release CRL
//
// RevokedAfter returns the strictest (earliest) of the two — neither
// channel can soften the other. Add() clears operator-set only; the
// CRL is upstream-authoritative and can't be silenced by re-trusting
// locally. Remove() clears both (the key itself is gone).
type MemoryTrustStore struct {
	mu           sync.RWMutex
	keys         map[KeyID]PublicKey
	revokedAt    map[KeyID]time.Time
	crlRevokedAt map[KeyID]time.Time
}

// NewMemoryTrustStore constructs an empty in-memory store. Keys are
// added via Add(). Concurrency-safe.
func NewMemoryTrustStore() *MemoryTrustStore {
	return &MemoryTrustStore{
		keys:         make(map[KeyID]PublicKey),
		revokedAt:    make(map[KeyID]time.Time),
		crlRevokedAt: make(map[KeyID]time.Time),
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
	delete(m.crlRevokedAt, id)
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

// RevokedAfter implements Revoker. Returns the strictest (earliest)
// of operator-set and CRL-set timestamps. Either channel alone is
// authoritative; neither can soften the other.
func (m *MemoryTrustStore) RevokedAfter(id KeyID) (time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	op, opOK := m.revokedAt[id]
	crl, crlOK := m.crlRevokedAt[id]
	switch {
	case opOK && crlOK:
		if op.Before(crl) {
			return op, true
		}
		return crl, true
	case opOK:
		return op, true
	case crlOK:
		return crl, true
	}
	return time.Time{}, false
}

// AllRevoked snapshots every revoked key with its strictest
// not-valid-after timestamp (operator-set + CRL-set channels merged
// via the same earliest-wins rule as RevokedAfter). The returned
// map is a fresh copy — caller may retain or mutate it freely.
//
// Used by the daemon's revocation cascader to diff revoked sets
// across SIGHUP reloads and CRL refreshes; #396 deliverable 2.
func (m *MemoryTrustStore) AllRevoked() map[KeyID]time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[KeyID]time.Time, len(m.revokedAt)+len(m.crlRevokedAt))
	for k, t := range m.revokedAt {
		out[k] = t
	}
	for k, t := range m.crlRevokedAt {
		if existing, ok := out[k]; !ok || t.Before(existing) {
			out[k] = t
		}
	}
	return out
}

// ApplyCRL installs the entries of a verified CRL into the store.
// Replaces (does not merge) the previous CRL state — re-applying the
// same CRL is idempotent; applying a newer CRL with fewer entries
// drops the missing keys' CRL-set timestamps (operator-set Revoke()
// timestamps are unaffected).
//
// Entries naming a KeyID NOT in the store are accepted anyway: the
// trust store may grow (Add) after a CRL was applied, and we want
// the CRL to take effect immediately rather than after a re-fetch.
// The dual map keeps these CRL-only entries separate from the keys
// the operator has explicitly trusted.
func (m *MemoryTrustStore) ApplyCRL(c *CRL) {
	if c == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fresh := make(map[KeyID]time.Time, len(c.Entries))
	for _, e := range c.Entries {
		fresh[e.KeyID] = e.NotValidAfter
	}
	m.crlRevokedAt = fresh
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

// Dir returns the on-disk directory backing this store. Exposed so
// admin CLI code can stat or display the path it just mutated; the
// hot-path verify code never needs it.
func (d *DirTrustStore) Dir() string { return d.dir }

// pubFileName / revokedFileName render the deterministic on-disk
// names for a KeyID. Persist writes to these names; PersistRemove and
// PersistRevoke target the same names so admin operations don't have
// to scan content to find the right file.
func pubFileName(id KeyID) string     { return id.Hex() + ".pub" }
func revokedFileName(id KeyID) string { return id.Hex() + ".revoked" }

// Load reads every .pub and .revoked file under the store's directory
// and populates the in-memory cache. A missing directory is treated
// as "no trusted keys" (empty store); this is the correct posture for
// first boot. A directory that exists but is unreadable, or a file
// that fails to parse, is a hard error.
//
// Operator-set revocation timestamps survive across reloads because
// .revoked files persist them. CRL-set timestamps live only in memory
// and are repopulated by ApplyCRL on the next refresher tick — by
// design (the CRL channel is upstream-authoritative, not operator-
// editable on disk).
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
	freshRevoked := make(map[KeyID]time.Time)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(d.dir, e.Name())
		switch {
		case strings.HasSuffix(e.Name(), ".pub"):
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
		case strings.HasSuffix(e.Name(), ".revoked"):
			raw, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("%w: %s: %w", ErrTrustStoreCorrupt, path, err)
			}
			id, t, err := parseRevokedFile(e.Name(), raw)
			if err != nil {
				return fmt.Errorf("%w: %s: %w", ErrTrustStoreCorrupt, path, err)
			}
			freshRevoked[id] = t
		default:
			// Other files are ignored (notes, README, etc.).
		}
	}

	d.mu.Lock()
	d.keys = fresh
	// Operator-set revocations come from disk; CRL-set entries are
	// kept untouched (they are repopulated by the upstream Refresher).
	d.revokedAt = freshRevoked
	d.mu.Unlock()
	return nil
}

// Persist writes pub to <dir>/<keyid>.pub and adds it to the in-memory
// cache. Atomic via tmp+rename — a concurrent Load() (or a daemon
// SIGHUP mid-write) never observes a half-written file. The operator-
// set revocation timestamp for this id is cleared on disk and in
// memory: a fresh Persist means "operator decided the key is valid
// again from now" (matches MemoryTrustStore.Add semantics).
//
// File mode 0o644: pubkeys are public material. Directory created
// 0o755 if missing.
func (d *DirTrustStore) Persist(pub PublicKey, comment string) error {
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return fmt.Errorf("truststore: mkdir %s: %w", d.dir, err)
	}
	raw, err := EncodePublicKeyFile(pub, comment)
	if err != nil {
		return fmt.Errorf("truststore: encode pubkey: %w", err)
	}
	target := filepath.Join(d.dir, pubFileName(pub.ID))
	if err := atomicWrite(target, raw, 0o644); err != nil {
		return fmt.Errorf("truststore: persist %s: %w", target, err)
	}
	// Mirror Add() semantics on the disk: clear the .revoked sidecar.
	revoked := filepath.Join(d.dir, revokedFileName(pub.ID))
	if err := os.Remove(revoked); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("truststore: clear stale revoked %s: %w", revoked, err)
	}
	d.MemoryTrustStore.Add(pub)
	return nil
}

// PersistRemove deletes <keyid>.pub and any companion <keyid>.revoked
// from the store directory and clears the in-memory entry. The
// operation is idempotent — a missing .pub file returns nil (the
// post-condition "key absent from store" already holds).
func (d *DirTrustStore) PersistRemove(id KeyID) error {
	pub := filepath.Join(d.dir, pubFileName(id))
	revoked := filepath.Join(d.dir, revokedFileName(id))
	if err := os.Remove(pub); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("truststore: remove %s: %w", pub, err)
	}
	if err := os.Remove(revoked); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("truststore: remove %s: %w", revoked, err)
	}
	d.MemoryTrustStore.Remove(id)
	return nil
}

// PersistRevoke writes <keyid>.revoked with the RFC3339Nano timestamp
// and records it in-memory. The pubkey file remains in place: Revoke
// pins a not-valid-after boundary, it does not delete the key.
//
// Revoking a key not present in the store is a no-op (matches
// MemoryTrustStore.Revoke semantics) — operators get a typed error so
// the CLI can surface a clear "no such key" rather than silently
// writing a dangling .revoked sidecar.
func (d *DirTrustStore) PersistRevoke(id KeyID, notValidAfter time.Time) error {
	if _, ok, _ := d.Lookup(id); !ok {
		return fmt.Errorf("%w: %s", ErrKeyNotInStore, id.Hex())
	}
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return fmt.Errorf("truststore: mkdir %s: %w", d.dir, err)
	}
	target := filepath.Join(d.dir, revokedFileName(id))
	body := []byte(notValidAfter.UTC().Format(time.RFC3339Nano) + "\n")
	if err := atomicWrite(target, body, 0o644); err != nil {
		return fmt.Errorf("truststore: persist %s: %w", target, err)
	}
	d.MemoryTrustStore.Revoke(id, notValidAfter)
	return nil
}

// ErrKeyNotInStore distinguishes "operator named a key the store
// doesn't have" from generic I/O errors. The admin CLI maps it to a
// non-zero exit with an explicit "no such key" message.
var ErrKeyNotInStore = errors.New("truststore: key not in store")

// atomicWrite writes data to a sibling temp file then renames over
// target. The rename is atomic on POSIX filesystems; a concurrent
// reader sees either the old content or the new, never a partial.
// On error, the temp file is best-effort cleaned up.
func atomicWrite(target string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		cleanup()
		return err
	}
	return nil
}

// parseRevokedFile decodes a <keyid>.revoked file. The KeyID is taken
// from the filename (16 hex chars before .revoked) so the file format
// itself is just a single RFC3339Nano timestamp line — no embedded ID
// to disagree with the filename.
func parseRevokedFile(name string, raw []byte) (KeyID, time.Time, error) {
	stem := strings.TrimSuffix(name, ".revoked")
	if len(stem) != 16 {
		return KeyID{}, time.Time{}, fmt.Errorf("filename %q: stem length %d, want 16 hex chars", name, len(stem))
	}
	var id KeyID
	for i := 0; i < 8; i++ {
		hi, ok1 := unhex(stem[i*2])
		lo, ok2 := unhex(stem[i*2+1])
		if !ok1 || !ok2 {
			return KeyID{}, time.Time{}, fmt.Errorf("filename %q: not hex at byte %d", name, i)
		}
		id[i] = hi<<4 | lo
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(raw)))
	if err != nil {
		return KeyID{}, time.Time{}, fmt.Errorf("body: %w", err)
	}
	return id, t, nil
}

// unhex maps an ASCII hex digit (upper or lower) to its 4-bit value.
func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}
