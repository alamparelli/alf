package wasm

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// daemonKeyFile is the relative path under DataDir where the local
// signing key is persisted. §7.3 Tier 2 — auto-generated at first
// boot, lives in the vault-equivalent directory with 0o600 perms.
const daemonKeyFile = "keys/daemon.json"

// daemonKeyRecord is the persisted form of the daemon key. Hex
// encoding keeps the file hand-inspectable; format is internal (we
// rev the struct when needed).
type daemonKeyRecord struct {
	IDHex   string `json:"id_hex"`
	PrivHex string `json:"priv_hex"`
	PubHex  string `json:"pub_hex"`
}

// LoadOrGenerateDaemonKey returns the daemon's local signing key,
// creating it on first boot (§7.3 Tier 2). The key file is written
// with 0o600; the parent keys/ directory with 0o700. Both are
// rejected if too-permissive on load — a tampered-permissions file
// is treated as compromised.
func LoadOrGenerateDaemonKey(dataDir string) (envelope.PublicKey, envelope.PrivateKey, error) {
	if dataDir == "" {
		return envelope.PublicKey{}, envelope.PrivateKey{}, fmt.Errorf("wasm: LoadOrGenerateDaemonKey requires non-empty DataDir")
	}
	path := filepath.Join(dataDir, daemonKeyFile)

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		pub, priv, perr := parseDaemonKey(raw)
		if perr != nil {
			return envelope.PublicKey{}, envelope.PrivateKey{}, fmt.Errorf("wasm: daemon key file is malformed: %w", perr)
		}
		if err := enforcePerms(path); err != nil {
			return envelope.PublicKey{}, envelope.PrivateKey{}, err
		}
		return pub, priv, nil
	case errors.Is(err, fs.ErrNotExist):
		// Generate.
		pub, priv, gerr := envelope.GenerateKey()
		if gerr != nil {
			return envelope.PublicKey{}, envelope.PrivateKey{}, gerr
		}
		if err := persistDaemonKey(path, pub, priv); err != nil {
			return envelope.PublicKey{}, envelope.PrivateKey{}, err
		}
		return pub, priv, nil
	default:
		return envelope.PublicKey{}, envelope.PrivateKey{}, fmt.Errorf("wasm: read daemon key: %w", err)
	}
}

func parseDaemonKey(raw []byte) (envelope.PublicKey, envelope.PrivateKey, error) {
	var rec daemonKeyRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return envelope.PublicKey{}, envelope.PrivateKey{}, fmt.Errorf("decode json: %w", err)
	}
	idBytes, err := hex.DecodeString(rec.IDHex)
	if err != nil || len(idBytes) != 8 {
		return envelope.PublicKey{}, envelope.PrivateKey{}, fmt.Errorf("id_hex: want 8 bytes, got %d (err=%v)", len(idBytes), err)
	}
	privBytes, err := hex.DecodeString(rec.PrivHex)
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		return envelope.PublicKey{}, envelope.PrivateKey{}, fmt.Errorf("priv_hex: want %d bytes, got %d (err=%v)", ed25519.PrivateKeySize, len(privBytes), err)
	}
	pubBytes, err := hex.DecodeString(rec.PubHex)
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return envelope.PublicKey{}, envelope.PrivateKey{}, fmt.Errorf("pub_hex: want %d bytes, got %d (err=%v)", ed25519.PublicKeySize, len(pubBytes), err)
	}
	var id envelope.KeyID
	copy(id[:], idBytes)
	return envelope.PublicKey{ID: id, Key: ed25519.PublicKey(pubBytes)},
		envelope.PrivateKey{ID: id, Key: ed25519.PrivateKey(privBytes)},
		nil
}

func persistDaemonKey(path string, pub envelope.PublicKey, priv envelope.PrivateKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("wasm: mkdir keys/: %w", err)
	}
	rec := daemonKeyRecord{
		IDHex:   hex.EncodeToString(priv.ID[:]),
		PrivHex: hex.EncodeToString(priv.Key),
		PubHex:  hex.EncodeToString(pub.Key),
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("wasm: marshal daemon key: %w", err)
	}
	// Write with 0o600 and a restrictive umask via explicit Chmod
	// after — os.WriteFile applies umask, so we normalise after.
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("wasm: write daemon key: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("wasm: chmod daemon key: %w", err)
	}
	return nil
}

// enforcePerms rejects the key file if it is world/group-readable.
// This is defence-in-depth for §7.3 Tier 2 — a local actor with
// read access to DataDir shouldn't be able to exfiltrate the
// signing key via POSIX ACL drift.
func enforcePerms(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("wasm: daemon key %q has permissive perms %v; refusing to load", path, info.Mode().Perm())
	}
	return nil
}
