package admin

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// ErrNonInteractive is returned by mutating admin subcommands when
// stdin is not a TTY. The CLI dispatcher exits non-zero with a
// message pointing the operator at the only sanctioned override:
// rerun the command from a real terminal.
//
// Used by trust add/remove/revoke, keygen, and sign. List is the
// only read-only command and does not call it.
var ErrNonInteractive = errors.New("alf admin: refusing to run without a TTY")

// Trust dispatches `alf trust <sub> ...` to the matching handler. A
// missing or unknown subcommand prints usage and returns a non-nil
// error so cmd/alf/main can exit non-zero. The dispatcher itself
// never touches the filesystem — every effect is in the handler so
// tests can drive each one with a custom TrustEnv.
func Trust(env TrustEnv, args []string) error {
	if len(args) == 0 {
		return printTrustUsage(env.Stderr)
	}
	switch args[0] {
	case "list":
		return TrustList(env)
	case "add":
		return TrustAdd(env, args[1:])
	case "remove", "rm":
		return TrustRemove(env, args[1:])
	case "revoke":
		return TrustRevoke(env, args[1:])
	case "help", "-h", "--help":
		_ = printTrustUsage(env.Stdout)
		return nil
	default:
		fmt.Fprintf(env.Stderr, "Unknown trust subcommand: %s\n\n", args[0])
		_ = printTrustUsage(env.Stderr)
		return fmt.Errorf("trust: unknown subcommand %q", args[0])
	}
}

func printTrustUsage(w io.Writer) error {
	_, err := fmt.Fprintln(w, `Usage: alf trust <subcommand> [args]

Manage the daemon's trust store (operator-managed signing keys).

Subcommands:
  list                                  List trusted keys + revocation status
  add <pub-file> [--comment "..."]      Add a public key from a minisign .pub file
  remove <fingerprint>                  Remove a key by 16-hex-char fingerprint
  revoke <fingerprint> [--at <RFC3339>] Mark a key not-valid-after a timestamp

Mutating commands require a TTY and prompt for confirmation.
Changes take effect on the next 'alf restart'.`)
	return err
}

