package controlcenter

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	appTokenTTL    = 5 * time.Minute
	appTokenKeyLen = 32
)

// AppTokenStore issues and validates short-lived HMAC-signed tokens for
// sandboxed app iframes. Tokens are self-contained (no server-side state)
// and scoped to a specific app slug.
//
// Format: base64(slug_len(1) + slug + expiry_unix(8) + hmac_sha256(32))
type AppTokenStore struct {
	mu  sync.Mutex
	key []byte // HMAC signing key
}

// NewAppTokenStore creates a store with a random signing key.
func NewAppTokenStore() (*AppTokenStore, error) {
	key := make([]byte, appTokenKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate app token key: %w", err)
	}
	return &AppTokenStore{key: key}, nil
}

// Issue creates a token scoped to the given slug, valid for appTokenTTL.
func (s *AppTokenStore) Issue(slug string) string {
	if len(slug) > 255 {
		slug = slug[:255]
	}
	expiry := time.Now().Add(appTokenTTL)

	// Build payload: slug_len(1) + slug + expiry_unix_sec(8)
	payload := make([]byte, 0, 1+len(slug)+8)
	payload = append(payload, byte(len(slug)))
	payload = append(payload, []byte(slug)...)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(expiry.Unix()))
	payload = append(payload, buf...)

	// Sign
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	sig := mac.Sum(nil)

	return base64.RawURLEncoding.EncodeToString(append(payload, sig...))
}

// Validate checks a token and returns the slug it was issued for.
// Returns ("", false) if the token is invalid or expired.
func (s *AppTokenStore) Validate(token string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", false
	}

	// Minimum: 1 (slug_len) + 0 (slug) + 8 (expiry) + 32 (hmac)
	if len(raw) < 41 {
		return "", false
	}

	slugLen := int(raw[0])
	if len(raw) != 1+slugLen+8+32 {
		return "", false
	}

	payload := raw[:1+slugLen+8]
	sig := raw[1+slugLen+8:]

	// Verify HMAC
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return "", false
	}

	// Check expiry
	expiry := int64(binary.BigEndian.Uint64(payload[1+slugLen:]))
	if time.Now().Unix() > expiry {
		return "", false
	}

	return string(payload[1 : 1+slugLen]), true
}

// ValidateForSlug checks that the token is valid AND scoped to the given slug.
func (s *AppTokenStore) ValidateForSlug(token, slug string) bool {
	tokenSlug, ok := s.Validate(token)
	return ok && tokenSlug == slug
}

// extractAppBearerToken extracts a Bearer token from the Authorization header.
func extractAppBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return ""
}
