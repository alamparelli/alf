package envelope

import (
	_ "embed"
	"errors"
	"strings"
)

// release_pubkey.minisign is the alf release public key, embedded at
// build time. The corresponding private key lives on alf release
// infrastructure and never ships in any binary.
//
// On a fresh-from-Git checkout the file is empty (no key generated
// yet for this branch). ReleasePublicKey() returns ok=false in that
// case, and the daemon's CRL refresher logs a one-time warning
// before degrading to "operator-set Revoke() only" (no upstream
// CRL distribution path).
//
// To populate it for a homelab signing flow, see scripts/gen-release-key.sh
// — which writes the pubkey here and the privkey to dev-secrets/.
//
//go:embed release_pubkey.minisign
var releasePubkeyFile []byte

// ErrNoReleaseKey signals that the embedded release pubkey is absent
// (dev builds, fresh checkout). Daemon callers log + degrade
// gracefully — they don't refuse to boot.
var ErrNoReleaseKey = errors.New("envelope: no alf release pubkey embedded in this build")

// ReleasePublicKey returns the embedded pubkey, or ErrNoReleaseKey
// if the file is empty / malformed (dev build, no key yet generated).
func ReleasePublicKey() (PublicKey, error) {
	trimmed := strings.TrimSpace(string(releasePubkeyFile))
	if trimmed == "" {
		return PublicKey{}, ErrNoReleaseKey
	}
	pub, err := ParsePublicKeyFile(releasePubkeyFile)
	if err != nil {
		return PublicKey{}, errors.Join(ErrNoReleaseKey, err)
	}
	return pub, nil
}
