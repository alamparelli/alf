// Package userkey persists the §7.3 Tier-3 user-endorsed signing key
// on disk, encrypted at rest with a passphrase the operator types on
// the TTY. The key is materialised in cleartext only inside Sign()'s
// stack frame, then zeroed before return — at no point does the
// daemon, an LLM-driven path, or another capability see the bytes.
//
// Position in the §6 admin trust domain: this package lives under
// internal/admin/, which TestAdminPackageBoundary pins to TTY-direct
// CLI commands and the dedicated CC admin trust domain. Adding a
// non-CLI consumer requires a one-line justification in the archtest
// allowlist — that's the load-bearing rule preventing prompt
// injection from reaching key material.
//
// File format. Single JSON record at <dataDir>/keys/user-endorsed.json
// (mode 0o600) with these fields:
//
//   - version         schema rev (currently 1; bumps when the on-wire
//     KDF or AEAD changes)
//   - kdf             "argon2id" — the only supported KDF today
//   - kdf_time        argon2id time cost (iterations)
//   - kdf_memory      argon2id memory cost (KiB)
//   - kdf_parallelism argon2id parallelism factor
//   - key_id_hex      8-byte minisign KeyID (16 hex chars)
//   - pub_hex         32-byte Ed25519 public key
//   - salt_b64        32-byte argon2id salt (random per key)
//   - nonce_b64       12-byte ChaCha20-Poly1305 nonce (random per
//     encryption — every Generate() rolls new salt + nonce)
//   - ciphertext_b64  AEAD-sealed 64-byte Ed25519 private key
//
// AEAD binding. The associated-data input to ChaCha20-Poly1305 is
// version || kdf-id || key_id || pub. A swap of any of these fields
// (e.g. attacker substitutes another user's pubkey hoping the
// passphrase still decrypts) breaks the AEAD tag and surfaces as
// ErrPassphrase — indistinguishable from a wrong password from the
// attacker's perspective.
//
// Why not the existing vault. The vault was designed for capability-
// scoped secrets accessed by Runtime on behalf of a running cap. The
// user-endorsed key is human-held material the operator unlocks per
// admin command — different lifecycle, different threat model, no
// daemon socket involved. Stage 2 chunk 4 introduces a vault
// user-scope partition for ratification tokens and CC-side admin
// state; the userkey store may migrate into it then. For chunk 2 a
// single self-contained file keeps the trust surface narrow.
package userkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// keyFile is the relative path under DataDir where the user-endorsed
// signing key record is persisted.
const keyFile = "keys/user-endorsed.json"

// schemaVersion is the file-format version. Reads of older versions
// would migrate; reads of newer versions are rejected with ErrCorrupt
// so a downgrade cannot silently truncate AEAD bindings.
const schemaVersion = 1

// kdfArgon2id is the only supported KDF identifier on disk. The
// constant width (1 byte's worth of meaning) lets the AEAD AAD
// include a single tag byte without serialising the longer string.
const (
	kdfArgon2idLabel = "argon2id"
	kdfArgon2idID    = byte(1)
)

// Default Argon2id parameters. Tuned for ~250ms on a 2024 laptop
// with 64 MiB memory; high enough to make brute-forcing a 12-char
// passphrase expensive without making `alf sign` painful. The
// numbers are persisted in the record so future tightening doesn't
// invalidate keys generated under older params.
const (
	defaultArgon2Time        uint32 = 3
	defaultArgon2Memory      uint32 = 64 * 1024 // 64 MiB
	defaultArgon2Parallelism uint8  = 4
	derivedKeyLen            uint32 = 32 // ChaCha20-Poly1305 key size
	saltLen                         = 32
)

// MinPassphraseBytes is the floor enforced at Generate time. Twelve
// bytes is the OWASP-style "memorable but not trivial" lower bound;
// the CLI prompts twice and refuses anything shorter. It is NOT
// re-checked on Sign — an existing key with a shorter passphrase
// still works (no silent lockout) but the operator gets a hint.
const MinPassphraseBytes = 12

