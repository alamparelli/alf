package controlcenter

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		plaintext []byte
		password  string
	}{
		{"simple", []byte("hello world"), "secret"},
		{"empty plaintext", []byte(""), "secret"},
		{"unicode password", []byte(`{"key":"value"}`), "pässwörd🔑"},
		{"long payload", bytes.Repeat([]byte("x"), 1<<16), "pw"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encrypted, err := EncryptVaultExport(tc.plaintext, tc.password)
			if err != nil {
				t.Fatalf("EncryptVaultExport: %v", err)
			}

			decrypted, err := DecryptVaultExport(encrypted, tc.password)
			if err != nil {
				t.Fatalf("DecryptVaultExport: %v", err)
			}

			if !bytes.Equal(decrypted, tc.plaintext) {
				t.Errorf("round-trip mismatch: got %q, want %q", decrypted, tc.plaintext)
			}
		})
	}
}

func TestDecryptWrongPassword(t *testing.T) {
	plaintext := []byte("sensitive data")
	encrypted, err := EncryptVaultExport(plaintext, "correct")
	if err != nil {
		t.Fatalf("EncryptVaultExport: %v", err)
	}

	_, err = DecryptVaultExport(encrypted, "wrong")
	if !errors.Is(err, ErrDecryptFailed) {
		t.Errorf("expected ErrDecryptFailed, got %v", err)
	}
}

func TestDecryptInvalidMagic(t *testing.T) {
	// Build a blob that is long enough but has a wrong magic prefix.
	data := make([]byte, headerLen+32)
	copy(data, "NOTVALID!")

	_, err := DecryptVaultExport(data, "any")
	if !errors.Is(err, ErrInvalidMagic) {
		t.Errorf("expected ErrInvalidMagic, got %v", err)
	}
}

func TestDecryptDataTooShort(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"one byte", []byte("x")},
		{"just under header", make([]byte, headerLen-1)},
		{"header only no ciphertext", func() []byte {
			// Valid magic + filler for salt+nonce, but zero-length ciphertext.
			b := make([]byte, headerLen)
			copy(b, vaultMagic)
			return b
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecryptVaultExport(tc.data, "pw")
			if !errors.Is(err, ErrDataTooShort) {
				t.Errorf("expected ErrDataTooShort, got %v", err)
			}
		})
	}
}

func TestEncryptProducesDifferentCiphertext(t *testing.T) {
	plaintext := []byte("deterministic input")
	password := "same-password"

	a, err := EncryptVaultExport(plaintext, password)
	if err != nil {
		t.Fatalf("first encrypt: %v", err)
	}

	b, err := EncryptVaultExport(plaintext, password)
	if err != nil {
		t.Fatalf("second encrypt: %v", err)
	}

	if bytes.Equal(a, b) {
		t.Error("two encryptions of the same input produced identical output; salt/nonce should differ")
	}
}
