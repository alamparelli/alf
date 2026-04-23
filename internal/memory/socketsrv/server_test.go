package socketsrv_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/socketsrv"
)

// shortSockPath returns a unix-socket path under os.TempDir() short enough
// to fit in the 104-byte sockaddr_un limit that macOS enforces. Using
// t.TempDir() directly would overflow on CI (>108 bytes) so the round-trip
// tests all fail with "bind: invalid argument".
func shortSockPath(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(os.TempDir(), "msrv-"+hex.EncodeToString(buf)+".sock")
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

// newTestServer returns a socket server backed by a fresh on-disk memory
// store under t.TempDir(). The caller gets both the server (for direct
// Handle() calls) and the store (for arrange-phase seeding).
func newTestServer(t *testing.T) (*socketsrv.Server, memory.Store) {
	t.Helper()
	store, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("memory.NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return socketsrv.New(store), store
}

// --- Direct Handle() coverage: unit tests on the dispatch logic, no
// socket plumbing. Faster and isolates protocol behaviour from
// net/unix noise.

func TestHandle_Search_FansOutAcrossScopes(t *testing.T) {
	srv, store := newTestServer(t)
	ctx := context.Background()
	_ = store.Index(ctx, "fact", memory.Document{ID: "1", Text: "the deployment uses docker compose"})
	_ = store.Index(ctx, "preference", memory.Document{ID: "2", Text: "user likes docker-compose"})
	_ = store.Index(ctx, "decision", memory.Document{ID: "3", Text: "chose helm over kustomize"})

	resp := srv.Handle(ctx, socketsrv.Request{Action: "search", Query: "docker", Limit: 10})
	if resp.Error != "" {
		t.Fatalf("search: %v", resp.Error)
	}
	if resp.Count < 2 {
		t.Errorf("expected hits across multiple scopes; count=%d", resp.Count)
	}

	// Every hit must carry its scope in the Type field for wire compat
	// with memstore.Memory.Type.
	seenScopes := map[string]bool{}
	for _, m := range resp.Results {
		seenScopes[m.Type] = true
	}
	if !seenScopes["fact"] || !seenScopes["preference"] {
		t.Errorf("expected fact+preference in results, got %v", seenScopes)
	}
}

func TestHandle_Search_EmptyStoreReturnsNoError(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := srv.Handle(context.Background(), socketsrv.Request{Action: "search", Query: "anything"})
	if resp.Error != "" {
		t.Errorf("empty-store search errored: %v", resp.Error)
	}
	if resp.Count != 0 {
		t.Errorf("expected 0 hits on empty store, got %d", resp.Count)
	}
}

func TestHandle_Store_AssignsIncrementingIDs(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx := context.Background()
	r1 := srv.Handle(ctx, socketsrv.Request{Action: "store", Text: "first", Type: "fact"})
	r2 := srv.Handle(ctx, socketsrv.Request{Action: "store", Text: "second", Type: "fact"})
	if r1.Error != "" || r2.Error != "" {
		t.Fatalf("store errors: r1=%q r2=%q", r1.Error, r2.Error)
	}
	if r1.ID == 0 || r2.ID == 0 {
		t.Errorf("expected non-zero IDs; got %d and %d", r1.ID, r2.ID)
	}
	if r1.ID == r2.ID {
		t.Errorf("IDs must differ across stores; both were %d", r1.ID)
	}
}

func TestHandle_Store_DefaultsTypeToFact(t *testing.T) {
	srv, store := newTestServer(t)
	resp := srv.Handle(context.Background(), socketsrv.Request{Action: "store", Text: "no type given"})
	if resp.Error != "" {
		t.Fatalf("store: %v", resp.Error)
	}
	// Must land in scope "fact".
	hits, _ := store.Search(context.Background(), "fact", "no type", 5)
	if len(hits) == 0 {
		t.Errorf("default-type store did not land in scope='fact'")
	}
}

func TestHandle_Store_RejectsInvalidType(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := srv.Handle(context.Background(), socketsrv.Request{Action: "store", Text: "x", Type: "not-a-real-type"})
	if resp.Error == "" {
		t.Error("expected error for invalid type")
	}
}

func TestHandle_Store_RejectsEmptyText(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := srv.Handle(context.Background(), socketsrv.Request{Action: "store", Text: "", Type: "fact"})
	if resp.Error == "" {
		t.Error("expected error for empty text")
	}
}

func TestHandle_Store_RejectsOversizedText(t *testing.T) {
	srv, _ := newTestServer(t)
	big := strings.Repeat("x", 11*1024) // >10KB
	resp := srv.Handle(context.Background(), socketsrv.Request{Action: "store", Text: big, Type: "fact"})
	if resp.Error == "" || !strings.Contains(resp.Error, "too large") {
		t.Errorf("expected too-large error, got %q", resp.Error)
	}
}

func TestHandle_Delete_RemovesStoredMemory(t *testing.T) {
	srv, store := newTestServer(t)
	ctx := context.Background()

	stored := srv.Handle(ctx, socketsrv.Request{Action: "store", Text: "ephemeral", Type: "fact"})
	if stored.Error != "" || stored.ID == 0 {
		t.Fatalf("store: %+v", stored)
	}

	del := srv.Handle(ctx, socketsrv.Request{Action: "delete", ID: stored.ID})
	if del.Error != "" {
		t.Fatalf("delete: %v", del.Error)
	}
	if del.ID != stored.ID {
		t.Errorf("delete returned id=%d, want %d", del.ID, stored.ID)
	}

	// Round-trip check: the document is gone from the store.
	got, _ := store.GetDocument(ctx, "fact", fmt.Sprintf("%d", stored.ID))
	if got != nil {
		t.Errorf("document still present after delete: %+v", got)
	}
}

func TestHandle_Delete_UnknownIDErrors(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := srv.Handle(context.Background(), socketsrv.Request{Action: "delete", ID: 999_999})
	if resp.Error == "" {
		t.Error("expected error for unknown id")
	}
}

func TestHandle_Delete_RejectsZeroID(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := srv.Handle(context.Background(), socketsrv.Request{Action: "delete", ID: 0})
	if resp.Error == "" {
		t.Error("expected error for missing id")
	}
}

func TestHandle_Recent_ReturnsTotalCount(t *testing.T) {
	srv, store := newTestServer(t)
	ctx := context.Background()
	_ = store.Index(ctx, "fact", memory.Document{ID: "1", Text: "a"})
	_ = store.Index(ctx, "preference", memory.Document{ID: "2", Text: "b"})
	_ = store.Index(ctx, "decision", memory.Document{ID: "3", Text: "c"})

	resp := srv.Handle(ctx, socketsrv.Request{Action: "recent"})
	if resp.Error != "" {
		t.Fatalf("recent: %v", resp.Error)
	}
	if resp.Count != 3 {
		t.Errorf("Count = %d, want 3", resp.Count)
	}
}

func TestHandle_UnknownActionErrors(t *testing.T) {
	srv, _ := newTestServer(t)
	resp := srv.Handle(context.Background(), socketsrv.Request{Action: "bogus"})
	if resp.Error == "" || !strings.Contains(resp.Error, "unknown action") {
		t.Errorf("expected unknown-action error, got %q", resp.Error)
	}
}

func TestHitToMemory_NonNumericDocIDDegradesToZero(t *testing.T) {
	// Non-numeric doc_ids ("ingest-abc123") come from the UI ingest adapter
	// and are not addressable via the int64 socket protocol. The server
	// must surface them with ID=0 rather than hide them; this pins the
	// contract so memory-tools can print a clear #0 marker.
	srv, store := newTestServer(t)
	ctx := context.Background()
	_ = store.Index(ctx, "fact", memory.Document{ID: "ingest-abc123", Text: "from ingest"})

	resp := srv.Handle(ctx, socketsrv.Request{Action: "search", Query: "ingest", Limit: 5})
	if resp.Count == 0 {
		t.Fatal("expected at least one hit")
	}
	var found bool
	for _, m := range resp.Results {
		if m.Text == "from ingest" {
			found = true
			if m.ID != 0 {
				t.Errorf("non-numeric docID produced ID=%d, want 0", m.ID)
			}
		}
	}
	if !found {
		t.Fatalf("ingest-originated doc missing from results")
	}
}

// --- Full socket round-trip: dial the listener, encode/decode JSON,
// catch regressions in the protocol framing layer.

// serveSocket starts a Server on a tmp unix socket and returns the path
// + a teardown closer. The server runs in a goroutine; the listener is
// closed on cleanup so the accept loop exits cleanly.
func serveSocket(t *testing.T, srv *socketsrv.Server) (sockPath string, teardown func()) {
	t.Helper()
	sockPath = shortSockPath(t)
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
				dec := json.NewDecoder(io.LimitReader(c, 64*1024))
				enc := json.NewEncoder(c)
				var req socketsrv.Request
				if err := dec.Decode(&req); err != nil {
					_ = enc.Encode(socketsrv.Response{Error: "decode: " + err.Error()})
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = enc.Encode(srv.Handle(ctx, req))
			}(conn)
		}
	}()
	return sockPath, func() {
		_ = ln.Close()
		<-done
	}
}

