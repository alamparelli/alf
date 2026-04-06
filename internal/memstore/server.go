package memstore

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
)

const (
	maxRequestSize = 64 * 1024 // 64KB max JSON request
	maxTextSize    = 10 * 1024 // 10KB max memory text
)

var validMemTypes = map[string]bool{
	"fact":       true,
	"summary":    true,
	"preference": true,
	"decision":   true,
}

// socketRequest is the JSON protocol for tool → daemon communication.
type socketRequest struct {
	Action string `json:"action"` // "search", "store", "recent", "delete"
	Query  string `json:"query,omitempty"`
	Text   string `json:"text,omitempty"`
	Type   string `json:"type,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Days   int    `json:"days,omitempty"`
	ID     int64  `json:"id,omitempty"`
}

// socketResponse is sent back to the tool.
type socketResponse struct {
	Results []Memory `json:"results,omitempty"`
	ID      int64    `json:"id,omitempty"`
	Count   int      `json:"count,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// ServeUnix starts listening on a unix domain socket for tool requests.
// Blocks until the listener is closed. Call in a goroutine.
func (s *Store) ServeUnix(sockPath string) error {
	// Remove stale socket file.
	os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", sockPath, err)
	}

	// Daemon runs as alfd (uid 1001, gid 1001). Set group to alf (1000) for subprocess access.
	// chown/chmod may fail on Docker Desktop volume mounts (VirtioFS) — non-fatal.
	if err := os.Chown(sockPath, -1, 1000); err != nil {
		log.Printf("memstore: chown %s: %v (non-root?)", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0660); err != nil {
		log.Printf("memstore: chmod %s: %v (continuing anyway)", sockPath, err)
	}

	log.Printf("memstore: socket server listening on %s", sockPath)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed") {
				return nil // clean shutdown
			}
			log.Printf("memstore: accept error: %v", err)
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *Store) handleConn(conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(io.LimitReader(conn, maxRequestSize))
	enc := json.NewEncoder(conn)

	var req socketRequest
	if err := dec.Decode(&req); err != nil {
		enc.Encode(socketResponse{Error: fmt.Sprintf("decode: %v", err)})
		return
	}

	var resp socketResponse

	switch req.Action {
	case "search":
		limit := req.Limit
		if limit <= 0 {
			limit = 5
		}
		results, err := s.Search(req.Query, limit)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Results = results
			resp.Count = len(results)
		}

	case "store":
		if req.Text == "" {
			resp.Error = "text required"
			break
		}
		if len(req.Text) > maxTextSize {
			resp.Error = fmt.Sprintf("text too large (%d bytes, max %d)", len(req.Text), maxTextSize)
			break
		}
		memType := req.Type
		if memType == "" {
			memType = "fact"
		}
		if !validMemTypes[memType] {
			resp.Error = fmt.Sprintf("invalid type %q (valid: fact, summary, preference, decision)", memType)
			break
		}
		id, err := s.Store(req.Text, memType, "claude", nil)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.ID = id
		}

	case "recent":
		days := req.Days
		if days <= 0 {
			days = 3
		}
		limit := req.Limit
		if limit <= 0 {
			limit = 10
		}
		results, err := s.Recent(days, limit)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Results = results
			resp.Count = len(results)
		}

	case "delete":
		if req.ID <= 0 {
			resp.Error = "id required for delete"
		} else if err := s.Delete(req.ID); err != nil {
			resp.Error = err.Error()
		} else {
			resp.ID = req.ID
		}

	default:
		resp.Error = fmt.Sprintf("unknown action: %s", req.Action)
	}

	enc.Encode(resp)
}
