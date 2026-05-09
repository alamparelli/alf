package envelope

import (
	"errors"
	"testing"
)

// TestMarketplacePublicKey_EmptyFileIsErrNoMarketplaceKey pins the
// dev-build path: a fresh checkout has an empty
// marketplace_pubkey.minisign, so MarketplacePublicKey() returns
// ErrNoMarketplaceKey rather than panicking. The marketplace
// subsystem branches on this to refuse install attempts with a
// clear error rather than silently falling back to unsigned
// install.
func TestMarketplacePublicKey_EmptyFileIsErrNoMarketplaceKey(t *testing.T) {
	_, err := MarketplacePublicKey()
	if !errors.Is(err, ErrNoMarketplaceKey) {
		t.Errorf("got %v, want ErrNoMarketplaceKey on empty embedded file", err)
	}
}
