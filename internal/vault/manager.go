// Package vault manages the vault-server subprocess lifecycle.
package vault

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

// Manager manages the vault-server subprocess and provides access to tokens.
type Manager struct {
	dataDir    string
	addr       string
	adminToken string
	proxyToken string
	masterPass string // stored for re-authentication if admin token is revoked
	mu         sync.Mutex
	cancel     context.CancelFunc

	// Process management: cmd is only accessed via waitCh coordination.
	// spawn() creates cmd + waitCh; watchdog owns Wait(); kill() signals + waits on waitCh.
	cmd    *exec.Cmd
	waitCh chan struct{} // closed when cmd.Wait() returns
}

// NewManager creates a new vault manager.
// dataDir is the path where vault.enc is stored (e.g. /opt/alf/vault-data).
func NewManager(dataDir string) *Manager {
	return &Manager{
		dataDir: dataDir,
		addr:    "http://127.0.0.1:8390",
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
// Stores the admin token and password for re-authentication if needed.
func (m *Manager) AutoUnlock(password string) error {
	c := vaultclient.NewWithToken(m.addr, "")
	token, err := c.Unlock(password)
	if err != nil {
		return fmt.Errorf("unlock vault: %w", err)
	}
	m.mu.Lock()
	m.adminToken = token
	m.masterPass = password
	m.mu.Unlock()
	return nil
}

// EnsureAuth re-authenticates if the admin token has been revoked.
// Returns nil if the admin token is valid, or re-unlocks using stored password.
func (m *Manager) EnsureAuth() error {
	c := m.Client()
	// Quick check: try listing tokens - if 401, re-auth.
	if _, err := c.ListTokens(); err == nil {
		return nil
	}
	m.mu.Lock()
	pw := m.masterPass
	m.mu.Unlock()
	if pw == "" {
		return fmt.Errorf("admin token invalid and no master password stored")
	}
	log.Println("[vault] admin token invalid, re-authenticating...")
	return m.AutoUnlock(pw)
}

// CreateProxyToken creates a proxy-scoped token for Claude subprocess usage.
func (m *Manager) CreateProxyToken() (string, error) {
	c := vaultclient.NewWithToken(m.addr, m.AdminToken())
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

// ClearTokens invalidates stored tokens and master password (e.g. after vault lock).
// After this, EnsureAuth cannot re-unlock - user must unlock manually.
func (m *Manager) ClearTokens() {
	m.mu.Lock()
	m.adminToken = ""
	m.proxyToken = ""
	m.masterPass = ""
	m.mu.Unlock()
}

// Addr returns the vault-server address.
func (m *Manager) Addr() string {
	return m.addr
}

// PasswordFile returns the path to the persisted master password file
// in the vault data directory (writable, survives container restarts).
func (m *Manager) PasswordFile() string {
	return m.dataDir + "/.master-password"
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
	c := vaultclient.NewWithToken(m.addr, "")
	return c.Health()
}

// Client returns a vault client using the admin token.
func (m *Manager) Client() *vaultclient.Client {
	return vaultclient.NewWithToken(m.addr, m.AdminToken())
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

	bin, err := exec.LookPath("vault-server")
	if err != nil {
		return fmt.Errorf("vault-server not found: %w", err)
	}

	cmd := exec.Command(bin,
		"-listen", "127.0.0.1:8390",
		"-data-dir", m.dataDir,
		"-token-ttl", "8760h", // 1 year - daemon manages token lifecycle
	)
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
	c := vaultclient.NewWithToken(m.addr, "")
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

		// Ensure port is released before respawning.
		m.mu.Lock()
		m.kill() // force-kill any lingering process
		m.mu.Unlock()
		time.Sleep(500 * time.Millisecond) // let OS release the port (TIME_WAIT)

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
		m.mu.Lock()
		pw := m.masterPass
		m.mu.Unlock()
		if pw != "" {
			if err := m.AutoUnlock(pw); err != nil {
				log.Printf("[vault] re-unlock after restart failed: %v", err)
			} else if _, err := m.CreateProxyToken(); err != nil {
				log.Printf("[vault] re-create proxy token failed: %v", err)
			} else {
				os.Setenv("VAULT_ADDR", m.addr)
				os.Setenv("VAULT_TOKEN", m.ProxyToken())
				log.Println("[vault] re-authenticated after restart, proxy token updated")
			}
		} else if status, err := m.Health(); err == nil {
			log.Printf("[vault] status after restart: %s", status)
		}
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
