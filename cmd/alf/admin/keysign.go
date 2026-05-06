package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/admin/userkey"
	"github.com/alamparelli/alf/internal/capability/envelope"
)

// Keygen handles `alf keygen [--export-pub <path>] [--force]`. Mints
// the §7.3 Tier-3 user-endorsed key, persisting it under
// env.UserKeyPath encrypted with a TTY-typed passphrase. Re-running
// without --force on an existing record fails — the operator must
// confirm overwrite explicitly because losing the old key invalidates
// every bundle previously signed with it.
//
// The function is idempotent only on the "no key yet" path; on the
// "already exists" path it requires --force AND the standard "yes"
// confirm prompt. After successful generation, the public half is
// printed (fingerprint) and optionally exported as a minisign-format
// .pub file, ready to feed into `alf trust add` on other machines.
func Keygen(env Env, args []string) error {
	exportPub := ""
	force := false
	comment := "alf user-endorsed key"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--export-pub":
			if i+1 >= len(args) {
				return fmt.Errorf("keygen: --export-pub needs a path argument")
			}
			exportPub = args[i+1]
			i++
		case "--comment":
			if i+1 >= len(args) {
				return fmt.Errorf("keygen: --comment needs a string argument")
			}
			comment = args[i+1]
			i++
		case "--force":
			force = true
		case "-h", "--help":
			return printKeygenUsage(env.Stdout)
		default:
			return fmt.Errorf("keygen: unknown argument %q", args[i])
		}
	}

	if env.IsTerminal != nil && !env.IsTerminal() {
		return ErrNonInteractive
	}

	store := &userkey.Store{Path: env.UserKeyPath}

	if store.Exists() {
		if !force {
			return fmt.Errorf("keygen: %s already exists; rerun with --force to overwrite (this invalidates every bundle previously signed with the old key)", env.UserKeyPath)
		}
		fmt.Fprintf(env.Stdout, "About to OVERWRITE existing user-endorsed key at %s.\n", env.UserKeyPath)
		fmt.Fprintln(env.Stdout, "Bundles signed with the old key will FAIL verification afterwards.")
		if err := requireConfirm(env, "Type 'yes' to overwrite: "); err != nil {
			return err
		}
		if err := store.Remove(); err != nil {
			return fmt.Errorf("keygen: remove existing record: %w", err)
		}
	}

	pass, err := promptNewPassphrase(env)
	if err != nil {
		return err
	}
	defer zeroBytes(pass)

	pub, err := store.Generate(pass)
	if err != nil {
		return fmt.Errorf("keygen: %w", err)
	}

	fmt.Fprintf(env.Stdout, "\nUser-endorsed key created.\n")
	fmt.Fprintf(env.Stdout, "  Fingerprint: %s\n", pub.ID.Hex())
	fmt.Fprintf(env.Stdout, "  Stored at:   %s (mode 0600, passphrase-protected)\n", env.UserKeyPath)

	if exportPub != "" {
		raw, err := envelope.EncodePublicKeyFile(pub, comment)
		if err != nil {
			return fmt.Errorf("keygen: encode pubkey: %w", err)
		}
		if err := writeFileAtomic(exportPub, raw, 0o644); err != nil {
			return fmt.Errorf("keygen: write %s: %w", exportPub, err)
		}
		fmt.Fprintf(env.Stdout, "  Exported pub: %s\n", exportPub)
		fmt.Fprintf(env.Stdout, "\nDistribute %s to other machines and run 'alf trust add %s'.\n", filepath.Base(exportPub), filepath.Base(exportPub))
	} else {
		fmt.Fprintln(env.Stdout, "\nTo trust this key on another machine, re-run with --export-pub <file>")
		fmt.Fprintln(env.Stdout, "or copy the daemon's pub via the alf admin export flow (Stage 2 chunk 3).")
	}
	return nil
}

