// Command alf-release-keygen generates an alf release keypair for
// homelab CRL signing. Run once per environment:
//
//	go run ./cmd/alf-release-keygen
//
// Writes:
//
//	internal/capability/envelope/release_pubkey.minisign  (committed to git)
//	dev-secrets/release-key.priv                          (gitignored)
//
// The pubkey is embedded into alf-daemon at build time via go:embed.
// The privkey is used by alf release tooling to sign CRLs (next chunk).
//
// In production, the privkey lives on a hardened signing host (HSM
// or air-gapped machine), never on disk in this repo. dev-secrets/
// is the homelab dev path — fine for v0.8.0-beta, must be replaced
// before any v1.0 signing.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

func main() {
	pubPath := flag.String("pub", "internal/capability/envelope/release_pubkey.minisign", "path to write release pubkey (embedded into binary at build time)")
	privPath := flag.String("priv", "dev-secrets/release-key.priv", "path to write release privkey (gitignored)")
	force := flag.Bool("force", false, "overwrite existing files")
	flag.Parse()

	if !*force {
		for _, p := range []string{*pubPath, *privPath} {
			info, err := os.Stat(p)
			if err == nil && info.Size() > 0 {
				log.Fatalf("refusing to overwrite non-empty %s (use -force)", p)
			}
		}
	}

	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		log.Fatalf("generate key: %v", err)
	}

	pubBytes, err := envelope.EncodePublicKeyFile(pub, fmt.Sprintf("alf release pubkey (homelab) — generated %s", time.Now().UTC().Format(time.RFC3339)))
	if err != nil {
		log.Fatalf("encode pubkey: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(*pubPath), 0o755); err != nil {
		log.Fatalf("mkdir pub: %v", err)
	}
	if err := os.WriteFile(*pubPath, pubBytes, 0o644); err != nil {
		log.Fatalf("write pubkey: %v", err)
	}

	privBytes := append([]byte(fmt.Sprintf("# alf release privkey (homelab) — generated %s\n", time.Now().UTC().Format(time.RFC3339))), priv.Key...)
	if err := os.MkdirAll(filepath.Dir(*privPath), 0o700); err != nil {
		log.Fatalf("mkdir priv: %v", err)
	}
	if err := os.WriteFile(*privPath, privBytes, 0o600); err != nil {
		log.Fatalf("write privkey: %v", err)
	}

	fmt.Printf("alf release keypair generated:\n")
	fmt.Printf("  pub  → %s  (embedded at build time, KeyID=%s)\n", *pubPath, pub.ID.Hex())
	fmt.Printf("  priv → %s  (gitignored — keep secure, used by CRL signing)\n", *privPath)
}