// TrustList prints the trust dir contents: every operator-added key
// with its fingerprint, untrusted-comment line, and (if any) the
// not-valid-after timestamp from a sibling .revoked sidecar.
//
// The daemon-bootstrapped key is not in the trust dir (it lives in
// <dataDir>/keys/daemon.json and is auto-trusted at boot), so it
// does not show up here — operators are not meant to manage it.
func TrustList(env TrustEnv) error {
	store, err := loadTrustStore(env.TrustDir)
	if err != nil {
		return err
	}
	keys := store.Keys()
	if len(keys) == 0 {
		fmt.Fprintln(env.Stdout, "No operator-managed keys.")
		fmt.Fprintf(env.Stdout, "Trust dir: %s\n", store.Dir())
		fmt.Fprintln(env.Stdout, "(The daemon-bootstrapped key is auto-trusted from <dataDir>/keys/daemon.json — not listed here.)")
		return nil
	}

	fmt.Fprintf(env.Stdout, "%-18s  %-22s  %s\n", "FINGERPRINT", "STATUS", "COMMENT")
	for _, id := range keys {
		comment := readPubComment(filepath.Join(store.Dir(), id.Hex()+".pub"))
		status := "trusted"
		if t, ok := store.RevokedAfter(id); ok {
			status = "revoked@" + t.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(env.Stdout, "%-18s  %-22s  %s\n", id.Hex(), status, comment)
	}
	fmt.Fprintf(env.Stdout, "\nTrust dir: %s\n", store.Dir())
	return nil
}

// TrustAdd reads a minisign .pub file from disk and persists it under
// <dataDir>/trust/<fingerprint>.pub. The CLI flow:
//
//  1. Parse the file (validates format before any side effect)
//  2. Print the fingerprint + comment we are about to trust
//  3. Prompt for explicit "yes" on the TTY — typo or autocomplete
//     cannot accidentally widen trust
//  4. Persist atomically (tmp+rename via DirTrustStore.Persist)
//
// Re-Adding the same fingerprint is allowed and clears any prior
// .revoked sidecar — operators interpret this as "I deliberately
// re-trust this key from now".
func TrustAdd(env TrustEnv, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("trust add: missing pub-file argument")
	}
	pubFile := args[0]
	comment := ""
	for i := 1; i < len(args); i++ {
		if args[i] == "--comment" && i+1 < len(args) {
			comment = args[i+1]
			i++
		}
	}

	raw, err := os.ReadFile(pubFile)
	if err != nil {
		return fmt.Errorf("trust add: read %s: %w", pubFile, err)
	}
	pub, err := envelope.ParsePublicKeyFile(raw)
	if err != nil {
		return fmt.Errorf("trust add: parse %s: %w", pubFile, err)
	}

	store, err := loadTrustStore(env.TrustDir)
	if err != nil {
		return err
	}

	// If the key already exists, surface that fact in the prompt so
	// the operator knows they may be silently clearing a revoked sidecar.
	wasPresent := false
	wasRevoked := time.Time{}
	if _, ok, _ := store.Lookup(pub.ID); ok {
		wasPresent = true
		if t, rev := store.RevokedAfter(pub.ID); rev {
			wasRevoked = t
		}
	}

	if comment == "" {
		comment = strings.TrimSpace(extractUntrustedComment(raw))
	}

	fmt.Fprintf(env.Stdout, "Add key %s to %s ?\n", pub.ID.Hex(), store.Dir())
	if comment != "" {
		fmt.Fprintf(env.Stdout, "  Comment: %s\n", comment)
	}
	if wasPresent {
		fmt.Fprintln(env.Stdout, "  Note: a key with this fingerprint is ALREADY trusted; re-Add will refresh the file in place.")
	}
	if !wasRevoked.IsZero() {
		fmt.Fprintf(env.Stdout, "  Note: existing operator-set revocation @ %s will be cleared.\n",
			wasRevoked.UTC().Format(time.RFC3339))
	}

	if err := requireConfirm(env, "Type 'yes' to add: "); err != nil {
		return err
	}
	if err := store.Persist(pub, comment); err != nil {
		return fmt.Errorf("trust add: persist: %w", err)
	}
	fmt.Fprintf(env.Stdout, "Added %s. Run 'alf restart' for the daemon to pick it up.\n", pub.ID.Hex())
	return nil
}

// TrustRemove deletes <fingerprint>.pub and any companion .revoked
// from the trust dir. The fingerprint is the 16-hex-char KeyID.
//
// The running daemon does NOT pick up the removal until the next
// restart; the message line at the end reminds the operator. If the
// fingerprint is not in the store, exits with a typed error so a
// typo doesn't silently no-op.
func TrustRemove(env TrustEnv, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("trust remove: missing fingerprint argument")
	}
	id, err := parseFingerprint(args[0])
	if err != nil {
		return fmt.Errorf("trust remove: %w", err)
	}
	store, err := loadTrustStore(env.TrustDir)
	if err != nil {
		return err
	}
	if _, ok, _ := store.Lookup(id); !ok {
		return fmt.Errorf("trust remove: no key with fingerprint %s in %s", id.Hex(), store.Dir())
	}

	fmt.Fprintf(env.Stdout, "Remove key %s from %s ?\n", id.Hex(), store.Dir())
	comment := readPubComment(filepath.Join(store.Dir(), id.Hex()+".pub"))
	if comment != "" {
		fmt.Fprintf(env.Stdout, "  Comment: %s\n", comment)
	}
	if err := requireConfirm(env, "Type 'yes' to remove: "); err != nil {
		return err
	}
	if err := store.PersistRemove(id); err != nil {
		return fmt.Errorf("trust remove: %w", err)
	}
	fmt.Fprintf(env.Stdout, "Removed %s. Run 'alf restart' for the daemon to pick it up.\n", id.Hex())
	return nil
}