// Typed errors. Callers map these to user-facing messages; the CLI
// turns ErrPassphrase into "wrong passphrase" without leaking which
// stage failed (key mismatch vs corrupt vs wrong pass all surface as
// ErrPassphrase from the AEAD level — the file layout is otherwise
// validated upfront with ErrCorrupt).
var (
	ErrAlreadyExists       = errors.New("userkey: a user-endorsed key already exists at this path")
	ErrNotFound            = errors.New("userkey: no user-endorsed key on disk")
	ErrPassphrase          = errors.New("userkey: passphrase does not unlock the stored key")
	ErrPassphraseTooShort  = fmt.Errorf("userkey: passphrase must be at least %d bytes", MinPassphraseBytes)
	ErrCorrupt             = errors.New("userkey: stored key file is corrupt or unreadable")
	ErrUnsupportedSchema   = errors.New("userkey: stored key uses an unsupported file-format version")
)

// Store is a thin filesystem-bound coordinator around the on-disk
// key record. All public methods are safe to call from a single
// goroutine (the CLI dispatch is single-threaded by construction);
// concurrent writes from two TTYs would race the tmp+rename and the
// loser's file may shadow the winner — same trade-off as DirTrustStore.
type Store struct {
	// Path is the absolute path to user-endorsed.json. Tests pass a
	// tmpdir-rooted value; production wires DefaultPath(dataDir).
	Path string
}

// NewStore constructs a Store rooted at <dataDir>/keys/user-endorsed.json.
// dataDir must be the install's data directory (the same directory
// the daemon's daemonkey.go uses for its own keys/daemon.json — the
// two records are siblings on purpose, so a single backup of keys/
// captures both Tier-2 and Tier-3 material).
func NewStore(dataDir string) *Store {
	return &Store{Path: filepath.Join(dataDir, keyFile)}
}

// Exists reports whether the on-disk record is present. Used by the
// CLI to decide whether `alf keygen` should refuse without --force.
// A read error other than ErrNotExist is treated as "exists, but
// you'd better look": surface it through Generate / LoadPublic.
func (s *Store) Exists() bool {
	_, err := os.Stat(s.Path)
	return err == nil
}

// record is the on-disk JSON layout. Field order is fixed to keep
// hand-inspection sane; field renames bump schemaVersion.
type record struct {
	Version         int    `json:"version"`
	KDF             string `json:"kdf"`
	KDFTime         uint32 `json:"kdf_time"`
	KDFMemory       uint32 `json:"kdf_memory"`
	KDFParallelism  uint8  `json:"kdf_parallelism"`
	KeyIDHex        string `json:"key_id_hex"`
	PubHex          string `json:"pub_hex"`
	SaltB64         string `json:"salt_b64"`
	NonceB64        string `json:"nonce_b64"`
	CiphertextB64   string `json:"ciphertext_b64"`
}

// Generate mints a fresh Ed25519 keypair, encrypts the private bytes
// with passphrase-derived material, and atomically persists the
// record at Path. Returns the public half so the CLI can immediately
// print the fingerprint.
//
// Refuses if a record is already present (caller's responsibility to
// remove first via `alf keygen --force` confirmation flow). Refuses
// passphrases shorter than MinPassphraseBytes — the CLI surfaces this
// as a re-prompt rather than a hard exit.
func (s *Store) Generate(passphrase []byte) (envelope.PublicKey, error) {
	if len(passphrase) < MinPassphraseBytes {
		return envelope.PublicKey{}, ErrPassphraseTooShort
	}
	if s.Exists() {
		return envelope.PublicKey{}, fmt.Errorf("%w: %s", ErrAlreadyExists, s.Path)
	}

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		return envelope.PublicKey{}, fmt.Errorf("userkey: generate keypair: %w", err)
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return envelope.PublicKey{}, fmt.Errorf("userkey: random salt: %w", err)
	}
	nonce := make([]byte, chacha20poly1305.NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return envelope.PublicKey{}, fmt.Errorf("userkey: random nonce: %w", err)
	}

	derivedKey := argon2.IDKey(passphrase, salt, defaultArgon2Time, defaultArgon2Memory, defaultArgon2Parallelism, derivedKeyLen)
	defer zero(derivedKey)

	aead, err := chacha20poly1305.New(derivedKey)
	if err != nil {
		return envelope.PublicKey{}, fmt.Errorf("userkey: aead init: %w", err)
	}

	aad := buildAAD(schemaVersion, kdfArgon2idID, priv.ID, pub.Key)
	ciphertext := aead.Seal(nil, nonce, priv.Key, aad)

	rec := record{
		Version:        schemaVersion,
		KDF:            kdfArgon2idLabel,
		KDFTime:        defaultArgon2Time,
		KDFMemory:      defaultArgon2Memory,
		KDFParallelism: defaultArgon2Parallelism,
		KeyIDHex:       hex.EncodeToString(priv.ID[:]),
		PubHex:         hex.EncodeToString(pub.Key),
		SaltB64:        base64.StdEncoding.EncodeToString(salt),
		NonceB64:       base64.StdEncoding.EncodeToString(nonce),
		CiphertextB64:  base64.StdEncoding.EncodeToString(ciphertext),
	}

	if err := persistRecord(s.Path, rec); err != nil {
		return envelope.PublicKey{}, err
	}
	return pub, nil
}

