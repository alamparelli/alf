// Package secrets is the Secrets facet of Sandbox: it manages the
// vault-server subprocess lifecycle and exposes per-capability vault access
// via a scoped HTTP proxy. Moved from internal/vault during #339 (Step 3).
package secrets

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	vaultclient "github.com/alessandrolamparelli/vault-proxy/pkg/client"
)

// vaultSocketMode restricts the daemon vault socket to the alfd group.
// The daemon runs as alfd; the socket is created inside vault-data
// (0700 alfd:alfd), which already gates access — this is defense in depth.
// Per-app proxy sockets (see proxy.go) expose scoped access to alf (uid 1000).
const vaultSocketMode os.FileMode = 0660

// Manager manages the vault-server subprocess and provides access to tokens.
type Manager struct {
	dataDir      string
	socketPath   string // Unix socket path for vault-server
	httpProxyURL string // optional: HTTP proxy for outbound vault-proxy requests
	adminToken   string
	proxyToken   string
	mu           sync.Mutex
	cancel       context.CancelFunc

	// OnTokenUpdate is called after vault restart re-authentication succeeds.
	// Used to notify proxies (LLM + per-app) of the new proxy token.
	OnTokenUpdate func(proxyToken string)

	// Process management: cmd is only accessed via waitCh coordination.
	// spawn() creates cmd + waitCh; watchdog owns Wait(); kill() signals + waits on waitCh.
	cmd    *exec.Cmd
	waitCh chan struct{} // closed when cmd.Wait() returns
}

// NewManager creates a new vault manager.
// dataDir is the path where vault.enc is stored (e.g. /opt/alf/vault-data).
// vault-server listens on a Unix socket inside dataDir.
func NewManager(dataDir string) *Manager {
	return &Manager{
		dataDir:    dataDir,
		socketPath: dataDir + "/vault.sock",
	}
}

// Start spawns vault-server and waits for it to become healthy.
// A watchdog goroutine restarts the process on crash with exponential backoff.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	watchCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	if err := m.spawn(); err != nil {
		cancel()
		return err
	}

	if err := m.waitHealthy(10 * time.Second); err != nil {
		m.kill()
		cancel()
		return err
	}

	if err := os.Chmod(m.socketPath, vaultSocketMode); err != nil {
		log.Printf("[vault] chmod socket %s: %v", m.socketPath, err)
	}

	// Watchdog: restart on crash with exponential backoff.
	go m.watchdog(watchCtx)

	return nil
}

// Stop cancels the watchdog and sends SIGTERM to vault-server.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	return m.kill()
}

// AutoUnlock unlocks the vault with the given master password.
// The password is NOT stored in memory — re-authentication reads from PasswordFile().
func (m *Manager) AutoUnlock(password string) error {
	c := vaultclient.NewWithSocket(m.socketPath, "")
	token, err := c.Unlock(password)
	if err != nil {
		return fmt.Errorf("unlock vault: %w", err)
	}
	m.mu.Lock()
	m.adminToken = token
	m.mu.Unlock()
	return nil
}

// EnsureAuth re-authenticates if the admin token has been revoked.
// Returns nil if the admin token is valid, or re-unlocks using the password file.
func (m *Manager) EnsureAuth() error {
	c := m.Client()
	// Quick check: try listing tokens - if 401, re-auth.
	if _, err := c.ListTokens(); err == nil {
		return nil
	}
	pw := m.readPasswordFile()
	if pw == "" {
		return fmt.Errorf("admin token invalid and no master password file")
	}
	log.Println("[vault] admin token invalid, re-authenticating...")
	return m.AutoUnlock(pw)
}

// CreateProxyToken creates a proxy-scoped token for Claude subprocess usage.
func (m *Manager) CreateProxyToken() (string, error) {
	c := vaultclient.NewWithSocket(m.socketPath, m.AdminToken())
	token, err := c.CreateToken("proxy")
	if err != nil {
		return "", fmt.Errorf("create proxy token: %w", err)
	}
	m.mu.Lock()
	m.proxyToken = token
	m.mu.Unlock()
	return token, nil
}

// AdminToken returns the current admin token.
func (m *Manager) AdminToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.adminToken
}

