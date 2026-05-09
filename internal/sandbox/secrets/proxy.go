package secrets

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/platform/socknet"
)

// VaultProxy is an HTTP reverse proxy to vault-server over Unix socket.
// It injects authentication server-side so consumers never see vault tokens.
//
// Two tiers use the same type:
//   - LLM proxy: allowedServices is nil → all /proxy/{service}/... requests pass through
//   - App proxy: allowedServices is set → only declared services are allowed
//
// All non-proxy paths (/health, /auth/*, /tokens, /files, /services, /ssh/*) are blocked.
type VaultProxy struct {
	allowedServices map[string]bool // nil = allow all (LLM tier)
	vaultSocket     string          // vault-server Unix socket path
	proxyToken      string          // injected into upstream requests
	mu              sync.RWMutex    // protects proxyToken for hot-reload
	transport       *http.Transport // reusable transport to vault socket
}

// NewVaultProxy creates a proxy to vault-server.
// If services is nil, all services are allowed (LLM tier).
// If services is non-nil, only listed services pass through (app tier).
func NewVaultProxy(vaultSocket, proxyToken string, services []string) *VaultProxy {
	p := &VaultProxy{
		vaultSocket: vaultSocket,
		proxyToken:  proxyToken,
		transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", vaultSocket)
			},
			MaxIdleConns:    10,
			IdleConnTimeout: 60 * time.Second,
		},
	}
	if services != nil {
		p.allowedServices = make(map[string]bool, len(services))
		for _, s := range services {
			p.allowedServices[s] = true
		}
	}
	return p
}

// UpdateToken replaces the proxy token (e.g. after vault-server restart).
func (p *VaultProxy) UpdateToken(token string) {
	p.mu.Lock()
	p.proxyToken = token
	p.mu.Unlock()
}

func (p *VaultProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Allow /proxy/{service}/... and /ssh/{service}/... requests.
	// /health is also allowed (no auth needed, useful for diagnostics).
	path := r.URL.Path
	var service string
	switch {
	case strings.HasPrefix(path, "/proxy/"):
		rest := strings.TrimPrefix(path, "/proxy/")
		if idx := strings.IndexByte(rest, '/'); idx >= 0 {
			service = rest[:idx]
		} else {
			service = rest
		}
	case strings.HasPrefix(path, "/ssh/"):
		rest := strings.TrimPrefix(path, "/ssh/")
		if idx := strings.IndexByte(rest, '/'); idx >= 0 {
			service = rest[:idx]
		} else {
			service = rest
		}
	case path == "/health":
		// Pass through to vault-server (no service check needed).
	case path == "/services":
		// Allow listing services (proxy scope).
	default:
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	if service == "" && path != "/health" && path != "/services" {
		http.Error(w, `{"error":"missing service name"}`, http.StatusBadRequest)
		return
	}

	// Check service allowlist (nil = allow all for LLM tier).
	if service != "" && p.allowedServices != nil && !p.allowedServices[service] {
		http.Error(w, `{"error":"service not allowed"}`, http.StatusForbidden)
		return
	}

	// Build upstream request to vault-server.
	upstreamURL := "http://localhost" + path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	// SEC-006: Limit request body to 10MB to prevent resource exhaustion.
	body := io.LimitReader(r.Body, 10<<20)
	upReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, body)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	// Copy safe headers from original request.
	for _, h := range []string{"Content-Type", "Accept", "Content-Length"} {
		if v := r.Header.Get(h); v != "" {
			upReq.Header.Set(h, v)
		}
	}

	// Inject proxy token (server-side — consumer never sees it).
	p.mu.RLock()
	token := p.proxyToken
	p.mu.RUnlock()
	if token != "" {
		upReq.Header.Set("Authorization", "Bearer "+token)
	}

	// Send to vault-server via Unix socket.
	resp, err := p.transport.RoundTrip(upReq)
	if err != nil {
		http.Error(w, `{"error":"vault unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Forward response headers and body.
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ListenAndServe starts the proxy on a Unix socket.
// The socket is accessible to uid 1000 (alf group) via filesystem permissions.
// Returns the listener for the caller to manage lifecycle.
func (p *VaultProxy) ListenAndServe(sockPath string) (net.Listener, error) {
	os.Remove(sockPath) // clear stale

	// SEC-407-002: vault-proxy was the original site of the
	// umask-around-Listen pattern (SEC-002 from the 0.7.9 audit).
	// The pattern is now extracted into socknet.ListenUnix0660 so
	// every Unix-socket creator in the codebase shares one secure
	// implementation. Errors are non-fatal (e.g. tests without
	// CAP_CHOWN).
	ln, err := socknet.ListenUnix0660(sockPath, 1000)
	if err != nil {
		return nil, err
	}

	srv := &http.Server{Handler: p}
	go func() {
		if err := srv.Serve(ln); err != nil && !strings.Contains(err.Error(), "use of closed") {
			log.Printf("[vault-proxy] serve error: %v", err)
		}
	}()

	return ln, nil
}
