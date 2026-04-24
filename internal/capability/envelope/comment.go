package envelope

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// TrustedComment is the structured form of the minisign trusted-comment
// field. Stop-gap carrier for the §7.10.3 envelope-record fields until
// the full JSON record is adopted; today we pack the same values into
// a whitespace-separated key=value list protected by minisign's global
// signature (so no post-sign tampering is possible).
//
// Canonical format:
//
//	"bundle <id> bundle_sha256=<hex> signed_at=<RFC3339-UTC>"
//
// The leading "bundle <id>" is a human-readable prefix shown by
// minisign -V; everything after is key=value pairs the verifier parses.
//
// Adding a field is a backwards-compatible change: BuildTrustedComment
// always emits all known fields in a stable order; ParseTrustedComment
// tolerates unknown keys (skips them so future versions don't break
// old verifiers). Removing a field bumps a hypothetical comment-format
// version; today we don't need one.
type TrustedComment struct {
	BundleID   string    // human prefix ("hello-read@0.1.0" etc.)
	BundleHash string    // sha256 hex of the bundle bytes
	SignedAt   time.Time // UTC, second precision (RFC 3339)
}

// BuildTrustedComment serialises the struct into the canonical
// whitespace-separated form. BundleHash may be empty when signing a
// manifest with no accompanying artefact (skill kind). SignedAt
// defaults to time.Now().UTC() if zero, but callers should always pass
// an explicit value so test fixtures are deterministic.
func BuildTrustedComment(tc TrustedComment) string {
	if tc.SignedAt.IsZero() {
		tc.SignedAt = time.Now().UTC()
	} else {
		tc.SignedAt = tc.SignedAt.UTC()
	}

	var b strings.Builder
	if tc.BundleID == "" {
		b.WriteString("bundle")
	} else {
		b.WriteString("bundle ")
		b.WriteString(tc.BundleID)
	}
	if tc.BundleHash != "" {
		b.WriteString(" bundle_sha256=")
		b.WriteString(tc.BundleHash)
	}
	b.WriteString(" signed_at=")
	b.WriteString(tc.SignedAt.Format(time.RFC3339))
	return b.String()
}

// ErrTrustedCommentMalformed is returned when a required field is
// absent or unparseable. The verify pipeline maps this to a load
// rejection — a signature with no signed_at cannot support CRL
// time-bound revocation (§7.7), so we fail closed.
var ErrTrustedCommentMalformed = errors.New("envelope: trusted comment malformed")

// ParseTrustedComment walks the key=value fields and returns the
// structured form. Unknown keys are silently skipped (forward-compat
// with future comment-format extensions). Missing signed_at is a
// hard error — the CRL path (#396) depends on it.
func ParseTrustedComment(comment string) (TrustedComment, error) {
	var tc TrustedComment

	i := 0
	// Optional "bundle <id>" prefix.
	if strings.HasPrefix(comment, "bundle ") {
		rest := comment[len("bundle "):]
		end := strings.IndexByte(rest, ' ')
		if end < 0 {
			tc.BundleID = rest
			i = len(comment)
		} else {
			tc.BundleID = rest[:end]
			i = len("bundle ") + end + 1
		}
	} else if comment == "bundle" {
		i = len(comment)
	}

	// Walk key=value tokens.
	for i < len(comment) {
		for i < len(comment) && (comment[i] == ' ' || comment[i] == '\t') {
			i++
		}
		start := i
		for i < len(comment) && comment[i] != ' ' && comment[i] != '\t' {
			i++
		}
		token := comment[start:i]
		if token == "" {
			continue
		}
		eq := strings.IndexByte(token, '=')
		if eq <= 0 {
			// Not a key=value pair — skip (forward-compat).
			continue
		}
		key, val := token[:eq], token[eq+1:]
		switch key {
		case "bundle_sha256":
			tc.BundleHash = val
		case "signed_at":
			t, err := time.Parse(time.RFC3339, val)
			if err != nil {
				return TrustedComment{}, fmt.Errorf("%w: signed_at=%q: %w", ErrTrustedCommentMalformed, val, err)
			}
			tc.SignedAt = t.UTC()
		default:
			// Unknown field — skip.
		}
	}

	if tc.SignedAt.IsZero() {
		return TrustedComment{}, fmt.Errorf("%w: missing signed_at field", ErrTrustedCommentMalformed)
	}
	return tc, nil
}
