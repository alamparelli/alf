package firewall

import (
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

	// Check unknown host (no matching rule).
	_, action, blocked = p.Check("unknown.com")
	if blocked {
		t.Error("Check(unknown.com) should not block when no rule matches")
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
