package crl

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// DefaultGracePeriod is the §7.7 N-day fail-safe: after this much
// time without a successful refresh, the daemon logs an offline
// warning but continues operating. 30 days matches the spec.
const DefaultGracePeriod = 30 * 24 * time.Hour

// DefaultInterval is how often Run() ticks. 6 hours balances "fresh
// CRL within a day of upstream publishing" against "don't hammer the
// CDN". Operators can override.
const DefaultInterval = 6 * time.Hour

// Applier is the trust-store seam the Refresher writes into. The
// concrete implementation is *envelope.MemoryTrustStore.ApplyCRL,
// extracted as an interface for test injection.
type Applier interface {
	ApplyCRL(c *envelope.CRL)
}

// TickResult summarises one refresh cycle: where the CRL came from,
// how stale the cache was, how many entries were applied. Callers
// (daemon log, status endpoint) consume this to surface state.
type TickResult struct {
	// FromSource = true if the upstream Source served fresh bytes.
	// false = fell back to the on-disk cache.
	FromSource bool

	// CacheStale = true if the cache (or fresh fetch) is older than
	// GracePeriod. Always false when FromSource = true.
	CacheStale bool

	// LastFetched is the moment the bytes currently applied were
	// originally retrieved from upstream. On FromSource=true that's
	// "now"; on cache-fallback that's whatever the meta says.
	LastFetched time.Time

	// EntryCount is len(crl.Entries) in the applied CRL.
	EntryCount int

	// AppliedAt is when this Tick wrote into the store.
	AppliedAt time.Time
}

// Refresher pulls signed CRLs from Source, caches them, and applies
// them to Store. Failure modes:
//   - Source unavailable + Cache present → apply cache, log warning.
//     If cache age > GracePeriod, log offline-fail-safe warning.
//   - Source unavailable + no Cache → log; do not error out (first
//     boot offline is recoverable on next Tick).
//   - Source malformed (sig invalid, bad JSON) → reject; do NOT fall
//     back to the cache (active-mis-serve is stronger than absence).
//
// Concurrency: Tick is goroutine-safe via mu. Run starts a single
// goroutine that calls Tick every Interval until ctx is done.
type Refresher struct {
	Source       Source
	Cache        Cache
	ReleasePub   envelope.PublicKey
	Store        Applier
	Interval     time.Duration
	GracePeriod  time.Duration
	Now          func() time.Time
	Logf         func(string, ...any)

	mu sync.Mutex
}

// Tick executes one refresh cycle. Returns the TickResult on success
// (including "fell back to cache" — that's still a success from the
// daemon's POV) and an error only when neither source nor cache
// could yield an applicable CRL. Errors are non-fatal; the caller
// (daemon boot, periodic loop) logs and proceeds.
func (r *Refresher) Tick(ctx context.Context) (TickResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	logf := r.logger()

	// Try the live source first.
	raw, srcErr := r.fetchFromSource(ctx)
	if srcErr == nil {
		crl, parseErr := envelope.ParseSignedCRL(raw, r.ReleasePub)
		if parseErr != nil {
			// Active mis-serve. Do not fall back to cache.
			logf("[crl] source returned malformed CRL: %v", parseErr)
			return TickResult{}, fmt.Errorf("%w: %w", ErrSourceMalformed, parseErr)
		}
		// Persist before applying so a crash post-apply doesn't lose
		// the bytes we just trusted.
		if r.Cache != nil {
			if err := r.Cache.Save(raw, now); err != nil {
				logf("[crl] cache save failed (non-fatal): %v", err)
			}
		}
		r.Store.ApplyCRL(crl)
		logf("[crl] refreshed from source: %d entries, issued_at=%s", len(crl.Entries), crl.IssuedAt.Format(time.RFC3339))
		return TickResult{
			FromSource:  true,
			LastFetched: now,
			EntryCount:  len(crl.Entries),
			AppliedAt:   now,
		}, nil
	}

	// Source failed. Log and try the cache.
	logf("[crl] source fetch failed: %v", srcErr)
	if errors.Is(srcErr, ErrSourceMalformed) {
		// Already logged above; surface as an error without
		// touching the cache (cache, if present, is still valid).
		return TickResult{}, srcErr
	}

	if r.Cache == nil {
		return TickResult{}, fmt.Errorf("%w: no cache configured", ErrSourceUnavailable)
	}

	cachedRaw, lastFetched, ok, cacheErr := r.Cache.Load()
	if cacheErr != nil {
		logf("[crl] cache load failed: %v", cacheErr)
	}
	if !ok {
		// First boot offline OR corrupt cache. Either way, the
		// daemon proceeds without applying any CRL — operator-set
		// revocations still work via Revoke(). Surface to caller.
		return TickResult{}, fmt.Errorf("%w: source down and no usable cache", ErrSourceUnavailable)
	}

	crl, parseErr := envelope.ParseSignedCRL(cachedRaw, r.ReleasePub)
	if parseErr != nil {
		// Cache survived chmod but the release key changed since
		// it was written, OR the cache was tampered with. Treat
		// as corrupt and proceed empty.
		logf("[crl] cache CRL fails verification (release key rotation?): %v", parseErr)
		return TickResult{}, fmt.Errorf("%w: cache verify: %w", ErrSourceUnavailable, parseErr)
	}

	stale := false
	grace := r.gracePeriod()
	age := now.Sub(lastFetched)
	if age >= grace {
		stale = true
		logf("[crl] OFFLINE FAIL-SAFE: cached CRL is %s old (grace = %s); continuing per §7.7",
			age.Round(time.Hour), grace.Round(time.Hour))
	} else {
		logf("[crl] applied cached CRL: %d entries, age=%s",
			len(crl.Entries), age.Round(time.Minute))
	}

	r.Store.ApplyCRL(crl)
	return TickResult{
		FromSource:  false,
		CacheStale:  stale,
		LastFetched: lastFetched,
		EntryCount:  len(crl.Entries),
		AppliedAt:   now,
	}, nil
}

// Run drives Tick on Interval until ctx is cancelled. The first
// Tick fires immediately so boot doesn't wait Interval before any
// CRL is applied. Tick errors are logged, never fatal — Run loops
// until ctx is done.
func (r *Refresher) Run(ctx context.Context) {
	logf := r.logger()
	interval := r.interval()

	if _, err := r.Tick(ctx); err != nil {
		logf("[crl] initial tick: %v", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := r.Tick(ctx); err != nil {
				logf("[crl] tick: %v", err)
			}
		}
	}
}

func (r *Refresher) fetchFromSource(ctx context.Context) ([]byte, error) {
	if r.Source == nil {
		return nil, fmt.Errorf("%w: no source configured", ErrSourceUnavailable)
	}
	return r.Source.Fetch(ctx)
}

func (r *Refresher) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Refresher) interval() time.Duration {
	if r.Interval <= 0 {
		return DefaultInterval
	}
	return r.Interval
}

func (r *Refresher) gracePeriod() time.Duration {
	if r.GracePeriod <= 0 {
		return DefaultGracePeriod
	}
	return r.GracePeriod
}

func (r *Refresher) logger() func(string, ...any) {
	if r.Logf != nil {
		return r.Logf
	}
	return func(string, ...any) {}
}
