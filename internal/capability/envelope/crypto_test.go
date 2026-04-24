package envelope

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func mustGenKey(t *testing.T) (PublicKey, PrivateKey) {
	t.Helper()
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

func TestGenerateKey_IDsMatch(t *testing.T) {
	pub, priv := mustGenKey(t)
	if pub.ID != priv.ID {
		t.Errorf("pub ID %s != priv ID %s", pub.ID.Hex(), priv.ID.Hex())
	}
	if len(pub.Key) != ed25519.PublicKeySize {
		t.Errorf("pub key size %d", len(pub.Key))
	}
	if len(priv.Key) != ed25519.PrivateKeySize {
		t.Errorf("priv key size %d", len(priv.Key))
	}
}

func TestKeyID_HexIsUpperCase16Chars(t *testing.T) {
	pub, _ := mustGenKey(t)
	hex := pub.ID.Hex()
	if len(hex) != 16 {
		t.Errorf("hex length %d, want 16", len(hex))
	}
	if hex != strings.ToUpper(hex) {
		t.Errorf("hex not uppercase: %q", hex)
	}
}

func TestSign_ProducesExpectedSize(t *testing.T) {
	_, priv := mustGenKey(t)
	sig, err := Sign(priv, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	want := 2 + 8 + ed25519.SignatureSize
	if len(sig) != want {
		t.Errorf("sig size %d, want %d", len(sig), want)
	}
	// First two bytes must be the prehashed algo prefix.
	if !bytes.Equal(sig[:2], algoPrehashed[:]) {
		t.Errorf("sig algo prefix %q, want %q", string(sig[:2]), string(algoPrehashed[:]))
	}
	// Next 8 bytes must be the signer's key ID.
	if !bytes.Equal(sig[2:10], priv.ID[:]) {
		t.Errorf("sig key ID mismatch")
	}
}

func TestSignVerify_RoundTrip(t *testing.T) {
	pub, priv := mustGenKey(t)
	data := []byte("signed payload")

	sig, err := Sign(priv, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignature(pub, data, sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_TamperedPayloadRejected(t *testing.T) {
	pub, priv := mustGenKey(t)
	data := []byte("original")
	sig, _ := Sign(priv, data)
	if err := VerifySignature(pub, []byte("tampered"), sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("want ErrSignatureInvalid, got %v", err)
	}
}

func TestVerify_WrongKeyRejected(t *testing.T) {
	_, priv := mustGenKey(t)
	otherPub, _ := mustGenKey(t)
	data := []byte("x")
	sig, _ := Sign(priv, data)
	if err := VerifySignature(otherPub, data, sig); !errors.Is(err, ErrKeyIDMismatch) {
		t.Fatalf("want ErrKeyIDMismatch, got %v", err)
	}
}

func TestVerify_MalformedSigRejected(t *testing.T) {
	pub, _ := mustGenKey(t)
	if err := VerifySignature(pub, []byte("x"), []byte("too-short")); !errors.Is(err, ErrSignatureMalformed) {
		t.Fatalf("want ErrSignatureMalformed, got %v", err)
	}
}

func TestVerify_AlgorithmSubstitutionRejected(t *testing.T) {
	// §7.10.4 scheme-substitution: an envelope claiming a different algo
	// with an Ed25519-shaped signature must be rejected because we
	// dispatch on the algo prefix BEFORE any cryptographic work.
	pub, priv := mustGenKey(t)
	data := []byte("x")
	sig, _ := Sign(priv, data)

	// Overwrite algo to something unsupported.
	sig[0] = 'R'
	sig[1] = 'S'
	if err := VerifySignature(pub, data, sig); !errors.Is(err, ErrAlgorithmUnsupported) {
		t.Fatalf("want ErrAlgorithmUnsupported, got %v", err)
	}
}

func TestEncodeAndParsePublicKeyFile(t *testing.T) {
	pub, _ := mustGenKey(t)
	file, err := EncodePublicKeyFile(pub, "test key "+pub.ID.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(file, []byte("untrusted comment: ")) {
		t.Errorf("pubkey file missing comment line: %q", file)
	}
	parsed, err := ParsePublicKeyFile(file)
	if err != nil {
		t.Fatalf("ParsePublicKeyFile: %v", err)
	}
	if parsed.ID != pub.ID {
		t.Errorf("ID mismatch after round-trip")
	}
	if !bytes.Equal(parsed.Key, pub.Key) {
		t.Errorf("key bytes mismatch after round-trip")
	}
}

func TestParsePublicKeyFile_MalformedRejected(t *testing.T) {
	cases := map[string][]byte{
		"single line":    []byte("untrusted comment: only one line"),
		"bad base64":     []byte("untrusted comment: x\n@@@not-base64@@@"),
		"short payload":  []byte("untrusted comment: x\n" + base64.StdEncoding.EncodeToString([]byte("short"))),
		"bad algo":       makeBadPubkeyAlgo(),
	}
	for name, raw := range cases {
		_, err := ParsePublicKeyFile(raw)
		if !errors.Is(err, ErrPubkeyMalformed) {
			t.Errorf("%s: want ErrPubkeyMalformed, got %v", name, err)
		}
	}
}

func makeBadPubkeyAlgo() []byte {
	blob := make([]byte, 2+8+ed25519.PublicKeySize)
	copy(blob[0:2], []byte{'X', 'X'}) // unknown algo
	_, _ = rand.Read(blob[2:])
	text := "untrusted comment: x\n" + base64.StdEncoding.EncodeToString(blob) + "\n"
	return []byte(text)
}

func TestEncodeAndParseSignatureFile(t *testing.T) {
	pub, priv := mustGenKey(t)
	data := []byte("hello world")
	sig, _ := Sign(priv, data)

	file, err := EncodeSignatureFile(priv, sig, "bundle hello-read@0.1.0")
	if err != nil {
		t.Fatal(err)
	}

	parsedSig, trusted, globalSig, err := ParseSignatureFile(file)
	if err != nil {
		t.Fatalf("ParseSignatureFile: %v", err)
	}
	if !bytes.Equal(parsedSig, sig) {
		t.Errorf("sig bytes mismatch after round-trip")
	}
	if trusted != "bundle hello-read@0.1.0" {
		t.Errorf("trusted comment = %q", trusted)
	}

	// Main sig still validates.
	if err := VerifySignature(pub, data, parsedSig); err != nil {
		t.Errorf("Verify main sig: %v", err)
	}
	// Global sig validates the trusted comment.
	if err := VerifyGlobalComment(pub, parsedSig, trusted, globalSig); err != nil {
		t.Errorf("VerifyGlobalComment: %v", err)
	}
}

func TestVerifyGlobalComment_TamperedRejected(t *testing.T) {
	pub, priv := mustGenKey(t)
	data := []byte("x")
	sig, _ := Sign(priv, data)
	file, _ := EncodeSignatureFile(priv, sig, "original comment")

	parsedSig, trusted, globalSig, _ := ParseSignatureFile(file)
	if trusted != "original comment" {
		t.Fatalf("setup: trusted=%q", trusted)
	}

	if err := VerifyGlobalComment(pub, parsedSig, "tampered comment", globalSig); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("want ErrSignatureInvalid on tampered comment, got %v", err)
	}
}

func TestParseSignatureFile_MalformedRejected(t *testing.T) {
	cases := map[string][]byte{
		"three lines": []byte("a\nb\nc"),
		"bad base64":  []byte("c1\n@@@\ntrusted comment: x\n\n"),
	}
	for name, raw := range cases {
		_, _, _, err := ParseSignatureFile(raw)
		if !errors.Is(err, ErrSigFileMalformed) {
			t.Errorf("%s: want ErrSigFileMalformed, got %v", name, err)
		}
	}
}
