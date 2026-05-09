package crl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// signedCRL helper — produces a signed-CRL JSON blob with the given
// entries, using a fresh release keypair returned for verification.
func signedCRL(t *testing.T, issuedAt time.Time, entries []envelope.CRLEntry) (envelope.PublicKey, []byte) {
	t.Helper()
	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	c := envelope.CRL{
		Version:    envelope.CRLEnvelopeVersion,
		IssuedAt:   issuedAt,
		NextUpdate: issuedAt.Add(30 * 24 * time.Hour),
		Entries:    entries,
	}
	raw, err := envelope.EncodeSignedCRL(c, priv)
	if err != nil {
		t.Fatal(err)
	}
	return pub, raw
}

// mockSource is a Source whose Fetch behaviour is set per-test.
type mockSource struct {
	mu       sync.Mutex
	response []byte
	err      error
	calls    int
}

func (m *mockSource) Fetch(_ context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

// captureLogger collects log lines so tests assert on what was said.
type captureLogger struct {
	mu    sync.Mutex
	lines []string
}

func (c *captureLogger) Logf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

func (c *captureLogger) contains(substr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.lines {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

// TestRefresher_FetchSuccess pins the happy path: source returns a
// fresh CRL → applied to store + cached + no warning.
func TestRefresher_FetchSuccess(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	pub, raw := signedCRL(t, now, []envelope.CRLEntry{
		{KeyID: mustKeyID(t, "0000000000000001"), NotValidAfter: now.Add(-time.Hour)},
	})

	store := envelope.NewMemoryTrustStore()
	cache := &FileCache{Dir: t.TempDir()}
	src := &mockSource{response: raw}

	r := &Refresher{
		Source:     src,
		Cache:      cache,
		ReleasePub: pub,
		Store:      store,
		Now:        func() time.Time { return now },
	}

	res, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if !res.FromSource {
		t.Error("FromSource should be true")
	}
	if res.EntryCount != 1 {
		t.Errorf("EntryCount: got %d want 1", res.EntryCount)
	}

	// Store applied?
	if _, ok := store.RevokedAfter(mustKeyID(t, "0000000000000001")); !ok {
		t.Error("CRL entry not surfaced via RevokedAfter")
	}

	// Cache written?
	cachedRaw, lastFetched, ok, _ := cache.Load()
	if !ok {
		t.Fatal("cache not written")
	}
	if string(cachedRaw) != string(raw) {
		t.Error("cache bytes differ from source bytes")
	}
	if !lastFetched.Equal(now) {
		t.Errorf("lastFetched: got %s want %s", lastFetched, now)
	}
}

// TestRefresher_FetchFailFallsBackToCache pins the offline path:
// source unavailable + cache present → cache applied with warning.
func TestRefresher_FetchFailFallsBackToCache(t *testing.T) {
	cacheTime := time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC) // 6 days later
	pub, raw := signedCRL(t, cacheTime, []envelope.CRLEntry{
		{KeyID: mustKeyID(t, "0000000000000001"), NotValidAfter: cacheTime.Add(-time.Hour)},
	})

	cacheDir := t.TempDir()
	cache := &FileCache{Dir: cacheDir}
	if err := cache.Save(raw, cacheTime, cacheTime); err != nil {
		t.Fatal(err)
	}

	store := envelope.NewMemoryTrustStore()
	src := &mockSource{err: ErrSourceUnavailable}
	logger := &captureLogger{}

	r := &Refresher{
		Source:     src,
		Cache:      cache,
		ReleasePub: pub,
		Store:      store,
		Now:        func() time.Time { return now },
		Logf:       logger.Logf,
	}

	res, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.FromSource {
		t.Error("FromSource should be false (cache fallback)")
	}
	if res.CacheStale {
		t.Error("CacheStale should be false at 6 days < 30-day grace")
	}
	if !logger.contains("source fetch failed") {
		t.Error("expected source-failure log line")
	}
	if _, ok := store.RevokedAfter(mustKeyID(t, "0000000000000001")); !ok {
		t.Error("cached CRL entry not applied")
	}
}

// TestRefresher_StaleButNotExpired pins behaviour just below the
// grace period: cache is old but not over the threshold → applied,
// stale=false.
func TestRefresher_StaleButNotExpired(t *testing.T) {
	cacheTime := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	now := cacheTime.Add(29 * 24 * time.Hour) // 1 day under grace
	pub, raw := signedCRL(t, cacheTime, nil)

	cache := &FileCache{Dir: t.TempDir()}
	if err := cache.Save(raw, cacheTime, cacheTime); err != nil {
		t.Fatal(err)
	}

	r := &Refresher{
		Source:     &mockSource{err: ErrSourceUnavailable},
		Cache:      cache,
		ReleasePub: pub,
		Store:      envelope.NewMemoryTrustStore(),
		Now:        func() time.Time { return now },
	}
	res, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.CacheStale {
		t.Error("CacheStale should be false (29d < 30d grace)")
	}
}

// TestRefresher_ExpiredStillOperates pins the §7.7 fail-safe: cache
// older than grace period, source down — daemon STILL applies the
// cache, logs offline-warning, never aborts.
func TestRefresher_ExpiredStillOperates(t *testing.T) {
	cacheTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := cacheTime.Add(45 * 24 * time.Hour) // 15d past grace
	pub, raw := signedCRL(t, cacheTime, []envelope.CRLEntry{
		{KeyID: mustKeyID(t, "0000000000000001"), NotValidAfter: cacheTime},
	})

	cache := &FileCache{Dir: t.TempDir()}
	if err := cache.Save(raw, cacheTime, cacheTime); err != nil {
		t.Fatal(err)
	}

	store := envelope.NewMemoryTrustStore()
	logger := &captureLogger{}
	r := &Refresher{
		Source:     &mockSource{err: ErrSourceUnavailable},
		Cache:      cache,
		ReleasePub: pub,
		Store:      store,
		Now:        func() time.Time { return now },
		Logf:       logger.Logf,
	}

	res, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick should not error on stale cache: %v", err)
	}
	if !res.CacheStale {
		t.Error("CacheStale should be true at 45d > 30d grace")
	}
	if !logger.contains("OFFLINE FAIL-SAFE") {
		t.Error("expected fail-safe warning log")
	}
	if _, ok := store.RevokedAfter(mustKeyID(t, "0000000000000001")); !ok {
		t.Error("stale-but-still-valid cache entry not applied")
	}
}

// TestRefresher_NoSourceNoCache pins first-boot-offline: nothing to
// apply, but Tick does NOT panic and the daemon proceeds. Operator-
// set Revoke() still works as a fallback channel.
func TestRefresher_NoSourceNoCache(t *testing.T) {
	pub, _ := signedCRL(t, time.Now(), nil)
	r := &Refresher{
		Source:     &mockSource{err: ErrSourceUnavailable},
		Cache:      &FileCache{Dir: t.TempDir()},
		ReleasePub: pub,
		Store:      envelope.NewMemoryTrustStore(),
		Now:        time.Now,
	}
	_, err := r.Tick(context.Background())
	if !errors.Is(err, ErrSourceUnavailable) {
		t.Errorf("got %v, want ErrSourceUnavailable", err)
	}
}

// TestRefresher_MalformedSourceDoesNotFallback pins the active-mis-
// serve guard: a CRL with bad signature must NOT silently fall
// back to the cache. The cache is still trustworthy, but the
// daemon shouldn't reward a hostile upstream by silently using
// older state.
func TestRefresher_MalformedSourceDoesNotFallback(t *testing.T) {
	cacheTime := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 4, 26, 0, 0, 0, 0, time.UTC)
	pub, validRaw := signedCRL(t, cacheTime, []envelope.CRLEntry{
		{KeyID: mustKeyID(t, "0000000000000001"), NotValidAfter: cacheTime},
	})

	cache := &FileCache{Dir: t.TempDir()}
	if err := cache.Save(validRaw, cacheTime, cacheTime); err != nil {
		t.Fatal(err)
	}

	// Source returns a CRL signed by a DIFFERENT key.
	otherPub, _, _ := envelope.GenerateKey()
	_ = otherPub
	_, otherRaw := signedCRL(t, now, nil) // signed by yet another key

	store := envelope.NewMemoryTrustStore()
	r := &Refresher{
		Source:     &mockSource{response: otherRaw},
		Cache:      cache,
		ReleasePub: pub, // expects pub, but source signed with another
		Store:      store,
		Now:        func() time.Time { return now },
	}
	_, err := r.Tick(context.Background())
	if !errors.Is(err, ErrSourceMalformed) {
		t.Errorf("got %v, want ErrSourceMalformed", err)
	}
	// Crucially, store should NOT have the cached CRL applied as a
	// silent fallback — bad source ≠ offline.
	if _, ok := store.RevokedAfter(mustKeyID(t, "0000000000000001")); ok {
		t.Error("malformed source should NOT trigger cache fallback")
	}
}