// ProxyToken returns the current proxy-scoped token.
func (m *Manager) ProxyToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.proxyToken
}

// ClearTokens invalidates stored tokens (e.g. after vault lock).
// After this, EnsureAuth re-reads from PasswordFile() to re-unlock.
func (m *Manager) ClearTokens() {
	m.mu.Lock()
	m.adminToken = ""
	m.proxyToken = ""
	m.mu.Unlock()
}

// SocketPath returns the vault-server Unix socket path.
func (m *Manager) SocketPath() string {
	return m.socketPath
}

// SetHTTPProxy configures an HTTP proxy for outbound vault-proxy requests.
// Must be called before Start().
func (m *Manager) SetHTTPProxy(proxyURL string) {
	m.httpProxyURL = proxyURL
}

// PasswordFile returns the path to the persisted master password file.
// SECURITY: this file is in vault-data (mode 0700, owned by daemon/alfd).
// The LLM subprocess (alf/uid 1000) cannot read it.
func (m *Manager) PasswordFile() string {
	return m.dataDir + "/.master-password"
}

// readPasswordFile reads the master password from the persisted file.
// Returns empty string if the file doesn't exist or is unreadable.
func (m *Manager) readPasswordFile() string {
	data, err := os.ReadFile(m.PasswordFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// IsFirstTime returns true if no vault.enc exists yet (fresh setup).
func (m *Manager) IsFirstTime() bool {
	_, err := os.Stat(m.dataDir + "/vault.enc")
	return os.IsNotExist(err)
}

// Reset stops vault-server, deletes vault.enc, clears tokens,
// and restarts vault-server fresh with no encrypted store.
func (m *Manager) Reset() error {
	// Stop cancels the watchdog and kills the process - no respawn race.
	m.Stop()

	// Delete the encrypted store while nothing is running.
	path := m.dataDir + "/vault.enc"
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete vault.enc: %w", err)
	}
	m.ClearTokens()

	// Restart with a fresh watchdog via Start().
	if err := m.Start(context.Background()); err != nil {
		return fmt.Errorf("restart vault-server: %w", err)
	}

	log.Println("[vault] reset complete - vault.enc deleted, server restarted")
	return nil
}

// Health returns the vault status ("locked" or "unlocked").
func (m *Manager) Health() (string, error) {
	c := vaultclient.NewWithSocket(m.socketPath, "")
	return c.Health()
}

// Client returns a vault client using the admin token.
func (m *Manager) Client() *vaultclient.Client {
	return vaultclient.NewWithSocket(m.socketPath, m.AdminToken())
}

// GetSecret reads a secret file from the vault and returns its contents as a string.
func (m *Manager) GetSecret(name string) (string, error) {
	data, err := m.Client().GetFile(name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// SetSecret writes a secret to the vault as an encrypted file.
func (m *Manager) SetSecret(name, value string) error {
	tmp, err := os.CreateTemp("", "vault-secret-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(value); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	return m.Client().UploadFile(name, tmp.Name())
}

// spawn starts vault-server and sets up waitCh. Must be called with m.mu held.
func (m *Manager) spawn() error {
	// Kill any lingering process before spawning a new one.
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		if m.waitCh != nil {
			<-m.waitCh
		}
		m.cmd = nil
		m.waitCh = nil
	}

	// Kill orphaned vault-server processes from previous daemon runs.
	killOrphans()

	// Remove stale socket before spawning (vault-server also does this,
	// but we clean up proactively in case a previous crash left it).
	os.Remove(m.socketPath)

	bin, err := exec.LookPath("vault-server")
	if err != nil {
		return fmt.Errorf("vault-server not found: %w", err)
	}

	args := []string{
		"-listen", "unix:" + m.socketPath,
		"-data-dir", m.dataDir,
		"-token-ttl", "8760h", // 1 year - daemon manages token lifecycle
	}
	if m.httpProxyURL != "" {
		args = append(args, "-http-proxy", m.httpProxyURL)
	}
	cmd := exec.Command(bin, args...)
	// Route subprocess output through Go's log package instead of raw os.Stdout.
	// Direct pipe inheritance can cause SIGPIPE in containerized environments
	// when the logging driver pipe breaks, killing the subprocess.
	cmd.Stdout = &logWriter{prefix: "[vault-server] "}
	cmd.Stderr = &logWriter{prefix: "[vault-server] "}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // own process group for clean shutdown
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start vault-server: %w", err)
	}

	m.cmd = cmd
	m.waitCh = make(chan struct{})

	// Single goroutine owns cmd.Wait() - no other code calls it.
	go func() {
		_ = cmd.Wait()
		close(m.waitCh)
	}()

	log.Printf("[vault] process started (pid=%d)", cmd.Process.Pid)
	return nil
}

// kill signals the process and waits for exit. Must be called with m.mu held.
func (m *Manager) kill() error {
	if m.cmd == nil || m.cmd.Process == nil {
		return nil
	}

	_ = m.cmd.Process.Signal(syscall.SIGTERM)

	select {
	case <-m.waitCh:
		// Exited gracefully.
	case <-time.After(5 * time.Second):
		_ = m.cmd.Process.Kill()
		<-m.waitCh
	}

	m.cmd = nil
	m.waitCh = nil
	return nil
}

func (m *Manager) waitHealthy(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	c := vaultclient.NewWithSocket(m.socketPath, "")
	for time.Now().Before(deadline) {
		if _, err := c.Health(); err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("vault-server not healthy after %v", timeout)
}

func (m *Manager) watchdog(ctx context.Context) {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		// Wait for process exit via the channel (no direct cmd.Wait call).
		m.mu.Lock()
		wch := m.waitCh
		m.mu.Unlock()

		if wch != nil {
			select {
			case <-wch:
				// Process exited.
			case <-ctx.Done():
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		log.Printf("[vault] process exited, restarting in %v...", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Ensure process is dead and socket is cleaned up before respawning.
		m.mu.Lock()
		m.kill()
		os.Remove(m.socketPath)
		m.mu.Unlock()

		m.mu.Lock()
		err := m.spawn()
		m.mu.Unlock()
		if err != nil {
			log.Printf("[vault] restart failed: %v", err)
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		if err := m.waitHealthy(10 * time.Second); err != nil {
			log.Printf("[vault] not healthy after restart: %v", err)
			m.mu.Lock()
			m.kill()
			m.mu.Unlock()
			backoff = min(backoff*2, maxBackoff)
			continue
		}

		log.Println("[vault] restarted successfully")
		backoff = time.Second

		// Re-authenticate after restart (token store is in-memory, lost on crash).
		pw := m.readPasswordFile()
		if pw != "" {
			if err := m.AutoUnlock(pw); err != nil {
				log.Printf("[vault] re-unlock after restart failed: %v", err)
			} else if _, err := m.CreateProxyToken(); err != nil {
				log.Printf("[vault] re-create proxy token failed: %v", err)
			} else {
				log.Println("[vault] re-authenticated after restart, proxy token updated")
				if m.OnTokenUpdate != nil {
					m.OnTokenUpdate(m.ProxyToken())
				}
			}
		} else if status, err := m.Health(); err == nil {
			log.Printf("[vault] status after restart: %s", status)
		}
	}
}

// killOrphans finds and kills any vault-server processes not managed by this Manager.
// This handles the case where a previous daemon died without cleaning up its vault-server
// child (which runs in its own process group via Setpgid).
func killOrphans() {
	out, err := exec.Command("pgrep", "-f", "vault-server.*-listen").Output()
	if err != nil || len(out) == 0 {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		pid := 0
		if _, err := fmt.Sscanf(line, "%d", &pid); err != nil || pid <= 1 {
			continue
		}
		p, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		log.Printf("[vault] killing orphaned vault-server (pid=%d)", pid)
		_ = p.Signal(syscall.SIGTERM)
		// Give it a moment to shut down gracefully.
		time.Sleep(500 * time.Millisecond)
		_ = p.Signal(syscall.SIGKILL)
	}
}

// logWriter routes subprocess output through Go's log package line-by-line.
// This avoids raw pipe inheritance which can cause SIGPIPE in containers.
type logWriter struct {
	prefix string
	buf    []byte
}

func (w *logWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := -1
		for i, b := range w.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		if line != "" {
			log.Printf("%s%s", w.prefix, line)
		}
	}
	return len(p), nil
}
