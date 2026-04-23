package signal

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type mockSender struct {
	lastReaction string
	lastMessage  string
	reactErr     error
	sendErr      error
}

func (m *mockSender) SetMessageReaction(chatID, messageID int64, emoji string) error {
	m.lastReaction = emoji
	return m.reactErr
}

func (m *mockSender) SendMessage(chatID int64, text string) error {
	m.lastMessage = text
	return m.sendErr
}

func setupServer(t *testing.T, sender *mockSender) (string, func()) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "signal.sock")
	srv := &Server{TG: sender, ChatID: 123, MessageID: 456}

	ln, err := srv.ListenUnix(sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve(ln)

	return sockPath, func() {
		ln.Close()
		os.Remove(sockPath)
	}
}

func call(sockPath string, req Request) Response {
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return Response{Error: fmt.Sprintf("dial: %v", err)}
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	json.NewEncoder(conn).Encode(req)
	var resp Response
	json.NewDecoder(conn).Decode(&resp)
	return resp
}

func TestReact(t *testing.T) {
	sender := &mockSender{}
	sockPath, cleanup := setupServer(t, sender)
	defer cleanup()

	resp := call(sockPath, Request{Action: "react", Emoji: "👀"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if sender.lastReaction != "👀" {
		t.Fatalf("expected 👀, got %s", sender.lastReaction)
	}
}

func TestReactMissingEmoji(t *testing.T) {
	sender := &mockSender{}
	sockPath, cleanup := setupServer(t, sender)
	defer cleanup()

	resp := call(sockPath, Request{Action: "react"})
	if resp.OK || resp.Error == "" {
		t.Fatal("expected error for missing emoji")
	}
}

func TestReactTelegramError(t *testing.T) {
	sender := &mockSender{reactErr: fmt.Errorf("telegram: 400")}
	sockPath, cleanup := setupServer(t, sender)
	defer cleanup()

	resp := call(sockPath, Request{Action: "react", Emoji: "👍"})
	if resp.OK {
		t.Fatal("expected error")
	}
	if resp.Error != "telegram: 400" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestStatus(t *testing.T) {
	sender := &mockSender{}
	sockPath, cleanup := setupServer(t, sender)
	defer cleanup()

	resp := call(sockPath, Request{Action: "status", Text: "Working on it..."})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if sender.lastMessage != "Working on it..." {
		t.Fatalf("expected message, got %s", sender.lastMessage)
	}
}

func TestStatusMissingText(t *testing.T) {
	sender := &mockSender{}
	sockPath, cleanup := setupServer(t, sender)
	defer cleanup()

	resp := call(sockPath, Request{Action: "status"})
	if resp.OK || resp.Error == "" {
		t.Fatal("expected error for missing text")
	}
}

func TestUnknownAction(t *testing.T) {
	sender := &mockSender{}
	sockPath, cleanup := setupServer(t, sender)
	defer cleanup()

	resp := call(sockPath, Request{Action: "bogus"})
	if resp.OK {
		t.Fatal("expected error for unknown action")
	}
}

// ---------------------------------------------------------------------------
// Input validation regression tests (#13, #14)
// ---------------------------------------------------------------------------

func TestOversizedPayload(t *testing.T) {
	sender := &mockSender{}
	sockPath, cleanup := setupServer(t, sender)
	defer cleanup()

	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send 20KB payload (> 16KB limit). Decoder should fail.
	huge := `{"action":"notify","text":"` + strings.Repeat("x", 20*1024) + `"}`
	conn.Write([]byte(huge + "\n"))

	var resp Response
	json.NewDecoder(conn).Decode(&resp)
	if resp.OK {
		t.Fatal("expected error for oversized payload")
	}
	if resp.Error == "" {
		t.Fatal("expected error message for oversized payload")
	}
}

func TestNotifyAction(t *testing.T) {
	var notified string
	sender := &mockSender{}
	sockPath := filepath.Join(t.TempDir(), "signal.sock")
	srv := &Server{
		TG: sender, ChatID: 123, MessageID: 456,
		Notify: func(text string) { notified = text },
	}

	ln, err := srv.ListenUnix(sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve(ln)
	defer func() { ln.Close(); os.Remove(sockPath) }()

	resp := call(sockPath, Request{Action: "notify", Text: "hello"})
	if !resp.OK {
		t.Fatalf("expected ok, got: %s", resp.Error)
	}
	if notified != "hello" {
		t.Fatalf("expected 'hello', got %q", notified)
	}
}

func TestNotifyMissingText(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "signal.sock")
	srv := &Server{
		TG: &mockSender{}, ChatID: 123, MessageID: 456,
		Notify: func(text string) {},
	}

	ln, err := srv.ListenUnix(sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve(ln)
	defer func() { ln.Close(); os.Remove(sockPath) }()

	resp := call(sockPath, Request{Action: "notify"})
	if resp.OK {
		t.Fatal("expected error for missing text")
	}
}
