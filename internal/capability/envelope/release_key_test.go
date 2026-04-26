package envelope

import (
	"errors"
	"testing"
)

// TestReleasePublicKey_EmptyFileIsErrNoReleaseKey pins the dev-build
// path: a fresh checkout has an empty release_pubkey.minisign, so
// ReleasePublicKey() returns ErrNoReleaseKey rather than panicking.
// The daemon's CRL refresher branches on this to log + degrade
// gracefully (operator-set Revoke still works).
func TestReleasePublicKey_EmptyFileIsErrNoReleaseKey(t *testing.T) {
	_, err := ReleasePublicKey()
	if !errors.Is(err, ErrNoReleaseKey) {
		t.Errorf("got %v, want ErrNoReleaseKey on empty embedded file", err)
	}
}
