package controlcenter

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

const (
	vaultMagic   = "ALFVAULT1"
	saltLen      = 32
	nonceLen     = 12
	keyLen       = 32
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
)

var (
	ErrInvalidMagic  = errors.New("invalid vault format: missing ALFVAULT1 header")
	ErrDataTooShort  = errors.New("invalid vault data: too short")
	ErrDecryptFailed = errors.New("decryption failed: wrong password or corrupted data")
)

// headerLen is the fixed-size prefix: magic + salt + nonce.
var headerLen = len(vaultMagic) + saltLen + nonceLen

// deriveKey derives a 256-bit key from password and salt using Argon2id.
func deriveKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, keyLen)
}

// EncryptVaultExport encrypts jsonData with AES-256-GCM using a key derived
// from password via Argon2id. The output format is:
//
//	ALFVAULT1 (8 bytes) | salt (32 bytes) | nonce (12 bytes) | ciphertext+tag
func EncryptVaultExport(jsonData []byte, password string) ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	key := deriveKey(password, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, jsonData, nil)

	out := make([]byte, 0, headerLen+len(ciphertext))
	out = append(out, []byte(vaultMagic)...)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)

	return out, nil
}

// DecryptVaultExport decrypts data produced by EncryptVaultExport.
// Returns ErrInvalidMagic if the header is missing, ErrDecryptFailed on
// authentication failure (wrong password or tampered data).
func DecryptVaultExport(encrypted []byte, password string) ([]byte, error) {
	if len(encrypted) < headerLen {
		return nil, ErrDataTooShort
	}

	magic := string(encrypted[:len(vaultMagic)])
	if magic != vaultMagic {
		return nil, ErrInvalidMagic
	}

	offset := len(vaultMagic)
	salt := encrypted[offset : offset+saltLen]
	offset += saltLen
	nonce := encrypted[offset : offset+nonceLen]
	offset += nonceLen
	ciphertext := encrypted[offset:]

	if len(ciphertext) == 0 {
		return nil, ErrDataTooShort
	}

	key := deriveKey(password, salt)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptFailed
	}

	return plaintext, nil
}
