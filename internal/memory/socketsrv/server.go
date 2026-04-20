// Package socketsrv serves the legacy memstore Unix socket protocol on
// top of memory.Store, so cmd/memory-tools can swap backends without
// changing its wire format. This is the daemon-side replacement for
// internal/memstore.Store.ServeUnix — lands in #337c4b2 as the
// prerequisite for retiring the memstore package.
package socketsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/memory"
)

const (
	maxRequestSize = 64 * 1024 // 64KB max JSON request
	maxTextSize    = 10 * 1024 // 10KB max memory text
	handleTimeout  = 30 * time.Second
)

// KnownScopes are the memory.Scope values produced by the legacy
// memstore extractor and consolidator. The socket protocol has no scope
// field; handlers fan out reads across all of these and accept any of
// them for writes (via the "type" field).
//
// Mirror of the slice in cmd/alf-daemon/adapters.go (memoryScopes) — kept
// here as a package-level export so callers can adjust both together.
var KnownScopes = []memory.Scope{"fact", "preference", "decision", "contact", "summary"}

var validMemTypes = func() map[memory.Scope]bool {
	m := make(map[memory.Scope]bool, len(KnownScopes))
	for _, s := range KnownScopes {
		m[s] = true
	}
	return m
}()

// Request is the JSON protocol for tool → daemon communication. Matches
// memstore.socketRequest byte-for-byte so a rollover is transparent.
type Request struct {
	Action string `json:"action"` // "search", "store", "delete", "recent"
	Query  string `json:"query,omitempty"`
	Text   string `json:"text,omitempty"`
	Type   string `json:"type,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Days   int    `json:"days,omitempty"` // accepted for wire compat; not yet honoured
	ID     int64  `json:"id,omitempty"`
}

// Memory mirrors memstore.Memory as exposed on the wire. Legacy
// memory-tools unmarshals this exact shape, so the field names and
// types MUST stay stable even as the backing store evolves.
type Memory struct {
	ID        int64   `json:"ID"`
	Text      string  `json:"Text"`
	Type      string  `json:"Type"`
	Source    string  `json:"Source"`
	CreatedAt string  `json:"CreatedAt"`
	Distance  float64 `json:"Distance"`
}

// Response is sent back to the tool.
type Response struct {
	Results []Memory `json:"results,omitempty"`
	ID      int64    `json:"id,omitempty"`
	Count   int      `json:"count,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// Server wraps a memory.Store with a dispatch loop over a Unix socket.
type Server struct {
	store memory.Store

	// idSeq supplies fresh int64 IDs for store() actions. The wire
	// protocol requires an int64; memory.Store doc_ids are strings. We
	// pick a monotonically-increasing suffix so identical-text ingests
	// still return distinct IDs, while the Store itself upserts on the
	// stable hash-derived docID (see Handle).
	mu    sync.Mutex
	idSeq int64
}

// New returns a Server bound to store.
func New(store memory.Store) *Server {
	return &Server{store: store}
}

// Handle dispatches a single Request and returns the Response. Safe for
// concurrent use. Kept separate from ServeUnix so tests can exercise the
// dispatch logic without spinning up a real socket.
func (s *Server) Handle(ctx context.Context, req Request) Response {
	switch req.Action {
	case "search":
		return s.handleSearch(ctx, req)
	case "store":
		return s.handleStore(ctx, req)
	case "delete":
		return s.handleDelete(ctx, req)
	case "recent":
		return s.handleRecent(ctx, req)
	default:
		return Response{Error: fmt.Sprintf("unknown action: %s", req.Action)}
	}
}

func (s *Server) handleSearch(ctx context.Context, req Request) Response {
	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}
	// Fan out across scopes — the wire protocol has no scope field. See
	// cmd/alf-daemon/memoryCCRecaller for the same pattern.
	var all []memory.Hit
	for _, scope := range KnownScopes {
		hits, err := s.store.Search(ctx, scope, req.Query, limit)
		if err != nil {
			log.Printf("[memory-socket] search scope=%q: %v", scope, err)
			continue
		}
		for _, h := range hits {
			if h.Document.Metadata == nil {
				h.Document.Metadata = map[string]string{}
			}
			h.Document.Metadata["scope"] = string(scope)
			all = append(all, h)
		}
	}
	// Sort by score descending (insertion sort — N stays small).
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].Score > all[j-1].Score; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if len(all) > limit {
		all = all[:limit]
	}
	results := make([]Memory, len(all))
	for i, h := range all {
		results[i] = hitToMemory(h)
	}
	return Response{Results: results, Count: len(results)}
}