// TestFileCache_RoundTrip pins Save → Load symmetry: bytes and
// timestamp survive intact.
func TestFileCache_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := &FileCache{Dir: dir}
	want := []byte(`{"crl":{},"signature":"AAAA"}`)
	wantT := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	if err := c.Save(want, wantT, wantT); err != nil {
		t.Fatal(err)
	}
	got, gotT, ok, err := c.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("Load reported absent after Save")
	}
	if string(got) != string(want) {
		t.Errorf("bytes differ:\n  got %q\n want %q", got, want)
	}
	if !gotT.Equal(wantT) {
		t.Errorf("time differs: got %s want %s", gotT, wantT)
	}
}

// TestFileCache_AbsentReturnsNoError pins the first-boot path: an
// empty cache directory yields ok=false but err=nil. Refresher
// treats this as "no cache, try source again next tick".
func TestFileCache_AbsentReturnsNoError(t *testing.T) {
	c := &FileCache{Dir: t.TempDir()}
	_, _, ok, err := c.Load()
	if err != nil {
		t.Errorf("absent cache should return nil err, got %v", err)
	}
	if ok {
		t.Error("absent cache should return ok=false")
	}
}

// TestFileCache_PayloadMissingMetaIsCorrupt pins half-written
// state: payload file present but meta file absent (interrupted
// Save) is treated as corrupt so the Refresher logs and rebuilds.
func TestFileCache_PayloadMissingMetaIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := writeAtomic(filepath.Join(dir, cachePayloadName), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &FileCache{Dir: dir}
	_, _, _, err := c.Load()
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Errorf("got %v, want ErrCacheCorrupt", err)
	}
}

