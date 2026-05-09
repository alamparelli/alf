package agents

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// SEC-080-002: chmodChownDirNoFollow must refuse to chmod a path that is
// a symlink, even when the swap to a symlink happens between the
// caller's MkdirAll and the chmod call. The fd-bound implementation
// guarantees this via O_NOFOLLOW|O_DIRECTORY at open time; this test
// pins the behaviour so a future refactor cannot regress to the
// Lstat→Chmod pattern.

func TestChmodChownDirNoFollow_RefusesSymlinkLeaf(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	err := chmodChownDirNoFollow(link, 0o775, os.Getuid(), os.Getgid())
	if !errors.Is(err, ErrSymlinkAtPath) {
		t.Fatalf("chmodChownDirNoFollow on symlink: got %v, want ErrSymlinkAtPath", err)
	}

	// The target's mode must not have changed — the kernel-level
	// O_NOFOLLOW must have stopped us before the chmod ran.
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("symlink target was chmod'd through the link: mode=%04o (expected 0700)", got)
	}
}

func TestChmodChownDirNoFollow_RefusesNonDirectory(t *testing.T) {
	tmp := t.TempDir()
	regular := filepath.Join(tmp, "file")
	if err := os.WriteFile(regular, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := chmodChownDirNoFollow(regular, 0o775, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("chmodChownDirNoFollow on regular file should error (O_DIRECTORY)")
	}
}

func TestChmodChownDirNoFollow_AppliesModeOnRealDir(t *testing.T) {
	tmp := t.TempDir()
	d := filepath.Join(tmp, "agents")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := chmodChownDirNoFollow(d, 0o775, os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("chmodChownDirNoFollow on real dir: %v", err)
	}
	info, err := os.Stat(d)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o775 {
		t.Fatalf("dir mode after chmodChownDirNoFollow: got %04o, want 0775", got)
	}
}

// TestChmodChownDirNoFollow_SymlinkSwapRace is the regression test for
// the SEC-080-002 attack: a hostile actor with write access to the
// parent dir continuously swaps the leaf between a real dir and a
// symlink. The Lstat→Chmod pattern that this code replaces would
// occasionally pass the Lstat (it saw the real dir) then Chmod through
// the symlink (the swap happened in between). chmodChownDirNoFollow's
// fd-bound chmod must never produce that outcome.
//
// Test design:
//   - One goroutine alternates `target` between a real dir (mode 0o700)
//     and a symlink to a sentinel target (mode 0o700) under tmp.
//   - The main goroutine repeatedly calls chmodChownDirNoFollow on
//     `target`, requesting mode 0o775.
//   - Outcome invariant: each call either succeeds with target being a
//     real dir at 0o775 OR fails with ErrSymlinkAtPath. The sentinel
//     target dir's mode must remain 0o700 throughout.
func TestChmodChownDirNoFollow_SymlinkSwapRace(t *testing.T) {
	if testing.Short() {
		t.Skip("race test")
	}
	tmp := t.TempDir()
	sentinel := filepath.Join(tmp, "sentinel")
	if err := os.Mkdir(sentinel, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tmp, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var swapper sync.WaitGroup
	swapper.Add(1)
	var swaps atomic.Int64
	go func() {
		defer swapper.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.RemoveAll(target)
			if swaps.Load()%2 == 0 {
				_ = os.Mkdir(target, 0o700)
			} else {
				_ = os.Symlink(sentinel, target)
			}
			swaps.Add(1)
		}
	}()

	// During the race the leaf can momentarily be: real dir / symlink
	// to dir / missing entirely (between the swapper's RemoveAll and
	// its Mkdir-or-Symlink). All three outcomes are legitimate race
	// results. The load-bearing invariant is the post-loop sentinel
	// mode check below: the sentinel must NEVER be chmod'd through
	// the symlink.
	const iters = 500
	var sawDir, sawSymlink, sawTransient int
	for i := 0; i < iters; i++ {
		err := chmodChownDirNoFollow(target, 0o775, os.Getuid(), os.Getgid())
		switch {
		case err == nil:
			sawDir++
		case errors.Is(err, ErrSymlinkAtPath):
			sawSymlink++
		default:
			// Any non-symlink failure during the swap (ENOENT mid-
			// rename, ENOTDIR for a brief regular-file state we
			// don't reach in this test, etc.) is a legitimate race
			// outcome — what matters is the sentinel-mode invariant.
			sawTransient++
		}
	}
	close(stop)
	swapper.Wait()

	// Sentinel dir must NEVER have been chmod'd to 0o775. If
	// chmodChownDirNoFollow had followed the symlink, it would have
	// re-moded the sentinel.
	info, err := os.Stat(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("symlink target (sentinel) was chmod'd through a symlink swap: mode=%04o", got)
	}
	t.Logf("race covered: dir-success=%d symlink-refused=%d transient=%d swaps=%d",
		sawDir, sawSymlink, sawTransient, swaps.Load())
}