func (s *Server) handleStore(ctx context.Context, req Request) Response {
	if req.Text == "" {
		return Response{Error: "text required"}
	}
	if len(req.Text) > maxTextSize {
		return Response{Error: fmt.Sprintf("text too large (%d bytes, max %d)", len(req.Text), maxTextSize)}
	}
	memType := req.Type
	if memType == "" {
		memType = "fact"
	}
	if !validMemTypes[memory.Scope(memType)] {
		return Response{Error: fmt.Sprintf("invalid type %q (valid: %s)", memType, joinScopes(KnownScopes))}
	}

	s.mu.Lock()
	s.idSeq++
	id := s.idSeq
	s.mu.Unlock()

	docID := strconv.FormatInt(id, 10)
	if err := s.store.Index(ctx, memory.Scope(memType), memory.Document{
		ID:   docID,
		Text: req.Text,
		Metadata: map[string]string{
			"source":     "claude",
			"created_at": time.Now().Format(time.RFC3339),
		},
	}); err != nil {
		return Response{Error: err.Error()}
	}
	return Response{ID: id}
}

func (s *Server) handleDelete(ctx context.Context, req Request) Response {
	if req.ID <= 0 {
		return Response{Error: "id required for delete"}
	}
	docID := strconv.FormatInt(req.ID, 10)

	// The socket protocol has no scope field on delete. Try each known
	// scope — memory-tools derived this ID from a prior recall, so it
	// lives in exactly one scope.
	for _, scope := range KnownScopes {
		ok, err := s.store.DeleteDocument(ctx, scope, docID)
		if err != nil {
			return Response{Error: err.Error()}
		}
		if ok {
			return Response{ID: req.ID}
		}
	}
	return Response{Error: fmt.Sprintf("memory #%d not found in any scope", req.ID)}
}

func (s *Server) handleRecent(ctx context.Context, req Request) Response {
	// "recent" is only used by memory-tools --status for a total count.
	// memory.Store does not expose a direct count — walk each scope with
	// a big k and use the hit count as an approximation.
	// Days is accepted for wire compat but ignored for now; adding real
	// recency would need a ListRecent contract method (future work).
	_ = req.Days
	var total int
	for _, scope := range KnownScopes {
		hits, err := s.store.Search(ctx, scope, "", 10_000)
		if err != nil {
			log.Printf("[memory-socket] recent scope=%q: %v", scope, err)
			continue
		}
		total += len(hits)
	}
	return Response{Count: total}
}

// hitToMemory converts a memory.Hit to the wire shape memory-tools
// expects. Non-numeric docIDs (e.g. "ingest-abc123" from the UI ingest
// adapter) yield ID=0 — memory-tools shows "#0" for these, and forget
// cannot target them via int64. That's the documented degraded mode
// until memory-tools itself moves to string IDs.
func hitToMemory(h memory.Hit) Memory {
	var id int64
	if n, err := strconv.ParseInt(h.Document.ID, 10, 64); err == nil {
		id = n
	}
	return Memory{
		ID:        id,
		Text:      h.Document.Text,
		Type:      h.Document.Metadata["scope"],
		Source:    h.Document.Metadata["source"],
		CreatedAt: h.Document.Metadata["created_at"],
		Distance:  float64(1 - h.Score),
	}
}

func joinScopes(scopes []memory.Scope) string {
	b := make([]string, len(scopes))
	for i, s := range scopes {
		b[i] = string(s)
	}
	return strings.Join(b, ", ")
}

// ServeUnix listens on sockPath and dispatches incoming requests. Blocks
// until ln.Close() is called. Intended to run in a goroutine.
//
// File-mode handling mirrors memstore.ServeUnix: chown to gid 1000 so
// the subprocess (uid 1000) can connect, chmod 0660. Failures are
// non-fatal — Docker Desktop VirtioFS mounts reject chown from non-root.
func (s *Server) ServeUnix(sockPath string) error {
	// Remove stale socket file.
	_ = os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", sockPath, err)
	}
	return s.serveListener(ln, sockPath, true)
}

// serveListener is the test seam — exposed for server_test.go to pass a
// pre-configured listener and skip the chown/chmod dance.
func (s *Server) serveListener(ln net.Listener, sockPath string, tuneFileMode bool) error {
	if tuneFileMode && sockPath != "" {
		if err := os.Chown(sockPath, -1, 1000); err != nil {
			log.Printf("[memory-socket] chown %s: %v (non-root?)", sockPath, err)
		}
		if err := os.Chmod(sockPath, 0660); err != nil {
			log.Printf("[memory-socket] chmod %s: %v (continuing)", sockPath, err)
		}
	}
	if sockPath != "" {
		log.Printf("[memory-socket] listening on %s", sockPath)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			if strings.Contains(err.Error(), "use of closed") {
				return nil
			}
			log.Printf("[memory-socket] accept error: %v", err)
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(handleTimeout))

	dec := json.NewDecoder(io.LimitReader(conn, maxRequestSize))
	enc := json.NewEncoder(conn)

	var req Request
	if err := dec.Decode(&req); err != nil {
		_ = enc.Encode(Response{Error: fmt.Sprintf("decode: %v", err)})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), handleTimeout)
	defer cancel()
	_ = enc.Encode(s.Handle(ctx, req))
}