// TestFileCache_PayloadSizeMismatchIsCorrupt pins the integrity
// check: if payload was truncated post-Save (bit-rot) but meta
// still claims the original size, treat as corrupt.
func TestFileCache_PayloadSizeMismatchIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	c := &FileCache{Dir: dir}
	if err := c.Save([]byte("hello world"), time.Now(), time.Time{}); err != nil {
		t.Fatal(err)
	}
	// Truncate the payload after Save.
	if err := writeAtomic(filepath.Join(dir, cachePayloadName), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := c.Load()
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Errorf("got %v, want ErrCacheCorrupt", err)
	}
}

// TestHTTPSource_HappyPath pins the production transport: served
// bytes round-trip through Fetch.
func TestHTTPSource_HappyPath(t *testing.T) {
	want := []byte(`{"crl":{},"signature":"AAAA"}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(want)
	}))
	defer srv.Close()

	got, err := (&HTTPSource{URL: srv.URL}).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q want %q", got, want)
	}
}

// TestHTTPSource_500IsUnavailable pins that an upstream 5xx maps
// to ErrSourceUnavailable (transport-level), not Malformed.
func TestHTTPSource_500IsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := (&HTTPSource{URL: srv.URL}).Fetch(context.Background())
	if !errors.Is(err, ErrSourceUnavailable) {
		t.Errorf("got %v, want ErrSourceUnavailable", err)
	}
}

// TestHTTPSource_BodyCapEnforced pins the MaxCRLBytes guardrail
// against a hostile upstream serving multi-GB.
func TestHTTPSource_BodyCapEnforced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Send a body bigger than the cap.
		_, _ = w.Write(make([]byte, MaxCRLBytes+10))
	}))
	defer srv.Close()

	_, err := (&HTTPSource{URL: srv.URL}).Fetch(context.Background())
	if !errors.Is(err, ErrSourceMalformed) {
		t.Errorf("got %v, want ErrSourceMalformed (body cap)", err)
	}
}

// TestValidateCRLURL_AllowsHTTPS pins SEC-007: a public HTTPS URL
// is the production path and must always pass.
func TestValidateCRLURL_AllowsHTTPS(t *testing.T) {
	if err := ValidateCRLURL("https://crl.alf.org/v1.json"); err != nil {
		t.Errorf("HTTPS rejected: %v", err)
	}
}

// TestValidateCRLURL_RejectsPlaintextPublicHost pins SEC-007: a
// non-loopback HTTP URL must be refused — it would let any
// net-position attacker swap CRL bytes (combined with SEC-001
// replay this widens rollback risk).
func TestValidateCRLURL_RejectsPlaintextPublicHost(t *testing.T) {
	cases := []string{
		"http://crl.alf.org/v1.json",
		"http://example.com/crl",
		"http://10.0.0.1/crl", // RFC1918 still public-routable from the threat model POV
	}
	for _, u := range cases {
		err := ValidateCRLURL(u)
		if !errors.Is(err, ErrInsecureURL) {
			t.Errorf("%s: got %v, want ErrInsecureURL", u, err)
		}
	}
}

// TestValidateCRLURL_AllowsLoopbackHTTP pins the test/dev
// exemption: HTTP is OK for loopback addresses. httptest harnesses
// bind to 127.0.0.1 and rely on this exemption.
func TestValidateCRLURL_AllowsLoopbackHTTP(t *testing.T) {
	cases := []string{
		"http://127.0.0.1:8080/crl",
		"http://[::1]:8080/crl",
		"http://localhost:8080/crl",
	}
	for _, u := range cases {
		if err := ValidateCRLURL(u); err != nil {
			t.Errorf("%s: loopback HTTP rejected: %v", u, err)
		}
	}
}

// TestValidateCRLURL_RejectsUnknownScheme pins that schemes other
// than http/https (file, ftp, javascript, gopher, ...) are refused.
func TestValidateCRLURL_RejectsUnknownScheme(t *testing.T) {
	cases := []string{
		"file:///etc/passwd",
		"ftp://crl.alf.org/v1.json",
		"javascript:alert(1)",
		"gopher://crl.alf.org/",
	}
	for _, u := range cases {
		err := ValidateCRLURL(u)
		if !errors.Is(err, ErrInsecureURL) {
			t.Errorf("%s: got %v, want ErrInsecureURL", u, err)
		}
	}
}

// TestHTTPSource_RejectsPlaintextPublicURL pins that the runtime
// fetch path enforces the same scheme rule — a misconfigured
// HTTPSource that bypassed the daemon-boot ValidateCRLURL still
// refuses to fetch.
func TestHTTPSource_RejectsPlaintextPublicURL(t *testing.T) {
	src := &HTTPSource{URL: "http://crl.example.com/v1.json"}
	_, err := src.Fetch(context.Background())
	if !errors.Is(err, ErrInsecureURL) {
		t.Errorf("got %v, want ErrInsecureURL", err)
	}
}

func mustKeyID(t *testing.T, hex string) envelope.KeyID {
	t.Helper()
	id, err := envelope.ParseKeyIDHex(hex)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// signedCRLWithKey signs a CRL with a caller-supplied keypair so a
// single test can produce multiple legitimately-signed CRLs from the
// same release key — required for replay scenarios.
func signedCRLWithKey(t *testing.T, priv envelope.PrivateKey, issuedAt time.Time, entries []envelope.CRLEntry) []byte {
	t.Helper()
	c := envelope.CRL{
		Version:    envelope.CRLEnvelopeVersion,
		IssuedAt:   issuedAt,
		NextUpdate: issuedAt.Add(30 * 24 * time.Hour),
		Entries:    entries,
	}
	raw, err := envelope.EncodeSignedCRL(c, priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestRefresher_SourceReplayRejected pins SEC-001 on the source path:
// once a CRL with IssuedAt=T2 has been applied, an older but
// validly-signed CRL with IssuedAt=T1<T2 served by the source must be
// rejected as ErrCRLReplay. The store stays at T2's revocation set.
func TestRefresher_SourceReplayRejected(t *testing.T) {
	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	keyOld := mustKeyID(t, "0000000000000001")
	keyNew := mustKeyID(t, "0000000000000002")

	rawT1 := signedCRLWithKey(t, priv, t1, []envelope.CRLEntry{
		{KeyID: keyOld, NotValidAfter: t1},
	})
	rawT2 := signedCRLWithKey(t, priv, t2, []envelope.CRLEntry{
		{KeyID: keyOld, NotValidAfter: t1},
		{KeyID: keyNew, NotValidAfter: t2}, // newer CRL adds keyNew
	})

	store := envelope.NewMemoryTrustStore()
	cache := &FileCache{Dir: t.TempDir()}
	src := &mockSource{response: rawT2}
	logger := &captureLogger{}
	r := &Refresher{
		Source:     src,
		Cache:      cache,
		ReleasePub: pub,
		Store:      store,
		Now:        func() time.Time { return t2.Add(time.Hour) },
		Logf:       logger.Logf,
	}

	// First Tick applies T2.
	if _, err := r.Tick(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if _, ok := store.RevokedAfter(keyNew); !ok {
		t.Fatal("T2 not applied")
	}

	// Source now serves T1 (the replay). Same release key, valid sig.
	src.mu.Lock()
	src.response = rawT1
	src.mu.Unlock()

	_, err = r.Tick(context.Background())
	if !errors.Is(err, ErrCRLReplay) {
		t.Fatalf("got %v, want ErrCRLReplay", err)
	}
	if !logger.contains("rejected as replay") {
		t.Error("expected replay-rejected log line")
	}
	// Store must still carry the T2 revocation set — replay did NOT
	// roll back to T1.
	if _, ok := store.RevokedAfter(keyNew); !ok {
		t.Error("replay must not have rolled back T2's revocation of keyNew")
	}
}

// TestRefresher_HighWaterPersistsAcrossRestart pins that the SEC-001
// guarantee survives a daemon restart: a fresh Refresher reading the
// same on-disk cache picks up the high-water from cache meta and
// rejects an older CRL served by the source.
func TestRefresher_HighWaterPersistsAcrossRestart(t *testing.T) {
	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	now := t2.Add(time.Hour)

	keyOld := mustKeyID(t, "0000000000000001")
	rawT1 := signedCRLWithKey(t, priv, t1, []envelope.CRLEntry{{KeyID: keyOld, NotValidAfter: t1}})
	rawT2 := signedCRLWithKey(t, priv, t2, []envelope.CRLEntry{{KeyID: keyOld, NotValidAfter: t2}})

	cacheDir := t.TempDir()
	cache := &FileCache{Dir: cacheDir}

	// Run 1 — apply T2 via source. Cache + high-water persisted.
	{
		r := &Refresher{
			Source:     &mockSource{response: rawT2},
			Cache:      cache,
			ReleasePub: pub,
			Store:      envelope.NewMemoryTrustStore(),
			Now:        func() time.Time { return now },
		}
		if _, err := r.Tick(context.Background()); err != nil {
			t.Fatalf("seed tick: %v", err)
		}
	}

	// Run 2 — fresh Refresher (simulating daemon restart). Source now
	// serves T1 (the replay). High-water should be loaded from cache
	// meta on first Tick and reject the source.
	{
		store := envelope.NewMemoryTrustStore()
		r := &Refresher{
			Source:     &mockSource{response: rawT1},
			Cache:      &FileCache{Dir: cacheDir}, // fresh handle, same disk
			ReleasePub: pub,
			Store:      store,
			Now:        func() time.Time { return now.Add(time.Hour) },
		}
		_, err := r.Tick(context.Background())
		if !errors.Is(err, ErrCRLReplay) {
			t.Fatalf("got %v, want ErrCRLReplay across restart", err)
		}
	}
}

// TestRefresher_CacheReplayRejected pins SEC-001 on the cache path:
// if an attacker plants an older signed CRL into the on-disk cache
// dir AND the source is unavailable, the Refresher must reject the
// older cached CRL as a replay rather than silently downgrading.
func TestRefresher_CacheReplayRejected(t *testing.T) {
	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	now := t2.Add(time.Hour)

	keyOld := mustKeyID(t, "0000000000000001")
	rawT1 := signedCRLWithKey(t, priv, t1, []envelope.CRLEntry{{KeyID: keyOld, NotValidAfter: t1}})
	rawT2 := signedCRLWithKey(t, priv, t2, []envelope.CRLEntry{{KeyID: keyOld, NotValidAfter: t2}})

	cacheDir := t.TempDir()
	cache := &FileCache{Dir: cacheDir}

	// Seed: apply T2 from source so high-water = T2 in cache meta.
	{
		r := &Refresher{
			Source:     &mockSource{response: rawT2},
			Cache:      cache,
			ReleasePub: pub,
			Store:      envelope.NewMemoryTrustStore(),
			Now:        func() time.Time { return now },
		}
		if _, err := r.Tick(context.Background()); err != nil {
			t.Fatalf("seed tick: %v", err)
		}
	}

	// Attacker tampers cache: writes T1 payload but keeps the meta
	// file unchanged so the high-water stays at T2. Real attackers
	// would need OS-level write to the cache dir — this is the threat
	// model for SEC-001 cache replay.
	attackerCache := &FileCache{Dir: cacheDir}
	if err := writeAtomic(filepath.Join(cacheDir, cachePayloadName), rawT1, 0o600); err != nil {
		t.Fatal(err)
	}
	// Source unavailable so the refresher falls back to cache.
	logger := &captureLogger{}
	r := &Refresher{
		Source:     &mockSource{err: ErrSourceUnavailable},
		Cache:      attackerCache,
		ReleasePub: pub,
		Store:      envelope.NewMemoryTrustStore(),
		Now:        func() time.Time { return now.Add(time.Hour) },
		Logf:       logger.Logf,
	}
	_, err = r.Tick(context.Background())
	if !errors.Is(err, ErrCRLReplay) {
		t.Fatalf("got %v, want ErrCRLReplay on cache tamper", err)
	}
	if !logger.contains("cached CRL rejected as replay") {
		t.Error("expected cache-replay log line")
	}
}

// TestRefresher_EqualIssuedAtIdempotent pins that re-applying the
// same CRL (same IssuedAt) is a no-op — not a replay error. This is
// the legitimate boot-from-cache scenario where the daemon restarts,
// loads the high-water from cache meta, and re-applies the same CRL
// the cache holds.
func TestRefresher_EqualIssuedAtIdempotent(t *testing.T) {
	pub, priv, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	now := t1.Add(time.Hour)

	keyOld := mustKeyID(t, "0000000000000001")
	rawT1 := signedCRLWithKey(t, priv, t1, []envelope.CRLEntry{{KeyID: keyOld, NotValidAfter: t1}})

	cacheDir := t.TempDir()
	cache := &FileCache{Dir: cacheDir}

	// Seed cache via Source.
	{
		r := &Refresher{
			Source:     &mockSource{response: rawT1},
			Cache:      cache,
			ReleasePub: pub,
			Store:      envelope.NewMemoryTrustStore(),
			Now:        func() time.Time { return now },
		}
		if _, err := r.Tick(context.Background()); err != nil {
			t.Fatalf("seed tick: %v", err)
		}
	}

	// Restart: source unavailable, cache holds T1, high-water in meta
	// is also T1. Re-applying T1 must NOT be a replay error.
	store := envelope.NewMemoryTrustStore()
	r := &Refresher{
		Source:     &mockSource{err: ErrSourceUnavailable},
		Cache:      &FileCache{Dir: cacheDir},
		ReleasePub: pub,
		Store:      store,
		Now:        func() time.Time { return now.Add(time.Hour) },
	}
	if _, err := r.Tick(context.Background()); err != nil {
		t.Fatalf("idempotent re-apply rejected as %v", err)
	}
	if _, ok := store.RevokedAfter(keyOld); !ok {
		t.Error("idempotent re-apply did not populate store")
	}
}

// TestRefresher_OnApplyFiresAfterSourceApply pins that the OnApply
// callback fires once after a successful source-path ApplyCRL, and
// that the callback observes the post-apply trust store state.
//
// This is the daemon's hook for cascading revocations down to live
// Instances without waiting for SIGHUP — wired in #396 D2.
func TestRefresher_OnApplyFiresAfterSourceApply(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	keyID := mustKeyID(t, "0000000000000001")
	pub, raw := signedCRL(t, now, []envelope.CRLEntry{
		{KeyID: keyID, NotValidAfter: now.Add(-time.Hour)},
	})

	store := envelope.NewMemoryTrustStore()

	// Capture the store state at the moment OnApply runs — the
	// invariant under test is that the apply happens BEFORE the
	// callback, so the snapshot must already include the entry.
	var observed bool
	var fireCount int
	onApply := func() {
		fireCount++
		_, observed = store.RevokedAfter(keyID)
	}

	r := &Refresher{
		Source:     &mockSource{response: raw},
		Cache:      &FileCache{Dir: t.TempDir()},
		ReleasePub: pub,
		Store:      store,
		Now:        func() time.Time { return now },
		OnApply:    onApply,
	}
	if _, err := r.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if fireCount != 1 {
		t.Errorf("OnApply fired %d times, want 1", fireCount)
	}
	if !observed {
		t.Error("OnApply did not observe post-apply trust store state")
	}
}

// TestRefresher_OnApplyFiresAfterCachePath pins that the cache-
// fallback path also fires OnApply. Operators offline with a fresh
// CRL in cache must still see Instances cascade.
func TestRefresher_OnApplyFiresAfterCachePath(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	keyID := mustKeyID(t, "0000000000000001")
	pub, raw := signedCRL(t, now, []envelope.CRLEntry{
		{KeyID: keyID, NotValidAfter: now.Add(-time.Hour)},
	})

	cacheDir := t.TempDir()
	cache := &FileCache{Dir: cacheDir}

	// Seed the cache via a successful source tick first.
	{
		seedStore := envelope.NewMemoryTrustStore()
		seed := &Refresher{
			Source:     &mockSource{response: raw},
			Cache:      cache,
			ReleasePub: pub,
			Store:      seedStore,
			Now:        func() time.Time { return now },
		}
		if _, err := seed.Tick(context.Background()); err != nil {
			t.Fatalf("seed tick: %v", err)
		}
	}

	// Now go offline, fresh refresher, fresh store, cache load only.
	store := envelope.NewMemoryTrustStore()
	var fireCount int
	r := &Refresher{
		Source:     &mockSource{err: ErrSourceUnavailable},
		Cache:      &FileCache{Dir: cacheDir},
		ReleasePub: pub,
		Store:      store,
		Now:        func() time.Time { return now.Add(time.Hour) },
		OnApply:    func() { fireCount++ },
	}
	res, err := r.Tick(context.Background())
	if err != nil {
		t.Fatalf("offline tick: %v", err)
	}
	if res.FromSource {
		t.Error("expected FromSource=false (cache path)")
	}
	if fireCount != 1 {
		t.Errorf("OnApply fired %d times on cache path, want 1", fireCount)
	}
}

// TestRefresher_OnApplyDoesNotFireOnSourceFailure pins that a
// source error WITHOUT a successful cache fallback does not fire
// OnApply. Nothing was applied to the store; cascading would be
// noise (or a defect — looking at a stale snapshot).
func TestRefresher_OnApplyDoesNotFireOnSourceFailure(t *testing.T) {
	pub, _, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := envelope.NewMemoryTrustStore()

	var fireCount int
	r := &Refresher{
		Source:     &mockSource{err: ErrSourceUnavailable},
		Cache:      nil,
		ReleasePub: pub,
		Store:      store,
		Now:        func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) },
		OnApply:    func() { fireCount++ },
	}
	if _, err := r.Tick(context.Background()); err == nil {
		t.Fatal("expected ErrSourceUnavailable")
	}
	if fireCount != 0 {
		t.Errorf("OnApply fired %d times on source failure, want 0", fireCount)
	}
}

// TestRefresher_OnApplyDoesNotFireOnMalformedSource pins that an
// active mis-serve (signature invalid) does not fire OnApply. The
// store was not touched, so cascading would be wrong.
func TestRefresher_OnApplyDoesNotFireOnMalformedSource(t *testing.T) {
	pub, _, err := envelope.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	store := envelope.NewMemoryTrustStore()
	var fireCount int
	r := &Refresher{
		Source:     &mockSource{response: []byte("garbage not a CRL")},
		Cache:      nil,
		ReleasePub: pub,
		Store:      store,
		Now:        func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) },
		OnApply:    func() { fireCount++ },
	}
	if _, err := r.Tick(context.Background()); err == nil {
		t.Fatal("expected malformed-source error")
	}
	if fireCount != 0 {
		t.Errorf("OnApply fired %d times on malformed source, want 0", fireCount)
	}
}

// TestRefresher_NilOnApplyNoOps pins backward compatibility: an
// existing Refresher with no OnApply set must keep working. This
// guards the existing daemon-wiring tests against accidental
// breakage from the new field.
func TestRefresher_NilOnApplyNoOps(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	pub, raw := signedCRL(t, now, []envelope.CRLEntry{
		{KeyID: mustKeyID(t, "0000000000000001"), NotValidAfter: now.Add(-time.Hour)},
	})

	store := envelope.NewMemoryTrustStore()
	r := &Refresher{
		Source:     &mockSource{response: raw},
		Cache:      &FileCache{Dir: t.TempDir()},
		ReleasePub: pub,
		Store:      store,
		Now:        func() time.Time { return now },
		// OnApply: nil
	}
	if _, err := r.Tick(context.Background()); err != nil {
		t.Fatalf("tick with nil OnApply: %v", err)
	}
	// No assertion needed beyond "did not panic".
}