// Sign handles `alf sign <bundle-dir> [--bundle <path>] [--at <RFC3339>]`.
// Reads the bundle's manifest.toml, validates it (NO Tier-2 ceiling
// check — Tier 3 is exactly the path that may widen authority beyond
// the daemon key's ceiling per SEC-004), canonicalises, asks for the
// user-key passphrase, signs, and writes manifest.sig next to
// manifest.toml.
//
// Bundle-artifact detection follows kind:
//
//   - wasm-tool / wasm-app  → look for a single *.wasm in the bundle dir
//   - marketplace-app       → look for bundle.zip in the bundle dir
//   - skill / provider      → no artifact (BundleHash empty in trusted
//     comment; envelope.Verify treats this as fine)
//
// --bundle <path> overrides detection. --at <RFC3339> overrides the
// signed-at timestamp (default: env.Now()). The signature file is
// written atomically; an existing manifest.sig is replaced (re-signing
// is the supported workflow).
func Sign(env Env, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("sign: missing bundle-dir argument")
	}
	bundleDir := args[0]
	bundleOverride := ""
	signedAt := env.Now()
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--bundle":
			if i+1 >= len(args) {
				return fmt.Errorf("sign: --bundle needs a path argument")
			}
			bundleOverride = args[i+1]
			i++
		case "--at":
			if i+1 >= len(args) {
				return fmt.Errorf("sign: --at needs an RFC3339 timestamp")
			}
			t, err := time.Parse(time.RFC3339, args[i+1])
			if err != nil {
				return fmt.Errorf("sign: --at %q: %w", args[i+1], err)
			}
			signedAt = t
			i++
		case "-h", "--help":
			return printSignUsage(env.Stdout)
		default:
			return fmt.Errorf("sign: unknown argument %q", args[i])
		}
	}

	if env.IsTerminal != nil && !env.IsTerminal() {
		return ErrNonInteractive
	}

	manifestPath := filepath.Join(bundleDir, "manifest.toml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("sign: read %s: %w", manifestPath, err)
	}
	manifest, err := envelope.Validate(manifestBytes)
	if err != nil {
		return fmt.Errorf("sign: validate manifest: %w", err)
	}

	bundlePath, bundleBytes, err := resolveBundleArtifact(bundleDir, bundleOverride, manifest)
	if err != nil {
		return err
	}

	store := &userkey.Store{Path: env.UserKeyPath}
	if !store.Exists() {
		return fmt.Errorf("sign: no user-endorsed key at %s — run 'alf keygen' first", env.UserKeyPath)
	}

	canonical, err := envelope.Canonicalize(manifestBytes)
	if err != nil {
		return fmt.Errorf("sign: canonicalize: %w", err)
	}

	fmt.Fprintf(env.Stdout, "Signing %s (kind=%s id=%s)\n", manifestPath, manifest.Kind, manifest.ID)
	if bundlePath != "" {
		fmt.Fprintf(env.Stdout, "  Bundle: %s (%d bytes)\n", bundlePath, len(bundleBytes))
	} else {
		fmt.Fprintf(env.Stdout, "  Bundle: <none — kind %q has no artefact>\n", manifest.Kind)
	}
	fmt.Fprintf(env.Stdout, "  Signed at: %s\n", signedAt.UTC().Format(time.RFC3339))

	pass, err := env.ReadPassword("User-endorsed key passphrase: ")
	if err != nil {
		return fmt.Errorf("sign: read passphrase: %w", err)
	}
	defer zeroBytes(pass)
	fmt.Fprintln(env.Stdout)

	tc := envelope.TrustedComment{
		BundleID: manifest.ID + "@" + manifest.Version,
		SignedAt: signedAt.UTC(),
	}
	if bundleBytes != nil {
		hash := sha256.Sum256(bundleBytes)
		tc.BundleHash = hex.EncodeToString(hash[:])
	}
	trustedComment := envelope.BuildTrustedComment(tc)

	// Single decryption: produce the main signature AND encode the
	// 4-line file (which internally generates the global-comment
	// Ed25519 signature). userkey.Store.WithPrivateKey hands us a
	// scoped PrivateKey and zeroes it on return; the priv bytes never
	// leave this callback's stack frame.
	var (
		sigFile  []byte
		signerID envelope.KeyID
	)
	err = store.WithPrivateKey(pass, func(priv envelope.PrivateKey) error {
		sigBlob, sErr := envelope.Sign(priv, canonical)
		if sErr != nil {
			return fmt.Errorf("sign: main sig: %w", sErr)
		}
		encoded, eErr := envelope.EncodeSignatureFile(priv, sigBlob, trustedComment)
		if eErr != nil {
			return fmt.Errorf("sign: encode signature file: %w", eErr)
		}
		sigFile = encoded
		signerID = priv.ID
		return nil
	})
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	sigPath := filepath.Join(bundleDir, "manifest.sig")
	if err := writeFileAtomic(sigPath, sigFile, 0o644); err != nil {
		return fmt.Errorf("sign: write %s: %w", sigPath, err)
	}

	fmt.Fprintf(env.Stdout, "Wrote %s.\n", sigPath)
	fmt.Fprintf(env.Stdout, "Signer: %s. The bundle now passes envelope.Verify against any trust store containing this key.\n", signerID.Hex())
	return nil
}