// LoadPublic reads the record and returns the public half without
// touching the passphrase. Used by the CLI to print the fingerprint
// in `alf keygen --print` and to expose the .pub file for `alf
// trust add` on other machines.
func (s *Store) LoadPublic() (envelope.PublicKey, error) {
	rec, err := readRecord(s.Path)
	if err != nil {
		return envelope.PublicKey{}, err
	}
	pub, _, err := decodeIdentity(rec)
	return pub, err
}

// Sign decrypts the private key with passphrase, signs canonical via
// envelope.Sign, and returns the 74-byte minisign signature blob plus
// the signer's KeyID. The plaintext private key lives only on this
// stack frame and is zeroed in defer before the function returns.
//
// A wrong passphrase, a corrupted file with the AEAD tag mismatch,
// or a tampered AAD field all surface as ErrPassphrase — by design,
// to avoid revealing whether the operator typed wrong or whether
// the file was modified. Surface-level corruption (bad base64,
// truncated record) returns ErrCorrupt before any KDF work.
func (s *Store) Sign(passphrase []byte, canonical []byte) ([]byte, envelope.KeyID, error) {
	rec, err := readRecord(s.Path)
	if err != nil {
		return nil, envelope.KeyID{}, err
	}

	pub, keyID, err := decodeIdentity(rec)
	if err != nil {
		return nil, envelope.KeyID{}, err
	}
	salt, err := base64.StdEncoding.DecodeString(rec.SaltB64)
	if err != nil || len(salt) != saltLen {
		return nil, envelope.KeyID{}, fmt.Errorf("%w: salt", ErrCorrupt)
	}
	nonce, err := base64.StdEncoding.DecodeString(rec.NonceB64)
	if err != nil || len(nonce) != chacha20poly1305.NonceSize {
		return nil, envelope.KeyID{}, fmt.Errorf("%w: nonce", ErrCorrupt)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(rec.CiphertextB64)
	if err != nil {
		return nil, envelope.KeyID{}, fmt.Errorf("%w: ciphertext", ErrCorrupt)
	}

	derivedKey := argon2.IDKey(passphrase, salt, rec.KDFTime, rec.KDFMemory, rec.KDFParallelism, derivedKeyLen)
	defer zero(derivedKey)

	aead, err := chacha20poly1305.New(derivedKey)
	if err != nil {
		return nil, envelope.KeyID{}, fmt.Errorf("userkey: aead init: %w", err)
	}
	aad := buildAAD(rec.Version, kdfArgon2idID, keyID, pub.Key)
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, envelope.KeyID{}, ErrPassphrase
	}
	defer zero(plaintext)
	if len(plaintext) != ed25519.PrivateKeySize {
		return nil, envelope.KeyID{}, fmt.Errorf("%w: priv key length %d", ErrCorrupt, len(plaintext))
	}

	priv := envelope.PrivateKey{ID: keyID, Key: ed25519.PrivateKey(plaintext)}
	sig, err := envelope.Sign(priv, canonical)
	if err != nil {
		return nil, envelope.KeyID{}, fmt.Errorf("userkey: sign: %w", err)
	}
	return sig, keyID, nil
}

