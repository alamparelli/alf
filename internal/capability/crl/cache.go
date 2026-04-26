package crl

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Cache is the persistent store of the last-known-good CRL. Tracks
// both the bytes and the moment they were successfully fetched —
// lastFetchedAt drives the §7.7 N-day fail-safe.
type Cache interface {
	// Load returns the cached CRL bytes and the lastFetched timestamp.
	// On first boot (no cache yet), returns ok=false with err=nil.
	// On corruption, returns ok=false with a typed error so the
	// Refresher can log and proceed (treats corrupt = absent).
	Load() (raw []byte, lastFetched time.Time, ok bool, err error)

	// Save replaces the cache with raw + fetchedAt + issuedAt. Atomic
	// from the reader's POV — partial writes never observed. issuedAt
	// is the IssuedAt of the CRL contained in raw; persisted so the
	// anti-replay high-water mark survives a daemon restart (SEC-001).
	Save(raw []byte, fetchedAt time.Time, issuedAt time.Time) error

	// LoadIssuedAt returns the IssuedAt of the most recent
	// successfully-applied CRL. Drives SEC-001 anti-replay: every
	// fetched/cached CRL must have IssuedAt strictly greater than
	// this high-water mark or it is rejected as a replay. Returns
	// zero time + nil on first boot (no prior apply).
	LoadIssuedAt() (time.Time, error)
}

// ErrCacheCorrupt signals that the cache file exists but its
// metadata or payload cannot be parsed. Refresher treats this as
// "no cache" rather than aborting, but logs the corruption.
var ErrCacheCorrupt = errors.New("crl: cache corrupt")

// FileCache stores the CRL as two files under Dir: crl.json (raw
// signed bytes the daemon downloaded) and crl.meta.json (last-fetched
// timestamp + sha256 of the payload for tamper detection).
//
// Two files instead of one wrapper-JSON: the bytes the upstream
// signed are preserved verbatim, so a re-verify against a different
// release pubkey at boot is byte-exact.
type FileCache struct {
	Dir string
}

const (
	cachePayloadName = "crl.json"
	cacheMetaName    = "crl.meta.json"
)

type cacheMeta struct {
	LastFetched     time.Time `json:"last_fetched"`
	PayloadSize     int       `json:"payload_size"`
	LastCRLIssuedAt time.Time `json:"last_crl_issued_at,omitempty"`
}

// Load implements Cache. Returns (nil, zero, false, nil) on absent
// cache, (nil, zero, false, ErrCacheCorrupt) on parse error.
func (f *FileCache) Load() ([]byte, time.Time, bool, error) {
	if f.Dir == "" {
		return nil, time.Time{}, false, fmt.Errorf("crl: cache Dir not set")
	}
	rawPath := filepath.Join(f.Dir, cachePayloadName)
	metaPath := filepath.Join(f.Dir, cacheMetaName)

	raw, err := os.ReadFile(rawPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, time.Time{}, false, nil
		}
		return nil, time.Time{}, false, fmt.Errorf("%w: read payload: %w", ErrCacheCorrupt, err)
	}
	metaRaw, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Payload without meta = corrupt (likely interrupted
			// previous Save). Surface as corrupt so Refresher logs.
			return nil, time.Time{}, false, fmt.Errorf("%w: payload present but meta missing", ErrCacheCorrupt)
		}
		return nil, time.Time{}, false, fmt.Errorf("%w: read meta: %w", ErrCacheCorrupt, err)
	}
	var meta cacheMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return nil, time.Time{}, false, fmt.Errorf("%w: parse meta: %w", ErrCacheCorrupt, err)
	}
	if meta.PayloadSize != len(raw) {
		return nil, time.Time{}, false, fmt.Errorf("%w: payload size %d != meta %d",
			ErrCacheCorrupt, len(raw), meta.PayloadSize)
	}
	if meta.LastFetched.IsZero() {
		return nil, time.Time{}, false, fmt.Errorf("%w: meta lacks last_fetched", ErrCacheCorrupt)
	}
	return raw, meta.LastFetched.UTC(), true, nil
}

// Save implements Cache. Writes both files atomically via
// rename-from-tmp so a crash mid-write leaves the previous cache
// intact (or no cache at all on first boot).
func (f *FileCache) Save(raw []byte, fetchedAt time.Time, issuedAt time.Time) error {
	if f.Dir == "" {
		return fmt.Errorf("crl: cache Dir not set")
	}
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return fmt.Errorf("crl: mkdir cache: %w", err)
	}

	meta := cacheMeta{
		LastFetched:     fetchedAt.UTC(),
		PayloadSize:     len(raw),
		LastCRLIssuedAt: issuedAt.UTC(),
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("crl: marshal meta: %w", err)
	}

	if err := writeAtomic(filepath.Join(f.Dir, cachePayloadName), raw, 0o600); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(f.Dir, cacheMetaName), metaBytes, 0o600); err != nil {
		return err
	}
	return nil
}

// LoadIssuedAt implements Cache. Reads only the meta file; returns
// (zero, nil) when meta is absent or empty so the Refresher treats
// "first boot" as "no high-water set, accept anything". Corruption
// returns a typed error — the Refresher logs and proceeds without
// a high-water (which means the next fetch effectively re-baselines).
func (f *FileCache) LoadIssuedAt() (time.Time, error) {
	if f.Dir == "" {
		return time.Time{}, fmt.Errorf("crl: cache Dir not set")
	}
	metaPath := filepath.Join(f.Dir, cacheMetaName)
	metaRaw, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("%w: read meta: %w", ErrCacheCorrupt, err)
	}
	var meta cacheMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return time.Time{}, fmt.Errorf("%w: parse meta: %w", ErrCacheCorrupt, err)
	}
	return meta.LastCRLIssuedAt.UTC(), nil
}

// writeAtomic writes data to path via a tmp file in the same
// directory, then renames into place. Cross-FS rename is not a
// concern because tmp lives in the same Dir as the target.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".crl-*.tmp")
	if err != nil {
		return fmt.Errorf("crl: create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("crl: write tmp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("crl: chmod tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("crl: close tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("crl: rename tmp: %w", err)
	}
	return nil
}
