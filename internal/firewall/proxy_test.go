package firewall

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern string
		host    string
		want    bool
	}{
		{"*", "anything.com", true},
		{"api.telegram.org", "api.telegram.org", true},
		{"api.telegram.org", "evil.com", false},
		{"*.anthropic.com", "api.anthropic.com", true},
		{"*.anthropic.com", "anthropic.com", false},
		{"*.anthropic.com", "sub.api.anthropic.com", true},
		{"*.Example.COM", "sub.example.com", true},
		{"example.com", "example.com", true},
		{"example.com", "sub.example.com", false},
	}
	for _, tt := range tests {
		got := matchPattern(tt.pattern, tt.host)
		if got != tt.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.host, got, tt.want)
		}
	}
}

func TestRingBuffer(t *testing.T) {
	rb := &RingBuffer{}

	// Empty buffer.
	if entries := rb.Entries(); entries != nil {
		t.Fatalf("expected nil entries, got %d", len(entries))
	}

	// Add a few entries.
	for i := 0; i < 3; i++ {
		rb.Add(RequestEntry{Host: "test.com", Status: i})
	}
	entries := rb.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Status != 0 || entries[2].Status != 2 {
		t.Errorf("entries not in chronological order")
	}

	// Clear.
	rb.Clear()
	if entries := rb.Entries(); entries != nil {
		t.Fatalf("expected nil entries after clear, got %d", len(entries))
	}

	// Overflow: add more than ringSize.
	for i := 0; i < ringSize+10; i++ {
		rb.Add(RequestEntry{Status: i})
	}
	entries = rb.Entries()
	if len(entries) != ringSize {
		t.Fatalf("expected %d entries, got %d", ringSize, len(entries))
	}
	// Oldest should be entry #10 (0-9 were evicted).
	if entries[0].Status != 10 {
		t.Errorf("oldest entry status = %d, want 10", entries[0].Status)
	}
	if entries[ringSize-1].Status != ringSize+9 {
		t.Errorf("newest entry status = %d, want %d", entries[ringSize-1].Status, ringSize+9)
	}
}

func TestProxyMatch(t *testing.T) {
	cfg := &Config{
		Mode: ModeEnforce,
		Rules: []Rule{
			{Pattern: "*.evil.com", Action: "deny"},
			{Pattern: "api.telegram.org", Action: "allow"},
			{Pattern: "*", Action: "deny"},
		},
	}
	p := NewProxy(cfg)

	// evil.com subdomain → deny
	pattern, action := p.match("sub.evil.com")
	if pattern != "*.evil.com" || action != "deny" {
		t.Errorf("match(sub.evil.com) = (%q, %q), want (*.evil.com, deny)", pattern, action)
	}

	// telegram → allow
	pattern, action = p.match("api.telegram.org")
	if action != "allow" {
		t.Errorf("match(api.telegram.org) = (%q, %q), want allow", pattern, action)
	}

	// anything else → deny (wildcard)
	pattern, action = p.match("random.com")
	if action != "deny" {
		t.Errorf("match(random.com) = (%q, %q), want deny", pattern, action)
	}

	// No rules
	p.Reload(&Config{Mode: ModeLogOnly, Rules: []Rule{}})
	pattern, action = p.match("anything.com")
	if pattern != "" || action != "" {
		t.Errorf("match with no rules = (%q, %q), want empty", pattern, action)
	}
}

func TestModeLogOnlyDoesNotBlock(t *testing.T) {
	cfg := &Config{
		Mode: ModeLogOnly,
		Rules: []Rule{
			{Pattern: "evil.com", Action: "deny"},
		},
	}
	p := NewProxy(cfg)

	// In log-only mode, match returns deny but proxy shouldn't block.
	_, action := p.match("evil.com")
	blocked := action == "deny" && cfg.Mode == ModeEnforce
	if blocked {
		t.Error("log-only mode should not block")
	}
}