// Remove deletes the on-disk record. Used by `alf keygen --force`
// after the operator confirms overwrite. Idempotent on a missing
// file (so a `--force` rerun after a partial write does not error).
func (s *Store) Remove() error {
	err := os.Remove(s.Path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("userkey: remove %s: %w", s.Path, err)
}

// DefaultPath returns the canonical path for the user-endorsed key
// under dataDir. CLI consumers compose this with the install layout's
// AlfDir() resolution.
func DefaultPath(dataDir string) string {
	return filepath.Join(dataDir, keyFile)
}

// readRecord loads + validates the JSON record + permission gate.
// Refuses files with too-permissive perms (defence in depth: a local
// actor with read access shouldn't be able to copy ciphertext + nonce
// for offline brute-force). Schema version mismatches are rejected
// before any KDF work.
func readRecord(path string) (record, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return record{}, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return record{}, fmt.Errorf("userkey: stat %s: %w", path, err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return record{}, fmt.Errorf("userkey: %s has permissive perms %v; refusing to load", path, info.Mode().Perm())
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return record{}, fmt.Errorf("userkey: read %s: %w", path, err)
	}
	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return record{}, fmt.Errorf("%w: json: %w", ErrCorrupt, err)
	}
	if rec.Version != schemaVersion {
		return record{}, fmt.Errorf("%w: got version %d, want %d", ErrUnsupportedSchema, rec.Version, schemaVersion)
	}
	if rec.KDF != kdfArgon2idLabel {
		return record{}, fmt.Errorf("%w: kdf %q (only %q supported)", ErrUnsupportedSchema, rec.KDF, kdfArgon2idLabel)
	}
	return rec, nil
}

// decodeIdentity extracts the typed PublicKey + KeyID from the record
// without touching the encrypted private bytes. Used both by Sign
// (to fill the priv struct) and LoadPublic (cheap lookup).
func decodeIdentity(rec record) (envelope.PublicKey, envelope.KeyID, error) {
	idBytes, err := hex.DecodeString(rec.KeyIDHex)
	if err != nil || len(idBytes) != 8 {
		return envelope.PublicKey{}, envelope.KeyID{}, fmt.Errorf("%w: key_id_hex", ErrCorrupt)
	}
	pubBytes, err := hex.DecodeString(rec.PubHex)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return envelope.PublicKey{}, envelope.KeyID{}, fmt.Errorf("%w: pub_hex", ErrCorrupt)
	}
	var id envelope.KeyID
	copy(id[:], idBytes)
	return envelope.PublicKey{ID: id, Key: ed25519.PublicKey(pubBytes)}, id, nil
}

// persistRecord writes rec to path atomically (tmp + rename) with
// 0o600 perms and ensures the parent dir exists with 0o700. Mirrors
// daemonkey.go's persistDaemonKey hygiene.
func persistRecord(path string, rec record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("userkey: mkdir %s: %w", filepath.Dir(path), err)
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("userkey: marshal: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".user-endorsed-*.tmp")
	if err != nil {
		return fmt.Errorf("userkey: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename failed.
		_ = os.Remove(tmpPath)
	}()
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("userkey: chmod tmp: %w", err)
	}
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("userkey: write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("userkey: sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("userkey: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("userkey: rename tmp -> %s: %w", path, err)
	}
	return nil
}

// buildAAD packs the binding fields into a stable byte sequence. The
// AAD is part of the AEAD seal, so any swap of these fields between
// records breaks decryption. Layout is fixed-width to avoid length-
// extension ambiguities.
//
//	[1 byte version-low] [1 byte kdf-id] [8 bytes keyID] [32 bytes pub]
func buildAAD(version int, kdfID byte, keyID envelope.KeyID, pub ed25519.PublicKey) []byte {
	aad := make([]byte, 0, 1+1+8+ed25519.PublicKeySize)
	aad = append(aad, byte(version))
	aad = append(aad, kdfID)
	aad = append(aad, keyID[:]...)
	aad = append(aad, pub...)
	return aad
}

// zero overwrites b with zero bytes. Best-effort hygiene — Go does
// not guarantee the compiler won't keep a stale copy in a register,
// but for material that's ~30µs old this is the cheapest reasonable
// thing to do. Used on derived keys + decrypted private bytes.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