// resolveBundleArtifact returns the bundle path + bytes (or empty
// strings + nil for kinds that have no artefact). Looks for the
// expected file in the bundle dir, honoring --bundle override.
func resolveBundleArtifact(bundleDir, override string, manifest *envelope.Manifest) (string, []byte, error) {
	if override != "" {
		raw, err := os.ReadFile(override)
		if err != nil {
			return "", nil, fmt.Errorf("sign: read --bundle %s: %w", override, err)
		}
		return override, raw, nil
	}

	switch manifest.Kind {
	case envelope.KindWASMTool, envelope.KindWASMApp:
		matches, err := findFiles(bundleDir, ".wasm")
		if err != nil {
			return "", nil, err
		}
		switch len(matches) {
		case 0:
			return "", nil, fmt.Errorf("sign: no .wasm artefact in %s; pass --bundle <path> if it lives elsewhere", bundleDir)
		case 1:
			raw, err := os.ReadFile(matches[0])
			if err != nil {
				return "", nil, fmt.Errorf("sign: read %s: %w", matches[0], err)
			}
			return matches[0], raw, nil
		default:
			return "", nil, fmt.Errorf("sign: multiple .wasm files in %s (%d found); pass --bundle <path> to disambiguate", bundleDir, len(matches))
		}
	case envelope.KindMarketplaceApp:
		zipPath := filepath.Join(bundleDir, "bundle.zip")
		raw, err := os.ReadFile(zipPath)
		if err != nil {
			return "", nil, fmt.Errorf("sign: read %s: %w (kind %q expects bundle.zip)", zipPath, err, manifest.Kind)
		}
		return zipPath, raw, nil
	case envelope.KindSkill, envelope.KindProvider:
		return "", nil, nil
	default:
		return "", nil, fmt.Errorf("sign: unsupported manifest kind %q for artefact detection; pass --bundle <path>", manifest.Kind)
	}
}

// promptNewPassphrase asks for a passphrase, then asks again to
// confirm. Returns the matching passphrase or an error if the two
// reads disagree, the first read is shorter than MinPassphraseBytes,
// or either read fails. The function defers zeroing of the rejected
// candidate before returning.
func promptNewPassphrase(env Env) ([]byte, error) {
	first, err := env.ReadPassword("New passphrase: ")
	if err != nil {
		return nil, fmt.Errorf("read passphrase: %w", err)
	}
	fmt.Fprintln(env.Stdout)
	if len(first) < userkey.MinPassphraseBytes {
		zeroBytes(first)
		return nil, fmt.Errorf("passphrase must be at least %d bytes", userkey.MinPassphraseBytes)
	}
	second, err := env.ReadPassword("Confirm passphrase: ")
	if err != nil {
		zeroBytes(first)
		return nil, fmt.Errorf("read confirm: %w", err)
	}
	fmt.Fprintln(env.Stdout)
	if !bytes.Equal(first, second) {
		zeroBytes(first)
		zeroBytes(second)
		return nil, fmt.Errorf("passphrases do not match")
	}
	zeroBytes(second)
	return first, nil
}

// writeFileAtomic writes data to path via tmp+rename. Tighter perms
// than os.WriteFile (0o600 for keys, 0o644 for pub files) — caller
// passes the exact mode. The tmp file lands in the same directory so
// rename stays in-fs.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sig-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// findFiles walks dir non-recursively looking for files ending in
// suffix. Used to locate the single .wasm in a wasm-tool bundle.
// We do not recurse — bundles are flat by convention; recursion
// would make `--bundle` necessary too often and introduce
// surprising matches under data/ subdirs.
func findFiles(dir, suffix string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("sign: read dir %s: %w", dir, err)
	}
	var matches []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), suffix) {
			matches = append(matches, filepath.Join(dir, e.Name()))
		}
	}
	return matches, nil
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func printKeygenUsage(w io.Writer) error {
	_, err := fmt.Fprintln(w, `Usage: alf keygen [--export-pub <path>] [--comment "..."] [--force]

Mint the user-endorsed signing key (§7.3 Tier 3).

Stored at <dataDir>/keys/user-endorsed.json (mode 0600), encrypted
with a passphrase you type on the TTY. Use 'alf sign' to sign bundles
with this key.

Options:
  --export-pub <path>   Also write a minisign .pub file at <path>
                        (feed into 'alf trust add' on other machines)
  --comment "..."       Untrusted-comment line for the exported .pub
                        (default: "alf user-endorsed key")
  --force               Overwrite an existing key. Bundles signed
                        with the old key will FAIL verification.

Refuses on non-TTY stdin.`)
	return err
}

func printSignUsage(w io.Writer) error {
	_, err := fmt.Fprintln(w, `Usage: alf sign <bundle-dir> [--bundle <path>] [--at <RFC3339>]

Sign a bundle with the user-endorsed key (§7.3 Tier 3).

Reads <bundle-dir>/manifest.toml, validates the schema, canonicalises,
signs, and writes <bundle-dir>/manifest.sig. Detects the bundle
artefact from manifest.kind:

  - wasm-tool / wasm-app  : single *.wasm in <bundle-dir>
  - marketplace-app       : <bundle-dir>/bundle.zip
  - skill / provider      : no artefact

Options:
  --bundle <path>     Override artefact detection (file path)
  --at <RFC3339>      Override signed-at timestamp (default: now)

Refuses on non-TTY stdin and when no user-endorsed key exists yet
(run 'alf keygen' first).`)
	return err
}