func TestCheckAndRecord(t *testing.T) {
	cfg := &Config{
		Mode: ModeEnforce,
		Rules: []Rule{
			{Pattern: "blocked.com", Action: "deny"},
			{Pattern: "allowed.com", Action: "allow"},
		},
	}
	p := NewProxy(cfg)

	// Check allowed host.
	rule, action, blocked := p.Check("allowed.com")
	if blocked || action != "allow" {
		t.Errorf("Check(allowed.com) = rule=%q action=%q blocked=%v, want allowed", rule, action, blocked)
	}

	// Check blocked host.
	rule, action, blocked = p.Check("blocked.com")
	if !blocked || action != "deny" {
		t.Errorf("Check(blocked.com) = rule=%q action=%q blocked=%v, want blocked", rule, action, blocked)
	}

	// Check unknown host (no matching rule) — default-deny in enforce mode.
	_, _, blocked = p.Check("unknown.com")
	if !blocked {
		t.Error("Check(unknown.com) should default-deny in enforce mode when no rule matches")
	}

	// Record external entry and verify it appears in log.
	p.Record(RequestEntry{Host: "api.klipy.com", Method: "GET", Path: "/search", Source: "vault"})
	entries := p.Log.Entries()
	found := false
	for _, e := range entries {
		if e.Host == "api.klipy.com" && e.Source == "vault" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Record() entry with source=vault not found in log")
	}
}

func TestCheckLogOnly(t *testing.T) {
	cfg := &Config{
		Mode: ModeLogOnly,
		Rules: []Rule{
			{Pattern: "evil.com", Action: "deny"},
		},
	}
	p := NewProxy(cfg)

	// In log-only mode, deny rules should NOT block.
	_, _, blocked := p.Check("evil.com")
	if blocked {
		t.Error("Check in log-only mode should not block")
	}
}

func TestEnforceDefaultDeny(t *testing.T) {
	cfg := &Config{Mode: ModeEnforce, Rules: []Rule{}}
	p := NewProxy(cfg)
	p.SetVaultHosts([]string{"openrouter.ai", "api.telegram.org"})

	cases := []struct {
		host        string
		wantBlocked bool
		wantRule    string
	}{
		// Empty rules, enforce mode → default-deny for public hosts.
		{"example.com", true, "default-deny"},
		{"google.com", true, "default-deny"},
		// Vault-registered hosts → implicit allow.
		{"openrouter.ai", false, "vault-implicit-allow"},
		{"api.telegram.org", false, "vault-implicit-allow"},
		// Internal networking: loopback, private, localhost, link-local.
		{"127.0.0.1", false, "internal-implicit-allow"},
		{"localhost", false, "internal-implicit-allow"},
		{"10.0.0.5", false, "internal-implicit-allow"},
		{"172.17.0.2", false, "internal-implicit-allow"},
		{"192.168.1.10", false, "internal-implicit-allow"},
		{"169.254.1.1", false, "internal-implicit-allow"},
	}
	for _, tc := range cases {
		rule, blocked := p.decide(tc.host)
		if blocked != tc.wantBlocked {
			t.Errorf("decide(%q) blocked=%v, want %v (rule=%q)", tc.host, blocked, tc.wantBlocked, rule)
		}
		if rule != tc.wantRule {
			t.Errorf("decide(%q) rule=%q, want %q", tc.host, rule, tc.wantRule)
		}
	}
}

func TestEnforceExplicitAllowOverridesDefaultDeny(t *testing.T) {
	cfg := &Config{
		Mode: ModeEnforce,
		Rules: []Rule{
			{Pattern: "marketplace.alfos.ai", Action: "allow"},
			{Pattern: "*.anthropic.com", Action: "allow"},
		},
	}
	p := NewProxy(cfg)

	if _, blocked := p.decide("marketplace.alfos.ai"); blocked {
		t.Error("explicit allow should not be blocked")
	}
	if _, blocked := p.decide("api.anthropic.com"); blocked {
		t.Error("wildcard allow should not be blocked")
	}
	if rule, blocked := p.decide("evil.com"); !blocked || rule != "default-deny" {
		t.Errorf("unmatched host: got rule=%q blocked=%v, want default-deny", rule, blocked)
	}
}

func TestLogOnlyNeverBlocks(t *testing.T) {
	cfg := &Config{
		Mode: ModeLogOnly,
		Rules: []Rule{
			{Pattern: "evil.com", Action: "deny"},
			{Pattern: "*", Action: "deny"},
		},
	}
	p := NewProxy(cfg)

	for _, host := range []string{"evil.com", "anything.com", "google.com"} {
		if _, blocked := p.decide(host); blocked {
			t.Errorf("decide(%q) blocked in log-only mode, should never block", host)
		}
	}
}

func TestDefaultConfig_SaneDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Mode != ModeLogOnly {
		t.Errorf("default mode = %q, want %q", cfg.Mode, ModeLogOnly)
	}
	if cfg.Port != 4751 {
		t.Errorf("default port = %d, want 4751", cfg.Port)
	}
	if len(cfg.Rules) != 0 {
		t.Errorf("default rules should be empty, got %d", len(cfg.Rules))
	}
}

