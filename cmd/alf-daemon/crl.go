package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alamparelli/alf/internal/capability/crl"
	"github.com/alamparelli/alf/internal/capability/envelope"
)

// crlSubsystem bundles the CRL refresher + clock-skew monitor so
// main() has a single object to defer-cancel on shutdown.
type crlSubsystem struct {
	cancel context.CancelFunc
}

// Close stops the refresher + skew-monitor goroutines. Safe on nil.
func (c *crlSubsystem) Close() {
	if c == nil || c.cancel == nil {
		return
	}
	c.cancel()
}

// setupCRL wires the §7.7 + §8 revocation pipeline: clock-sanity
// check at boot, CRL refresher applying signed CRLs to the WASM
// trust store, wall-clock skew monitor in background.
//
// Degrades gracefully:
//
//   - No release pubkey embedded (dev build / fresh checkout)
//     → skip CRL refresher entirely. Operator-set Revoke() still
//     works via the admin path.
//
//   - No CRL URL configured (env var ALF_CRL_URL empty)
//     → skip refresher. Cache-only mode is meaningless without a
//     source to fetch from on first boot.
//
//   - System clock more than 1y before build → REFUSE to boot
//     (returns error; main() escalates to log.Fatal).
//
// All other failures (source down, cache corrupt, etc.) are
// recovered by the Refresher per §7.7 fail-safe.
func setupCRL(ctx context.Context, dataDir string, store *envelope.MemoryTrustStore, logf func(string, ...any)) (*crlSubsystem, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}

	// Clock-sanity boot check — refuses if system clock is wildly
	// past relative to the binary's build time.
	if buildAt, ok := envelope.BuildTime(); ok {
		if err := envelope.CheckBootClock(time.Now(), buildAt); err != nil {
			return nil, fmt.Errorf("clock-sanity: %w", err)
		}
		logf("[clock] boot check ok (build=%s, now=%s)",
			buildAt.Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	} else {
		logf("[clock] dev build (no buildTime ldflag) — boot clock check skipped")
	}

	subCtx, cancel := context.WithCancel(ctx)
	sys := &crlSubsystem{cancel: cancel}

	// Background skew monitor — runs regardless of CRL availability.
	go envelope.MonitorClockSkew(subCtx, time.Hour, envelope.DefaultSkewThreshold, time.Now, logf)

	// CRL refresher: needs both the release pubkey (embedded) AND a
	// fetch URL (operator-configured). Either missing → degrade to
	// "operator-set Revoke only" mode and log once.
	pub, err := envelope.ReleasePublicKey()
	if err != nil {
		if errors.Is(err, envelope.ErrNoReleaseKey) {
			logf("[crl] release pubkey not embedded in this build — CRL distribution disabled (operator-set Revoke() still works)")
			return sys, nil
		}
		return sys, fmt.Errorf("crl: load release pubkey: %w", err)
	}

	url := os.Getenv("ALF_CRL_URL")
	if url == "" {
		logf("[crl] ALF_CRL_URL not set — CRL distribution disabled (release-key fingerprint=%s)", pub.ID.Hex())
		return sys, nil
	}
	// SEC-007: enforce HTTPS at boot rather than letting the first
	// Tick surface the misconfiguration. A plaintext URL on a public
	// host is a deployment bug — refuse to start so the operator
	// notices immediately.
	if err := crl.ValidateCRLURL(url); err != nil {
		return sys, fmt.Errorf("crl: ALF_CRL_URL refused: %w", err)
	}

	cacheDir := filepath.Join(dataDir, "crl")
	cache := &crl.FileCache{Dir: cacheDir}
	source := &crl.HTTPSource{URL: url}

	refresher := &crl.Refresher{
		Source:      source,
		Cache:       cache,
		ReleasePub:  pub,
		Store:       store,
		Interval:    crl.DefaultInterval,
		GracePeriod: crl.DefaultGracePeriod,
		Now:         time.Now,
		Logf:        logf,
	}

	go refresher.Run(subCtx)
	logf("[crl] refresher started: url=%s release-key=%s interval=%s grace=%s",
		url, pub.ID.Hex(), crl.DefaultInterval, crl.DefaultGracePeriod)
	return sys, nil
}
