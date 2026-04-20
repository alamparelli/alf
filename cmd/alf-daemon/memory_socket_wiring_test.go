package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/socketsrv"
)

// shortSockPath is the same helper used in internal/memory/socketsrv —
// macOS's 104-byte sockaddr_un limit truncates t.TempDir paths on CI,
// so unix-socket tests always bind under os.TempDir with a short name.
func shortSockPath(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(os.TempDir(), "alfdw-"+hex.EncodeToString(buf)+".sock")
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

// TestDaemonMemorySocket_EndToEnd spins up the same pieces the daemon
// wires in main.go — memory.NewSQLiteStore + socketsrv.Server.ServeUnix —
// then exercises the socket round-trip with the legacy memstore protocol
// that cmd/memory-tools speaks. This is the regression guard for the
// #337c4b3 wiring: if either side of the bridge drifts (protocol shape,
// scope mapping, ID conversion), this fails before a user sees it.
func TestDaemonMemorySocket_EndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	store, err := memory.NewSQLiteStore(dataDir)
	if err != nil {
		t.Fatalf("memory.NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	srv := socketsrv.New(store)

	sockPath := shortSockPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				dec := json.NewDecoder(c)
				enc := json.NewEncoder(c)
				var req socketsrv.Request
				if err := dec.Decode(&req); err != nil {
					_ = enc.Encode(socketsrv.Response{Error: "decode: " + err.Error()})
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = enc.Encode(srv.Handle(ctx, req))
			}(conn)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})

	// 1. Store via socket (what an LLM /remember call sends).
	stored := exchange(t, sockPath, socketsrv.Request{
		Action: "store",
		Text:   "wiring test: alf daemon socket works end-to-end",
		Type:   "fact",
	})
	if stored.Error != "" {
		t.Fatalf("store: %v", stored.Error)
	}
	if stored.ID == 0 {
		t.Fatalf("store returned zero ID")
	}

	// 2. Search via socket (what /recall sends).
	found := exchange(t, sockPath, socketsrv.Request{
		Action: "search",
		Query:  "wiring test",
		Limit:  5,
	})
	if found.Error != "" {
		t.Fatalf("search: %v", found.Error)
	}
	if found.Count == 0 {
		t.Fatalf("search found no results after store; results=%+v", found.Results)
	}
	var matched bool
	for _, m := range found.Results {
		if m.ID == stored.ID {
			matched = true
			if m.Type != "fact" {
				t.Errorf("expected Type=fact, got %q", m.Type)
			}
		}
	}
	if !matched {
		t.Errorf("stored memory #%d not found in search results", stored.ID)
	}

	// 3. Delete via socket (what /forget sends).
	del := exchange(t, sockPath, socketsrv.Request{Action: "delete", ID: stored.ID})
	if del.Error != "" {
		t.Errorf("delete: %v", del.Error)
	}

	// 4. Confirm by direct store read — the delete propagated past the
	// socket layer into memory.Store, including the vec/FTS cleanup.
	got, _ := store.GetDocument(context.Background(), "fact", itoa(stored.ID))
	if got != nil {
		t.Errorf("document survived delete: %+v", got)
	}
}

// exchange dials the unix socket, sends one request, reads one response.
// Inline helper so this test file stays self-contained.
func exchange(t *testing.T, sockPath string, req socketsrv.Request) socketsrv.Response {
	t.Helper()
	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var resp socketsrv.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// itoa mirrors strconv.FormatInt for int64 — avoids pulling strconv into
// the test file just for one use.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
