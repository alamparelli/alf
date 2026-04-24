package envelope

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBuildTrustedComment_Full(t *testing.T) {
	tc := TrustedComment{
		BundleID:   "hello-read@0.1.0",
		BundleHash: "abc123",
		SignedAt:   time.Date(2026, 4, 24, 15, 30, 0, 0, time.UTC),
	}
	got := BuildTrustedComment(tc)
	want := "bundle hello-read@0.1.0 bundle_sha256=abc123 signed_at=2026-04-24T15:30:00Z"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestBuildTrustedComment_NoBundle(t *testing.T) {
	// A skill kind has no artefact → no bundle_sha256 field.
	tc := TrustedComment{
		BundleID: "my-skill@0.2.0",
		SignedAt: time.Date(2026, 4, 24, 15, 30, 0, 0, time.UTC),
	}
	got := BuildTrustedComment(tc)
	want := "bundle my-skill@0.2.0 signed_at=2026-04-24T15:30:00Z"
	if got != want {
		t.Errorf("\ngot:  %q\nwant: %q", got, want)
	}
}

func TestBuildTrustedComment_ZeroSignedAtDefaultsToNow(t *testing.T) {
	tc := TrustedComment{BundleID: "x", BundleHash: "y"}
	before := time.Now().UTC()
	got := BuildTrustedComment(tc)
	after := time.Now().UTC()

	// Extract the signed_at value from the produced string.
	const marker = "signed_at="
	idx := strings.Index(got, marker)
	if idx < 0 {
		t.Fatalf("output lacks signed_at: %q", got)
	}
	timestamp := got[idx+len(marker):]
	parsed, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		t.Fatalf("parse default timestamp %q: %v", timestamp, err)
	}
	if parsed.Before(before.Truncate(time.Second)) || parsed.After(after.Add(time.Second)) {
		t.Errorf("default timestamp %v outside [%v, %v]", parsed, before, after)
	}
}

func TestBuildTrustedComment_NormalisesToUTC(t *testing.T) {
	tz, _ := time.LoadLocation("Europe/Paris")
	tc := TrustedComment{
		BundleID: "x",
		SignedAt: time.Date(2026, 4, 24, 17, 30, 0, 0, tz), // 15:30 UTC
	}
	got := BuildTrustedComment(tc)
	if !strings.Contains(got, "2026-04-24T15:30:00Z") {
		t.Errorf("timestamp not normalised to UTC Z: %q", got)
	}
}

func TestParseTrustedComment_RoundTrip(t *testing.T) {
	orig := TrustedComment{
		BundleID:   "hello@0.1.0",
		BundleHash: "e3b0c442",
		SignedAt:   time.Date(2026, 4, 24, 15, 30, 0, 0, time.UTC),
	}
	raw := BuildTrustedComment(orig)
	parsed, err := ParseTrustedComment(raw)
	if err != nil {
		t.Fatalf("ParseTrustedComment: %v", err)
	}
	if parsed.BundleID != orig.BundleID {
		t.Errorf("BundleID round-trip failed")
	}
	if parsed.BundleHash != orig.BundleHash {
		t.Errorf("BundleHash round-trip failed")
	}
	if !parsed.SignedAt.Equal(orig.SignedAt) {
		t.Errorf("SignedAt round-trip: got %v, want %v", parsed.SignedAt, orig.SignedAt)
	}
}

func TestParseTrustedComment_MissingSignedAt(t *testing.T) {
	// Pre-signed_at legacy comment — the verifier must refuse it so a
	// pre-CRL bundle can never be accepted once CRL is wired (#396).
	_, err := ParseTrustedComment("bundle hello bundle_sha256=abc")
	if !errors.Is(err, ErrTrustedCommentMalformed) {
		t.Fatalf("want ErrTrustedCommentMalformed, got %v", err)
	}
	if !strings.Contains(err.Error(), "signed_at") {
		t.Errorf("error should mention signed_at, got %v", err)
	}
}

func TestParseTrustedComment_MalformedSignedAt(t *testing.T) {
	_, err := ParseTrustedComment("bundle x signed_at=not-a-date")
	if !errors.Is(err, ErrTrustedCommentMalformed) {
		t.Fatalf("want ErrTrustedCommentMalformed, got %v", err)
	}
}

func TestParseTrustedComment_UnknownFieldsSkipped(t *testing.T) {
	// Forward-compat: unknown key=value tokens are skipped, not rejected.
	// An older verifier reading a newer signer's comment keeps working.
	raw := "bundle x bundle_sha256=abc signed_at=2026-04-24T15:30:00Z future_field=xyz"
	tc, err := ParseTrustedComment(raw)
	if err != nil {
		t.Fatalf("unknown field should be skipped, got %v", err)
	}
	if tc.BundleHash != "abc" {
		t.Errorf("BundleHash lost to unknown-field parser: %q", tc.BundleHash)
	}
}

func TestParseTrustedComment_NoBundleID(t *testing.T) {
	// A comment without the "bundle <id>" prefix still parses as long
	// as signed_at is present. Edge case — production signers always
	// emit the prefix, but the parser is lenient.
	raw := "signed_at=2026-04-24T15:30:00Z"
	tc, err := ParseTrustedComment(raw)
	if err != nil {
		t.Fatalf("bare signed_at should parse, got %v", err)
	}
	if tc.BundleID != "" {
		t.Errorf("BundleID=%q, want empty", tc.BundleID)
	}
}
