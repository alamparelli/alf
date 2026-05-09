package socknet

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestListenUnix0660_InodeMode pins SEC-407-002's load-bearing
// invariant: the socket inode is mode 0660 immediately after
// ListenUnix0660 returns, NOT after a follow-up Chmod. Even if
// the post-listen Chown/Chmod errors out (typical in unprivileged
// test environments), the umask-narrowed Listen still produces
// the right mode.
func TestListenUnix0660_InodeMode(t *testing.T) {
	// Unix socket sun_path is capped at 104 (macOS) / 108 (Linux)
	// chars. t.TempDir() embeds the test name and runs over on
	// long names — use a short path prefix.
	dir, _ := os.MkdirTemp("", "sn-")
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "test.sock")

	ln, err := ListenUnix0660(sock, os.Getgid())
	if err != nil {
		t.Fatalf("ListenUnix0660: %v", err)
	}
	defer ln.Close()
	defer os.Remove(sock)

	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	got := info.Mode().Perm()
	want := os.FileMode(0o660)
	if got != want {
		t.Errorf("socket mode after Listen = %#o, want %#o (TOCTOU window leaked)", got, want)
	}
}

// TestListenUnix0660_RestoresUmask pins that the umask wrapper
// is symmetric: after the function returns, the process umask is
// back where it was. A leak would loosen perms on every
// subsequent file the daemon creates.
func TestListenUnix0660_RestoresUmask(t *testing.T) {
	// Unix socket sun_path is capped at 104 (macOS) / 108 (Linux)
	// chars. t.TempDir() embeds the test name and runs over on
	// long names — use a short path prefix.
	dir, _ := os.MkdirTemp("", "sn-")
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "test.sock")

	// Create a probe file BEFORE the listen to capture the
	// pre-call umask via the resulting mode. With default daemon
	// umask 0o002, a 0o666 WriteFile produces 0o664.
	probePre := filepath.Join(dir, "pre")
	if err := os.WriteFile(probePre, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	preMode, _ := os.Stat(probePre)

	ln, err := ListenUnix0660(sock, os.Getgid())
	if err != nil {
		t.Fatalf("ListenUnix0660: %v", err)
	}
	defer ln.Close()

	// Probe file AFTER. Same write-mode 0o666; if the umask is
	// still 0o117, the resulting mode would be 0o660 — different
	// from the pre file. If symmetric, both files match.
	probePost := filepath.Join(dir, "post")
	if err := os.WriteFile(probePost, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	postMode, _ := os.Stat(probePost)

	if preMode.Mode().Perm() != postMode.Mode().Perm() {
		t.Errorf("umask leaked: pre=%#o post=%#o (ListenUnix0660 should restore umask)",
			preMode.Mode().Perm(), postMode.Mode().Perm())
	}
}

// TestListenUnix0660_ConcurrentListensSerialise pins that two
// concurrent calls do not race on the process-global syscall.Umask
// state. Without the listenMu guard, one caller could observe
// the other's umask and produce a wrong-mode socket.
//
// Sockets are stat'd while the listeners are still open — the
// per-goroutine listener handle keeps the inode pinned past the
// Stat call.
func TestListenUnix0660_ConcurrentListensSerialise(t *testing.T) {
	// Unix socket sun_path is capped at 104 (macOS) / 108 (Linux)
	// chars. t.TempDir() embeds the test name and runs over on
	// long names — use a short path prefix.
	dir, _ := os.MkdirTemp("", "sn-")
	t.Cleanup(func() { os.RemoveAll(dir) })

	const N = 16
	type result struct {
		mode os.FileMode
		err  error
	}
	results := make([]result, N)
	listeners := make([]struct{ closeFn func() }, N)

	var wg sync.WaitGroup
	var startBarrier sync.WaitGroup
	startBarrier.Add(1)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			startBarrier.Wait() // every goroutine releases together
			sock := filepath.Join(dir, "sock-"+string(rune('a'+i)))
			ln, err := ListenUnix0660(sock, os.Getgid())
			if err != nil {
				results[i] = result{err: err}
				return
			}
			info, statErr := os.Stat(sock)
			if statErr != nil {
				ln.Close()
				results[i] = result{err: statErr}
				return
			}
			results[i] = result{mode: info.Mode().Perm()}
			listeners[i].closeFn = func() { ln.Close() }
		}(i)
	}
	startBarrier.Done()
	wg.Wait()
	defer func() {
		for _, l := range listeners {
			if l.closeFn != nil {
				l.closeFn()
			}
		}
	}()

	for i, r := range results {
		if r.err != nil {
			t.Errorf("listen #%d failed: %v", i, r.err)
			continue
		}
		if r.mode != 0o660 {
			t.Errorf("concurrent listen #%d produced mode %#o (race?)", i, r.mode)
		}
	}
}
