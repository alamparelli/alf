package controlcenter

import (
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/creack/pty"
	"nhooyr.io/websocket"
)

// TerminalHandler upgrades to WebSocket and bridges to a PTY shell.
// Registered outside the middleware stack to preserve http.Hijacker.
type TerminalHandler struct {
	AuthToken string
	Sessions  *SessionStore
}

func (h *TerminalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Inline auth check (middleware can't wrap this handler).
	if !h.checkAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("[terminal] websocket accept: %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx := r.Context()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.CommandContext(ctx, shell, "--login")
	env := os.Environ()
	// Ensure ~/.local/bin is in PATH for Claude CLI.
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = e + ":" + os.Getenv("HOME") + "/.local/bin"
			break
		}
	}
	// Override HOME to point to the persistent data volume so that
	// Claude CLI auth (~/.claude/) and binaries (~/.local/bin/) survive
	// container rebuilds.
	dataDir := os.Getenv("ALF_DATA_DIR")
	if dataDir == "" {
		dataDir = "/home/node/data"
	}
	for i, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			env[i] = "HOME=" + dataDir
			break
		}
	}
	cmd.Env = append(env, "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Printf("[terminal] pty start: %v", err)
		conn.Close(websocket.StatusInternalError, "failed to start shell")
		return
	}
	defer ptmx.Close()

	log.Printf("[terminal] session started (shell=%s)", shell)

	var once sync.Once
	done := make(chan struct{})
	cleanup := func() { once.Do(func() { close(done) }) }

	// PTY → WebSocket
	go func() {
		defer cleanup()
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				return
			}
			if err := conn.Write(ctx, websocket.MessageBinary, buf[:n]); err != nil {
				return
			}
		}
	}()

	// WebSocket → PTY
	go func() {
		defer cleanup()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary && len(data) >= 5 {
				// Resize: [1, cols_hi, cols_lo, rows_hi, rows_lo]
				cols := uint16(data[1])<<8 | uint16(data[2])
				rows := uint16(data[3])<<8 | uint16(data[4])
				pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows})
				continue
			}
			// Text message = user input.
			if _, err := ptmx.Write(data); err != nil {
				return
			}
		}
	}()

	<-done
	cmd.Process.Kill()
	cmd.Wait()
	log.Printf("[terminal] session ended")
}

func (h *TerminalHandler) checkAuth(r *http.Request) bool {
	// Bearer token.
	if h.AuthToken != "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") && subtle.ConstantTimeCompare([]byte(auth[7:]), []byte(h.AuthToken)) == 1 {
			return true
		}
	}
	// Session cookie.
	if h.Sessions != nil {
		if c, err := r.Cookie("cc_session"); err == nil && h.Sessions.Valid(c.Value) {
			return true
		}
	}
	return false
}
