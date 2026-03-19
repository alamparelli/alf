package signal

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
)

// Sender abstracts Telegram message operations for testability.
type Sender interface {
	SetMessageReaction(chatID, messageID int64, emoji string) error
	SendMessage(chatID int64, text string) error
}

// Notifier broadcasts a message to all configured channels.
type Notifier func(text string)

// Request is the JSON protocol from CLI tool → daemon.
type Request struct {
	Action string `json:"action"` // "react" | "status" | "notify"
	Emoji  string `json:"emoji,omitempty"`
	Text   string `json:"text,omitempty"`
}

// Response is sent back to the CLI tool.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Server handles signal requests from Claude subprocesses.
type Server struct {
	TG        Sender
	ChatID    int64
	MessageID int64
	Notify    Notifier // optional: broadcast to all channels
}

// ListenUnix creates a Unix socket listener with correct permissions.
// The caller should defer ln.Close() and os.Remove(sockPath).
func (s *Server) ListenUnix(sockPath string) (net.Listener, error) {
	os.Remove(sockPath) // remove stale socket

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", sockPath, err)
	}

	// Daemon runs as uid 1000 — socket is already owned correctly.
	os.Chmod(sockPath, 0660)

	return ln, nil
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed") {
				return // clean shutdown
			}
			log.Printf("signal: accept error: %v", err)
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var req Request
	if err := dec.Decode(&req); err != nil {
		enc.Encode(Response{Error: fmt.Sprintf("decode: %v", err)})
		return
	}

	var resp Response

	switch req.Action {
	case "react":
		if req.Emoji == "" {
			resp.Error = "emoji required"
		} else if err := s.TG.SetMessageReaction(s.ChatID, s.MessageID, req.Emoji); err != nil {
			resp.Error = err.Error()
		} else {
			resp.OK = true
		}

	case "status":
		if req.Text == "" {
			resp.Error = "text required"
		} else if err := s.TG.SendMessage(s.ChatID, req.Text); err != nil {
			resp.Error = err.Error()
		} else {
			resp.OK = true
		}

	case "notify":
		if req.Text == "" {
			resp.Error = "text required"
		} else if s.Notify == nil {
			resp.Error = "notify not configured"
		} else {
			s.Notify(req.Text)
			resp.OK = true
		}

	default:
		resp.Error = fmt.Sprintf("unknown action: %s", req.Action)
	}

	enc.Encode(resp)
}