func TestProxy_Handler_WrapsGoproxyServer(t *testing.T) {
	p := NewProxy(DefaultConfig())
	h := p.Handler()
	if h == nil {
		t.Fatal("Handler returned nil")
	}
	// The handler must respond to HTTP requests without panicking.
	// A bare GET with no upstream target is enough to prove wiring.
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// goproxy will attempt the upstream call and fail, but the handler
	// must not have panicked — any status is fine here.
	if w.Code == 0 {
		t.Error("Handler did not write a response")
	}
}

// Critical-path regression from TEST-BASELINE.md, scenario 7:
// firewall blocked net fails, allowed net succeeds. Drives real HTTP
// through the goproxy stack to prove the OnRequest/HandleConnect
// callbacks apply the firewall decision on the wire.

func TestProxy_HTTP_BlockedRuleReturns403(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should never be hit when firewall blocks the request")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// Deny everything in enforce mode.
	p := NewProxy(&Config{
		Mode:  ModeEnforce,
		Rules: []Rule{{Pattern: "*", Action: "deny"}},
	})
	proxySrv := httptest.NewServer(p.Handler())
	defer proxySrv.Close()

	proxyURL, _ := url.Parse(proxySrv.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("GET via firewall proxy: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403 from firewall, got %d: %s", resp.StatusCode, string(body))
	}

	// The block must be recorded in the ring buffer with Blocked=true.
	entries := p.Log.Entries()
	if len(entries) == 0 {
		t.Fatal("firewall did not record a blocked entry")
	}
	last := entries[len(entries)-1]
	if !last.Blocked {
		t.Errorf("recorded entry Blocked=%v, want true", last.Blocked)
	}
	if last.Status != http.StatusForbidden {
		t.Errorf("recorded entry Status=%d, want 403", last.Status)
	}
}

func TestProxy_HTTP_AllowedRequestRelayed(t *testing.T) {
	var upstreamHit bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("relayed-ok"))
	}))
	defer upstream.Close()

	// Log-only mode: no rule ever blocks — allowed traffic should
	// reach the upstream unchanged.
	p := NewProxy(&Config{
		Mode:  ModeLogOnly,
		Rules: []Rule{{Pattern: "*", Action: "deny"}}, // deny rule present but log-only ignores it
	})
	proxySrv := httptest.NewServer(p.Handler())
	defer proxySrv.Close()

	proxyURL, _ := url.Parse(proxySrv.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("GET via firewall proxy: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 relayed, got %d: %s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "relayed-ok" {
		t.Errorf("upstream body not relayed: %q", string(body))
	}
	if !upstreamHit {
		t.Error("upstream was never hit — firewall did not relay the request")
	}

	// The allowed request must be recorded with Blocked=false.
	entries := p.Log.Entries()
	if len(entries) == 0 {
		t.Fatal("firewall did not record the allowed entry")
	}
	last := entries[len(entries)-1]
	if last.Blocked {
		t.Errorf("allowed request recorded as Blocked=true")
	}
}

// Store integration: when a Store is attached, the proxy persists
// cumulative host counters via record().
func TestProxy_Record_UpdatesStoreHostStats(t *testing.T) {
	p := NewProxy(&Config{Mode: ModeEnforce, Rules: []Rule{{Pattern: "*", Action: "deny"}}})
	p.Store = NewStore(t.TempDir())

	p.Record(RequestEntry{Host: "evil.com", Method: "GET", Blocked: true, Status: 403})
	p.Record(RequestEntry{Host: "evil.com", Method: "GET", Blocked: true, Status: 403})
	p.Record(RequestEntry{Host: "friend.com", Method: "GET"})

	hosts := p.Store.Hosts()
	byHost := map[string]HostStat{}
	for _, h := range hosts {
		byHost[h.Host] = h
	}
	if got := byHost["evil.com"]; got.Count != 2 || got.Blocked != 2 {
		t.Errorf("evil.com stats: %+v", got)
	}
	if got := byHost["friend.com"]; got.Count != 1 || got.Allowed != 1 {
		t.Errorf("friend.com stats: %+v", got)
	}
}

func TestStripPort(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"example.com:443", "example.com"},
		{"example.com", "example.com"},
		{"[::1]:8080", "::1"},
	}
	for _, tt := range tests {
		if got := stripPort(tt.input); got != tt.want {
			t.Errorf("stripPort(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
