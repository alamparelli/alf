package controlcenter

import (
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"nhooyr.io/websocket"
)

// TerminalHandler upgrades to WebSocket and bridges to a PTY shell.
// Registered outside the middleware stack to preserve http.Hijacker.
type TerminalHandler struct {
	AuthToken     string
	Sessions      *SessionStore
	ExtraTokenFns []func() string // additional valid tokens (e.g. mobile API token)
	AllowedOrigin string          // e.g. "https://cc.example.com" - restricts WebSocket origin
}

func (h *TerminalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Inline auth check (middleware can't wrap this handler).
	if !h.checkAuth(r) {
		respondError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Restrict WebSocket origin to prevent cross-site WebSocket hijacking.
	// When no AllowedOrigin is configured, reject all cross-origin connections
	// (same-origin requests have no Origin header and are always accepted).
	var originPatterns []string
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
	// Terminal runs as alf (uid 1000) — same permissions as the LLM subprocess.
	cmd := exec.CommandContext(ctx, shell, "--login")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
	}
	homeDir := "/home/alf"
	if d := os.Getenv("ALF_HOME_DIR"); d != "" {
		homeDir = d
	}
	cmd.Dir = homeDir
	cmd.Env = termSafeEnv(homeDir, "alf")

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
func termSafeEnv(homeDir, user string) []string {
	safePrefixes := []string{
		"PATH=", "TERM=", "LANG=", "LC_", "TZ=", "TMPDIR=",
		"XDG_", "OMP_NUM_THREADS=",
	}
	// Allow specific ANTHROPIC_/CLAUDE_ vars but never tokens or keys.
	safeExact := map[string]bool{
		"ANTHROPIC_MODEL":  true,
		"CLAUDE_CONFIG_DIR": true,
		"CLAUDE_MODEL":     true,
	}
	localBin := homeDir + "/.local/bin"
	env := make([]string, 0, 16)
	for _, e := range os.Environ() {
		// Check prefix-based allowlist.
		allowed := false
		for _, prefix := range safePrefixes {
			if strings.HasPrefix(e, prefix) {
				allowed = true
				break
			}
		}
		// Check exact var name allowlist (for ANTHROPIC_/CLAUDE_ without leaking tokens).
		if !allowed {
			if eqIdx := strings.IndexByte(e, '='); eqIdx > 0 {
				if safeExact[e[:eqIdx]] {
					allowed = true
				}
			}
		}
		if allowed {
			if strings.HasPrefix(e, "PATH=") {
				e = "PATH=" + localBin + ":" + strings.TrimPrefix(e, "PATH=")
			}
			env = append(env, e)
		}
	}
	env = append(env,
		"HOME="+homeDir,
		"USER="+user,
		"LOGNAME="+user,
		"TERM=xterm-256color",
	)
	return env
}

func (h *TerminalHandler) checkAuth(r *http.Request) bool {
	return checkRequestAuth(r, h.AuthToken, h.Sessions, h.ExtraTokenFns) != authNone
}
