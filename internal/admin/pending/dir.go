package pending

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DirStore is the on-disk Store implementation. One file per Item at
// <dir>/<id>.json (mode 0o600, parent 0o700). The id is the file's
// basename minus the extension; the file content is the Item's JSON.
//
// File layout chosen over SQLite:
//   - Atomic at the filesystem level (tmp+rename per Append; unlink
//     per Approve/Deny). No transaction window.
//   - Auditable: an operator with read access to <dir> can `cat
//     <id>.json` to inspect what's queued, without an sqlite client.
//   - No cgo dependency for the queue surface — lets a stripped-down
//     CLI build (`alf pending`) avoid linking the SQLite library
//     just to enumerate items.
//   - Same UX shape as <dataDir>/trust/<keyid>.pub — operators learn
//     one mental model for both admin-side caches.
//
// Concurrency. Append + Approve + Deny take a process-local mutex.
// Multiple alf binaries on the same dir would race file creation, but
// that's the same trade-off as DirTrustStore — admin commands are
// not meant to run concurrently from two TTYs.
type DirStore struct {
	dir string
	mu  sync.Mutex
	now func() time.Time

	// nextID tracks the next id to allocate. Initialised at NewDirStore
	// time by scanning <dir> for the highest existing id; subsequent
	// Append calls bump it monotonically. ids are zero-padded decimals
	// so lexicographic sort matches numeric order.
	nextID uint64
}

// NewDirStore opens (and creates if needed) the directory at dir, and
// returns a Store backed by the files within. Existing items are kept
// — restart durability is the point of switching off MemoryStore.
//
// Refuses if the directory exists with permissive perms (0o077 set);
// pending items are not secrets per se but a co-tenant reading the
// queue learns what the operator has been asked to ratify, which is
// a side-channel that should not exist by default.
func NewDirStore(dir string, now func() time.Time) (*DirStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("pending: NewDirStore requires a non-empty dir")
	}
	if now == nil {
		now = time.Now
	}
	s := &DirStore{dir: dir, now: now}
	if err := s.ensureDir(); err != nil {
		return nil, err
	}
	if err := s.scanNextID(); err != nil {
		return nil, err
	}
	return s, nil
}

// Dir returns the directory the store is bound to. Used by admin CLI
// help text + tests.
func (s *DirStore) Dir() string { return s.dir }

func (s *DirStore) ensureDir() error {
	info, err := os.Stat(s.dir)
	switch {
	case err == nil:
		if !info.IsDir() {
			return fmt.Errorf("pending: %s exists and is not a directory", s.dir)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("pending: %s has permissive perms %v; refusing to use", s.dir, info.Mode().Perm())
		}
		return nil
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(s.dir, 0o700); err != nil {
			return fmt.Errorf("pending: mkdir %s: %w", s.dir, err)
		}
		return nil
	default:
		return fmt.Errorf("pending: stat %s: %w", s.dir, err)
	}
}

// scanNextID walks the directory once at construction time to find
// the maximum existing id, then sets nextID = max+1. New ids never
// collide with already-removed items (which is fine — ids are
// monotonic at allocation time, not reused on Approve/Deny).
func (s *DirStore) scanNextID() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("pending: read dir: %w", err)
	}
	var max uint64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if id == e.Name() {
			continue // not a queue file
		}
		var n uint64
		for _, c := range id {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + uint64(c-'0')
		}
		if n > max {
			max = n
		}
	}
	s.nextID = max
	return nil
}

func (s *DirStore) Append(_ context.Context, item Item) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	id := formatID(s.nextID)
	item.ID = id
	if item.CreatedAt.IsZero() {
		item.CreatedAt = s.now().UTC()
	}

	if err := s.writeItem(id, item); err != nil {
		// Roll back the id bump so a transient FS failure does not
		// leave a hole in the sequence on the next attempt.
		s.nextID--
		return "", err
	}
	return id, nil
}

func (s *DirStore) List(_ context.Context) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("pending: list: %w", err)
	}
	items := make([]Item, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			// Fail closed: a corrupted item file should NOT be
			// silently skipped — the operator needs to see it (or
			// the test should fail) so they can manually clean up.
			return nil, fmt.Errorf("pending: read %s: %w", e.Name(), err)
		}
		var it Item
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, fmt.Errorf("pending: decode %s: %w", e.Name(), err)
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (s *DirStore) Approve(_ context.Context, id string) (Item, error) {
	return s.remove(id)
}

func (s *DirStore) Deny(_ context.Context, id string) (Item, error) {
	return s.remove(id)
}

func (s *DirStore) remove(id string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dir, id+".json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("pending: read %s: %w", id, err)
	}
	var it Item
	if err := json.Unmarshal(raw, &it); err != nil {
		return Item{}, fmt.Errorf("pending: decode %s: %w", id, err)
	}
	if err := os.Remove(path); err != nil {
		return Item{}, fmt.Errorf("pending: remove %s: %w", id, err)
	}
	return it, nil
}

// writeItem serialises item and writes <dir>/<id>.json atomically
// (tmp+rename) with mode 0o600. Caller holds s.mu.
func (s *DirStore) writeItem(id string, item Item) error {
	out, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return fmt.Errorf("pending: encode: %w", err)
	}
	tmp, err := os.CreateTemp(s.dir, ".item-*.tmp")
	if err != nil {
		return fmt.Errorf("pending: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("pending: chmod tmp: %w", err)
	}
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("pending: write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("pending: sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("pending: close tmp: %w", err)
	}
	final := filepath.Join(s.dir, id+".json")
	if err := os.Rename(tmpPath, final); err != nil {
		return fmt.Errorf("pending: rename tmp -> %s: %w", final, err)
	}
	return nil
}

// DefaultDir resolves the on-disk pending dir from the alf install
// layout: <dataDir>/admin/pending/. Sibling to keys/ and trust/ under
// data/.
func DefaultDir(dataDir string) string {
	return filepath.Join(dataDir, "admin", "pending")
}
