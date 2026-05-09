package envelope

import (
	_ "embed"
	"errors"
	"strings"
)

// marketplace_pubkey.minisign is the alf-marketplace public key,
// embedded at build time. The corresponding private key lives on
// alf-marketplace infrastructure and never ships in any binary.
//
// On a fresh-from-Git checkout the file is empty. MarketplacePublicKey()
// returns ok=false in that case, and the daemon's marketplace
// subsystem treats every install attempt as "registry not yet upgraded
// for v0.8.0 signed bundles" — refused with a clear error rather than
// silently falling back to unsigned install.
//
// To populate it for a homelab marketplace flow, generate a keypair
// (e.g. via `go run ./cmd/alf-release-keygen` adapted for marketplace,
// or any Ed25519/minisign keygen) and write the pubkey here. The
// private key stays on the marketplace signing host.
//
//go:embed marketplace_pubkey.minisign
var marketplacePubkeyFile []byte

// ErrNoMarketplaceKey signals that the embedded marketplace pubkey
// is absent (dev builds, fresh checkout). The marketplace subsystem
// refuses to install bundles in this state — there is no trust
// anchor to verify against. Tier 4 (third-party) signers still work
// once an operator runs `alf trust add` on the publisher's key.
var ErrNoMarketplaceKey = errors.New("envelope: no alf-marketplace pubkey embedded in this build")

// MarketplacePublicKey returns the embedded marketplace pubkey, or
// ErrNoMarketplaceKey if the file is empty / malformed (dev build,
// no key yet generated). Daemon callers degrade to "marketplace
// install disabled, operator must alf trust add manually" rather
// than refusing to boot.
func MarketplacePublicKey() (PublicKey, error) {
	trimmed := strings.TrimSpace(string(marketplacePubkeyFile))
	if trimmed == "" {
		return PublicKey{}, ErrNoMarketplaceKey
	}
	pub, err := ParsePublicKeyFile(marketplacePubkeyFile)
	if err != nil {
		return PublicKey{}, errors.Join(ErrNoMarketplaceKey, err)
	}
	return pub, nil
}