func dialAndExchange(t *testing.T, sockPath string, req socketsrv.Request) socketsrv.Response {
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

func TestServeUnix_StoreSearchDeleteRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	sockPath, teardown := serveSocket(t, srv)
	defer teardown()

	// Store via the socket.
	stored := dialAndExchange(t, sockPath, socketsrv.Request{Action: "store", Text: "socket test fact", Type: "fact"})
	if stored.Error != "" || stored.ID == 0 {
		t.Fatalf("store over socket: %+v", stored)
	}

	// Search via the socket.
	found := dialAndExchange(t, sockPath, socketsrv.Request{Action: "search", Query: "socket test", Limit: 5})
	if found.Error != "" {
		t.Fatalf("search over socket: %v", found.Error)
	}
	var match bool
	for _, m := range found.Results {
		if m.ID == stored.ID && strings.Contains(m.Text, "socket test") {
			match = true
		}
	}
	if !match {
		t.Errorf("stored memory not found in search results: %+v", found)
	}

	// Delete via the socket.
	del := dialAndExchange(t, sockPath, socketsrv.Request{Action: "delete", ID: stored.ID})
	if del.Error != "" {
		t.Errorf("delete over socket: %v", del.Error)
	}
}

func TestServeUnix_BadJSONProducesErrorResponse(t *testing.T) {
	srv, _ := newTestServer(t)
	sockPath, teardown := serveSocket(t, srv)
	defer teardown()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("{this is not valid json")); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Close write side so the decoder on the server stops waiting.
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	var resp socketsrv.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == "" || !strings.Contains(resp.Error, "decode") {
		t.Errorf("expected decode error, got %q", resp.Error)
	}
}

func TestServeUnix_ConcurrentClients(t *testing.T) {
	srv, _ := newTestServer(t)
	sockPath, teardown := serveSocket(t, srv)
	defer teardown()

	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp := dialAndExchange(t, sockPath, socketsrv.Request{
				Action: "store",
				Text:   fmt.Sprintf("concurrent fact #%d about golang", i),
				Type:   "fact",
			})
			if resp.Error != "" {
				errs <- fmt.Errorf("client %d: %s", i, resp.Error)
				return
			}
			if resp.ID == 0 {
				errs <- fmt.Errorf("client %d: zero id", i)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Post-check: all N rows are searchable.
	resp := dialAndExchange(t, sockPath, socketsrv.Request{Action: "search", Query: "golang", Limit: 100})
	if resp.Count < n {
		t.Errorf("expected >= %d hits after concurrent stores, got %d", n, resp.Count)
	}
}
