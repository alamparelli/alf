// POC for ticket #387: signing bundles with Go stdlib Ed25519 in a format
// that the stock `minisign` CLI can verify. Goal of the POC is interop —
// a sysadmin or operator must be able to run `minisign -V -p pubkey -m
// bundle` against an artefact the alf daemon produced, without any alf
// tooling on the path.
//
// Scope kept intentionally narrow:
//   - Ed25519 keypair generated with crypto/ed25519
//   - Pubkey file in minisign format (unencrypted, as minisign does)
//   - Signature file in minisign pre-hashed format ("ED" algorithm)
//   - Go verifier round-trips the POC's own output
//
// Out of scope for the POC:
//   - Encrypted secret key file (minisign stores it encrypted with scrypt;
//     production alf stores it in vault user-scope per #395, not on disk
//     in a .key file)
//   - CLI surface (production alf sign / alf verify live in internal/cli
//     once the spec lands; the POC exposes subcommands here only to drive
//     the interop test)
//
// Build: go build -o trust-poc ./technical/poc/trust-minisign-compat
// Run: see testdata/run.sh
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// Minisign pre-hashed algorithm identifier. Written as the first two bytes
// of the on-wire signature blob. The plain "Ed" variant signs raw bytes
// and only works for small inputs; "ED" signs the BLAKE2b-512 hash of the
// input and is what every modern minisign installation produces and
// expects for real payloads.
var algoPrehashed = [2]byte{'E', 'D'}

// algoPlain is the legacy signature algorithm (signs raw data, no hash).
// Used only to decode foreign pubkey files that might carry it — we
// never produce plain-algorithm signatures ourselves.
var algoPlain = [2]byte{'E', 'd'}

// On-wire signature: 2-byte algo || 8-byte key ID || 64-byte Ed25519 sig.
type signatureBlob struct {
	Algo  [2]byte
	KeyID [8]byte
	Sig   [ed25519.SignatureSize]byte
}

// On-wire pubkey: 2-byte algo || 8-byte key ID || 32-byte Ed25519 pubkey.
type pubkeyBlob struct {
	Algo   [2]byte
	KeyID  [8]byte
	PubKey [ed25519.PublicKeySize]byte
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "keygen":
		keygenCmd(os.Args[2:])
	case "sign":
		signCmd(os.Args[2:])
	case "verify":
		verifyCmd(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `trust-poc — minisign-compatible Ed25519 for #387

Subcommands:
  keygen -pub <out.pub> -sec <out.sec.raw>        Generate keypair
  sign   -sec <sec.raw> -m <file> [-c "trusted"]  Sign file, writes <file>.minisig
  verify -pub <pub> -m <file>                     Verify <file>.minisig`)
}

func keygenCmd(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	pubPath := fs.String("pub", "alf-poc.pub", "output pubkey path (minisign format)")
	secPath := fs.String("sec", "alf-poc.sec.raw", "output secret key path (raw, for POC only — prod stores in vault)")
	_ = fs.Parse(args)

	pub, sec, err := ed25519.GenerateKey(rand.Reader)
	check(err, "generate keypair")

	var kid [8]byte
	_, err = rand.Read(kid[:])
	check(err, "generate key ID")

	pkb := pubkeyBlob{Algo: algoPlain, KeyID: kid}
	copy(pkb.PubKey[:], pub)
	writeMinisignPubkey(*pubPath, pkb, "minisign public key "+hexKeyID(kid))

	// Store secret with key ID prepended so sign() can emit the right ID.
	secBytes := append(kid[:], sec...)
	check(os.WriteFile(*secPath, secBytes, 0o600), "write secret key")

	fmt.Printf("keypair written:\n  public:  %s (minisign format)\n  secret:  %s (raw, POC-only — prod in vault)\n  key ID:  %s\n", *pubPath, *secPath, hexKeyID(kid))
}

func signCmd(args []string) {
	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	secPath := fs.String("sec", "alf-poc.sec.raw", "secret key path")
	msgPath := fs.String("m", "", "file to sign")
	trusted := fs.String("c", "alf POC signature", "trusted comment (covered by global signature)")
	_ = fs.Parse(args)

	if *msgPath == "" {
		fatal("sign: -m is required")
	}

	secBytes, err := os.ReadFile(*secPath)
	check(err, "read secret key")
	if len(secBytes) != 8+ed25519.PrivateKeySize {
		fatal(fmt.Sprintf("sign: unexpected secret key length %d", len(secBytes)))
	}
	var kid [8]byte
	copy(kid[:], secBytes[:8])
	sec := ed25519.PrivateKey(secBytes[8:])

	payload, err := os.ReadFile(*msgPath)
	check(err, "read message")

	// Pre-hash: minisign "ED" algorithm signs BLAKE2b-512 of the payload.
	hash := blake2b.Sum512(payload)

	// First signature: Ed25519 over the hash.
	rawSig := ed25519.Sign(sec, hash[:])
	var sigb signatureBlob
	sigb.Algo = algoPrehashed
	sigb.KeyID = kid
	copy(sigb.Sig[:], rawSig)

	// Global signature: signs (raw_signature || trusted_comment_bytes).
	// minisign -V will fail closed if this second signature does not
	// round-trip, so we MUST produce it correctly.
	globalInput := append([]byte(nil), rawSig...)
	globalInput = append(globalInput, []byte(*trusted)...)
	globalSig := ed25519.Sign(sec, globalInput)

	sigPath := *msgPath + ".minisig"
	writeMinisignSig(sigPath, sigb, *trusted, globalSig)
	fmt.Printf("signed: %s → %s\n  trusted-comment: %q\n", *msgPath, sigPath, *trusted)
}

