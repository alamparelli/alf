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
	AuthToken     string
	Sessions      *SessionStore
	AllowedOrigin string // e.g. "https://cc.lamparelli.eu" - restricts WebSocket origin
}

func (h *TerminalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Inline auth check (middleware can't wrap this handler).
	if !h.checkAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Restrict WebSocket origin to prevent cross-site WebSocket hijacking.
	originPatterns := []string{"*"}
	if h.AllowedOrigin != "" {
		originPatterns = []string{h.AllowedOrigin}
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: originPatterns,
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
	// Daemon already runs as uid 1000 (alf) — no credential switch needed.
	cmd := exec.CommandContext(ctx, shell, "--login")
	homeDir := "/home/alf"
	if d := os.Getenv("ALF_HOME_DIR"); d != "" {
		homeDir = d
	}
	cmd.Dir = homeDir
	// Build a safe environment - exclude daemon secrets (OAuth tokens, API keys, etc.).
	env := termSafeEnv(homeDir)
	cmd.Env = env

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

// termSafeEnv builds a filtered environment for terminal sessions,
// excluding daemon secrets (OAuth tokens, API keys, auth tokens).
func termSafeEnv(homeDir string) []string {
	safePrefixes := []string{
		"PATH=", "TERM=", "LANG=", "LC_", "TZ=", "TMPDIR=",
		"XDG_", "OMP_NUM_THREADS=", "ANTHROPIC_", "CLAUDE_",
	}
	localBin := homeDir + "/.local/bin"
	env := make([]string, 0, 16)
	for _, e := range os.Environ() {
		for _, prefix := range safePrefixes {
			if strings.HasPrefix(e, prefix) {
				if strings.HasPrefix(e, "PATH=") {
					e = "PATH=" + localBin + ":" + strings.TrimPrefix(e, "PATH=")
				}
				env = append(env, e)
				break
			}
		}
	}
	env = append(env,
		"HOME="+homeDir,
		"USER=alf",
		"LOGNAME=alf",
		"TERM=xterm-256color",
	)
	return env
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