// TrustRevoke records a not-valid-after timestamp for the given
// fingerprint. The pubkey file remains in place (the key is still
// "in the store" for lookup purposes); the .revoked sidecar pins the
// boundary that envelope.Verify reads via the Revoker interface.
//
// Default revocation timestamp is "now" (env.Now). Operators may
// override with --at <RFC3339> when they are recording a compromise
// known to have started earlier than this command's invocation.
func TrustRevoke(env TrustEnv, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("trust revoke: missing fingerprint argument")
	}
	id, err := parseFingerprint(args[0])
	if err != nil {
		return fmt.Errorf("trust revoke: %w", err)
	}
	when := env.Now().UTC()
	for i := 1; i < len(args); i++ {
		if args[i] == "--at" && i+1 < len(args) {
			t, err := time.Parse(time.RFC3339, args[i+1])
			if err != nil {
				return fmt.Errorf("trust revoke: --at %q: %w", args[i+1], err)
			}
			when = t.UTC()
			i++
		}
	}

	store, err := loadTrustStore(env.TrustDir)
	if err != nil {
		return err
	}
	if _, ok, _ := store.Lookup(id); !ok {
		return fmt.Errorf("trust revoke: no key with fingerprint %s in %s", id.Hex(), store.Dir())
	}

	fmt.Fprintf(env.Stdout, "Revoke key %s as of %s ?\n", id.Hex(), when.Format(time.RFC3339))
	fmt.Fprintln(env.Stdout, "  Bundles signed at or after that timestamp will be rejected.")
	if err := requireConfirm(env, "Type 'yes' to revoke: "); err != nil {
		return err
	}
	if err := store.PersistRevoke(id, when); err != nil {
		return fmt.Errorf("trust revoke: %w", err)
	}
	fmt.Fprintf(env.Stdout, "Revoked %s @ %s. Run 'alf restart' for the daemon to pick it up.\n",
		id.Hex(), when.Format(time.RFC3339))
	return nil
}

// loadTrustStore opens the dir-backed trust store at the env's
// TrustDir and runs Load(). A missing directory is treated as
// "empty store" (matches DirTrustStore.Load semantics) so first
// boot of the CLI does not require an explicit init.
func loadTrustStore(dir string) (*envelope.DirTrustStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("trust: no trust dir resolved (set ALF_DIR or run from install)")
	}
	store := envelope.NewDirTrustStore(dir)
	if err := store.Load(); err != nil {
		return nil, fmt.Errorf("trust: load %s: %w", dir, err)
	}
	return store, nil
}

// requireConfirm prompts the operator for an exact "yes" response.
// Refuses on non-TTY input — mutating trust state is the canonical
// prompt-injection target this boundary exists to block.
func requireConfirm(env TrustEnv, prompt string) error {
	if env.IsTerminal != nil && !env.IsTerminal() {
		return ErrNonInteractive
	}
	fmt.Fprint(env.Stdout, prompt)
	r := bufio.NewReader(env.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("trust: read confirmation: %w", err)
	}
	if strings.TrimSpace(line) != "yes" {
		return fmt.Errorf("trust: aborted (operator did not type 'yes')")
	}
	return nil
}

// parseFingerprint accepts a 16-hex-char KeyID with optional 0x
// prefix or surrounding whitespace. Case-insensitive. Anything else
// is rejected before the trust store is even opened.
func parseFingerprint(s string) (envelope.KeyID, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if len(s) != 16 {
		return envelope.KeyID{}, fmt.Errorf("fingerprint must be 16 hex chars, got %d", len(s))
	}
	var id envelope.KeyID
	for i := 0; i < 8; i++ {
		hi, ok1 := unhexNybble(s[i*2])
		lo, ok2 := unhexNybble(s[i*2+1])
		if !ok1 || !ok2 {
			return envelope.KeyID{}, fmt.Errorf("fingerprint %q: not hex at position %d", s, i*2)
		}
		id[i] = hi<<4 | lo
	}
	return id, nil
}

func unhexNybble(c byte) (byte, bool) {
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

// readPubComment best-effort extracts the first untrusted-comment
// line from a minisign pubkey file at path. Returns empty string on
// any failure (the comment is informational; never load-bearing).
func readPubComment(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(extractUntrustedComment(raw))
}

func extractUntrustedComment(raw []byte) string {
	const prefix = "untrusted comment:"
	for _, line := range strings.SplitN(string(raw), "\n", 3) {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