func verifyCmd(args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubPath := fs.String("pub", "alf-poc.pub", "public key path (minisign format)")
	msgPath := fs.String("m", "", "file to verify")
	_ = fs.Parse(args)

	if *msgPath == "" {
		fatal("verify: -m is required")
	}

	pkb, err := readMinisignPubkey(*pubPath)
	check(err, "read pubkey")

	payload, err := os.ReadFile(*msgPath)
	check(err, "read message")

	sigb, trusted, globalSig, err := readMinisignSig(*msgPath + ".minisig")
	check(err, "read signature")

	if sigb.KeyID != pkb.KeyID {
		fatal(fmt.Sprintf("verify: key ID mismatch (sig=%s pub=%s)", hexKeyID(sigb.KeyID), hexKeyID(pkb.KeyID)))
	}

	hash := blake2b.Sum512(payload)
	if !ed25519.Verify(pkb.PubKey[:], hash[:], sigb.Sig[:]) {
		fatal("verify: signature does not match payload")
	}
	globalInput := append([]byte(nil), sigb.Sig[:]...)
	globalInput = append(globalInput, []byte(trusted)...)
	if !ed25519.Verify(pkb.PubKey[:], globalInput, globalSig) {
		fatal("verify: global signature invalid (trusted comment tampered or wrong key)")
	}
	fmt.Printf("verify: OK\n  file:             %s\n  key ID:           %s\n  trusted-comment:  %q\n", *msgPath, hexKeyID(sigb.KeyID), trusted)
}

// -----------------------------------------------------------------------------
// Minisign file format helpers.
//
// Pubkey file:
//   untrusted comment: <string>
//   <base64(pubkeyBlob)>
//
// Signature file:
//   untrusted comment: <string>
//   <base64(signatureBlob)>
//   trusted comment: <string>
//   <base64(globalSig)>
// -----------------------------------------------------------------------------

func writeMinisignPubkey(path string, p pubkeyBlob, untrustedComment string) {
	buf := make([]byte, 0, 42)
	buf = append(buf, p.Algo[:]...)
	buf = append(buf, p.KeyID[:]...)
	buf = append(buf, p.PubKey[:]...)
	contents := "untrusted comment: " + untrustedComment + "\n" + base64.StdEncoding.EncodeToString(buf) + "\n"
	check(os.WriteFile(path, []byte(contents), 0o644), "write pubkey")
}

func readMinisignPubkey(path string) (pubkeyBlob, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return pubkeyBlob{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		return pubkeyBlob{}, fmt.Errorf("pubkey: expected at least 2 lines, got %d", len(lines))
	}
	decoded, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil {
		return pubkeyBlob{}, fmt.Errorf("pubkey: base64 decode: %w", err)
	}
	if len(decoded) != 2+8+ed25519.PublicKeySize {
		return pubkeyBlob{}, fmt.Errorf("pubkey: expected %d bytes, got %d", 2+8+ed25519.PublicKeySize, len(decoded))
	}
	var p pubkeyBlob
	copy(p.Algo[:], decoded[0:2])
	copy(p.KeyID[:], decoded[2:10])
	copy(p.PubKey[:], decoded[10:])
	if p.Algo != algoPlain && p.Algo != algoPrehashed {
		return pubkeyBlob{}, fmt.Errorf("pubkey: unknown algorithm %q", string(p.Algo[:]))
	}
	return p, nil
}

func writeMinisignSig(path string, s signatureBlob, trustedComment string, globalSig []byte) {
	buf := make([]byte, 0, 2+8+ed25519.SignatureSize)
	buf = append(buf, s.Algo[:]...)
	buf = append(buf, s.KeyID[:]...)
	buf = append(buf, s.Sig[:]...)
	contents := "untrusted comment: signature from alf POC key " + hexKeyID(s.KeyID) + "\n" +
		base64.StdEncoding.EncodeToString(buf) + "\n" +
		"trusted comment: " + trustedComment + "\n" +
		base64.StdEncoding.EncodeToString(globalSig) + "\n"
	check(os.WriteFile(path, []byte(contents), 0o644), "write signature")
}

func readMinisignSig(path string) (signatureBlob, string, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return signatureBlob{}, "", nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 4 {
		return signatureBlob{}, "", nil, fmt.Errorf("signature: expected 4 lines, got %d", len(lines))
	}
	sigDecoded, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil {
		return signatureBlob{}, "", nil, fmt.Errorf("signature: decode blob: %w", err)
	}
	if len(sigDecoded) != 2+8+ed25519.SignatureSize {
		return signatureBlob{}, "", nil, fmt.Errorf("signature: expected %d bytes, got %d", 2+8+ed25519.SignatureSize, len(sigDecoded))
	}
	var s signatureBlob
	copy(s.Algo[:], sigDecoded[0:2])
	copy(s.KeyID[:], sigDecoded[2:10])
	copy(s.Sig[:], sigDecoded[10:])

	const trustedPrefix = "trusted comment: "
	if !strings.HasPrefix(lines[2], trustedPrefix) {
		return signatureBlob{}, "", nil, fmt.Errorf("signature: missing trusted comment line")
	}
	trusted := strings.TrimPrefix(lines[2], trustedPrefix)

	globalSig, err := base64.StdEncoding.DecodeString(lines[3])
	if err != nil {
		return signatureBlob{}, "", nil, fmt.Errorf("signature: decode global sig: %w", err)
	}
	if len(globalSig) != ed25519.SignatureSize {
		return signatureBlob{}, "", nil, fmt.Errorf("signature: global sig length %d, want %d", len(globalSig), ed25519.SignatureSize)
	}
	return s, trusted, globalSig, nil
}

func hexKeyID(kid [8]byte) string {
	const hex = "0123456789ABCDEF"
	out := make([]byte, 16)
	for i, b := range kid {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out)
}

func check(err error, what string) {
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", what, err))
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}
