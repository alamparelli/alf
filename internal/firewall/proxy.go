package firewall

import (
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elazarl/goproxy"
)

// Mode controls whether deny rules are enforced or just logged.
type Mode string

const (
	ModeLogOnly Mode = "log-only"
	ModeEnforce Mode = "enforce"
)

// Rule defines a domain pattern match and action.
type Rule struct {
	Pattern string `json:"pattern"` // "*.anthropic.com", "api.telegram.org", "*"
	Action  string `json:"action"`  // "allow", "deny"
}

// Config holds the firewall proxy configuration.
type Config struct {
	Mode  Mode   `json:"mode"`
	Port  int    `json:"port"`
	Rules []Rule `json:"rules"`
}

// DefaultConfig returns sensible defaults (log-only, no rules).
func DefaultConfig() *Config {
	return &Config{
		Mode:  ModeLogOnly,
		Port:  4751,
		Rules: []Rule{},
	}
}

// RequestEntry records a single proxied request.
type RequestEntry struct {
	Time    time.Time `json:"time"`
	Method  string    `json:"method"`
	Host    string    `json:"host"`
	Path    string    `json:"path"`
	Status  int       `json:"status"`
	Blocked bool      `json:"blocked"`
	Rule    string    `json:"rule"`
}

// ringSize is the maximum number of logged requests.
const ringSize = 500

// RingBuffer is a fixed-size circular buffer for request entries.
type RingBuffer struct {
	mu    sync.Mutex
	items [ringSize]RequestEntry
	pos   int
	count int
}

// Add appends an entry to the ring buffer.
func (r *RingBuffer) Add(e RequestEntry) {
	r.mu.Lock()
	r.items[r.pos] = e
	r.pos = (r.pos + 1) % ringSize
	if r.count < ringSize {
		r.count++
	}
	r.mu.Unlock()
}

// Entries returns a copy of all entries in chronological order.
func (r *RingBuffer) Entries() []RequestEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count == 0 {
		return nil
	}
	out := make([]RequestEntry, r.count)
	start := (r.pos - r.count + ringSize) % ringSize
	for i := 0; i < r.count; i++ {
		out[i] = r.items[(start+i)%ringSize]
	}
	return out
}

// Clear removes all entries.
func (r *RingBuffer) Clear() {
	r.mu.Lock()
	r.pos = 0
	r.count = 0
	r.mu.Unlock()
}

// Proxy wraps a goproxy server with domain-based firewall rules.
type Proxy struct {
	server *goproxy.ProxyHttpServer
	config atomic.Pointer[Config]
	Log    *RingBuffer
}

// NewProxy creates a firewall proxy with the given initial config.
func NewProxy(cfg *Config) *Proxy {
	p := &Proxy{
		Log: &RingBuffer{},
	}
	p.config.Store(cfg)

	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = false

	// Prevent infinite loop: proxy's own transport must not use HTTP_PROXY.
	proxy.Tr = &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:  10 * time.Second,
		MaxIdleConns:          50,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// HTTP request handler.
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		host := stripPort(req.Host)
		rule, action := p.match(host)
		blocked := action == "deny" && p.currentConfig().Mode == ModeEnforce

		entry := RequestEntry{
			Time:    time.Now(),
			Method:  req.Method,
			Host:    host,
			Path:    req.URL.Path,
			Blocked: blocked,
			Rule:    rule,
		}

		if blocked {
			entry.Status = http.StatusForbidden
			p.Log.Add(entry)
			log.Printf("[firewall] BLOCKED %s %s (rule: %s)", req.Method, host, rule)
			return req, goproxy.NewResponse(req, goproxy.ContentTypeText, http.StatusForbidden, "blocked by firewall")
		}

		p.Log.Add(entry)
		return req, nil
	})

	// HTTPS CONNECT handler (domain-level only, no MITM).
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		hostname := stripPort(host)
		rule, action := p.match(hostname)
		blocked := action == "deny" && p.currentConfig().Mode == ModeEnforce

		entry := RequestEntry{
			Time:    time.Now(),
			Method:  "CONNECT",
			Host:    hostname,
			Blocked: blocked,
			Rule:    rule,
		}

		if blocked {
			entry.Status = http.StatusForbidden
			p.Log.Add(entry)
			log.Printf("[firewall] BLOCKED CONNECT %s (rule: %s)", hostname, rule)
			return goproxy.RejectConnect, host
		}

		p.Log.Add(entry)
		return goproxy.OkConnect, host
	})

	p.server = proxy
	return p
}

// Reload atomically swaps the config.
func (p *Proxy) Reload(cfg *Config) {
	p.config.Store(cfg)
	log.Printf("[firewall] config reloaded: mode=%s, %d rules", cfg.Mode, len(cfg.Rules))
}

// Handler returns the HTTP handler for use with http.ListenAndServe.
func (p *Proxy) Handler() http.Handler {
	return p.server
}

// currentConfig returns the current config snapshot.
func (p *Proxy) currentConfig() *Config {
	return p.config.Load()
}

// match returns the matching rule pattern and action for a host.
// Returns ("", "") if no rule matches.
func (p *Proxy) match(host string) (pattern, action string) {
	cfg := p.currentConfig()
	host = strings.ToLower(host)
	for _, r := range cfg.Rules {
		if matchPattern(r.Pattern, host) {
			return r.Pattern, r.Action
		}
	}
	return "", ""
}

// matchPattern checks if host matches a rule pattern.
// Supports exact match, wildcard "*", and prefix wildcard "*.example.com".
func matchPattern(pattern, host string) bool {
	pattern = strings.ToLower(pattern)
	if pattern == "*" {
		return true
	}
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		return strings.HasSuffix(host, suffix)
	}
	return false
}

// stripPort removes the port from a host:port string.
func stripPort(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport // no port
	}
	return host
}
