package firewall

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNetTrackerDedup(t *testing.T) {
	cfg := &Config{Mode: ModeLogOnly, Rules: []Rule{}}
	p := NewProxy(cfg)
	tracker := NewNetTracker(p, "/nonexistent.sock")

	// First event should be recorded.
	tracker.processEvent(connEvent{Proto: 6, DstIP: "1.2.3.4", DPort: 443, TS: time.Now().Unix()})
	entries := p.Log.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Source != "nettrack" {
		t.Errorf("expected source=nettrack, got %q", entries[0].Source)
	}

	// Duplicate within dedup window should be skipped.
	tracker.processEvent(connEvent{Proto: 6, DstIP: "1.2.3.4", DPort: 443, TS: time.Now().Unix()})
	if len(p.Log.Entries()) != 1 {
		t.Fatalf("duplicate should be deduped, got %d entries", len(p.Log.Entries()))
	}

	// Different port should not be deduped.
	tracker.processEvent(connEvent{Proto: 6, DstIP: "1.2.3.4", DPort: 80, TS: time.Now().Unix()})
	if len(p.Log.Entries()) != 2 {
		t.Fatalf("different port should create new entry, got %d entries", len(p.Log.Entries()))
	}

	// Different protocol should not be deduped.
	tracker.processEvent(connEvent{Proto: 17, DstIP: "1.2.3.4", DPort: 443, TS: time.Now().Unix()})
	if len(p.Log.Entries()) != 3 {
		t.Fatalf("different proto should create new entry, got %d entries", len(p.Log.Entries()))
	}
}

func TestNetTrackerProtocolLabels(t *testing.T) {
	cfg := &Config{Mode: ModeLogOnly, Rules: []Rule{}}
	p := NewProxy(cfg)
	tracker := NewNetTracker(p, "/nonexistent.sock")

	tracker.processEvent(connEvent{Proto: 6, DstIP: "10.0.0.1", DPort: 443, TS: time.Now().Unix()})
	tracker.processEvent(connEvent{Proto: 17, DstIP: "10.0.0.2", DPort: 53, TS: time.Now().Unix()})

	entries := p.Log.Entries()
	if entries[0].Method != "TCP" {
		t.Errorf("expected TCP, got %q", entries[0].Method)
	}
	if entries[1].Method != "UDP" {
		t.Errorf("expected UDP, got %q", entries[1].Method)
	}
}

func TestNetTrackerKillSwitch(t *testing.T) {
	cfg := &Config{Mode: ModeLogOnly, Rules: []Rule{}}
	p := NewProxy(cfg)
	tracker := NewNetTracker(p, "/nonexistent.sock")

	// Without kill switch — not blocked.
	tracker.processEvent(connEvent{Proto: 6, DstIP: "5.5.5.5", DPort: 443, TS: time.Now().Unix()})
	entries := p.Log.Entries()
	if entries[0].Blocked {
		t.Error("should not be blocked without kill switch")
	}

	// Enable kill switch.
	tracker.SetKillSwitch(true)
	if !tracker.KillSwitchActive() {
		t.Error("kill switch should be active")
	}

	tracker.processEvent(connEvent{Proto: 6, DstIP: "6.6.6.6", DPort: 80, TS: time.Now().Unix()})
	entries = p.Log.Entries()
	last := entries[len(entries)-1]
	if !last.Blocked {
		t.Error("should be blocked with kill switch")
	}
	if last.Rule != "kill-switch" {
		t.Errorf("expected rule=kill-switch, got %q", last.Rule)
	}

	// Disable kill switch.
	tracker.SetKillSwitch(false)
	tracker.processEvent(connEvent{Proto: 6, DstIP: "7.7.7.7", DPort: 443, TS: time.Now().Unix()})
	entries = p.Log.Entries()
	last = entries[len(entries)-1]
	if last.Blocked {
		t.Error("should not be blocked after kill switch disabled")
	}
}

func TestNetTrackerFirewallRules(t *testing.T) {
	cfg := &Config{
		Mode: ModeEnforce,
		Rules: []Rule{
			{Pattern: "evil.com", Action: "deny"},
		},
	}
	p := NewProxy(cfg)
	tracker := NewNetTracker(p, "/nonexistent.sock")

	// evil.com won't match by IP (reverse DNS returns IP), so let's test
	// that the Check() integration works by using the raw IP.
	tracker.processEvent(connEvent{Proto: 6, DstIP: "93.184.216.34", DPort: 443, TS: time.Now().Unix()})
	entries := p.Log.Entries()
	// The IP won't match "evil.com" pattern, so it should not be blocked.
	if entries[0].Blocked {
		t.Error("raw IP should not match domain pattern")
	}
}

func TestNetTrackerPortInPath(t *testing.T) {
	cfg := &Config{Mode: ModeLogOnly, Rules: []Rule{}}
	p := NewProxy(cfg)
	tracker := NewNetTracker(p, "/nonexistent.sock")

	tracker.processEvent(connEvent{Proto: 6, DstIP: "1.1.1.1", DPort: 8443, TS: time.Now().Unix()})
	entries := p.Log.Entries()
	if entries[0].Path != ":8443" {
		t.Errorf("expected path=:8443, got %q", entries[0].Path)
	}
}

func TestNetTrackerSocketCommunication(t *testing.T) {
	// Test that the tracker can connect to a Unix socket and receive events.
	// Use /tmp for socket to avoid macOS 104-char limit on t.TempDir() paths.
	sockPath := filepath.Join(os.TempDir(), "alf-nettrack-test.sock")
	os.Remove(sockPath)
	t.Cleanup(func() { os.Remove(sockPath) })

	cfg := &Config{Mode: ModeLogOnly, Rules: []Rule{}}
	p := NewProxy(cfg)
	tracker := NewNetTracker(p, sockPath)

	// Start a fake helper server.
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	os.Chmod(sockPath, 0o666)

	// Send a test event from the "helper".
	done := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		enc := json.NewEncoder(conn)
		enc.Encode(connEvent{Proto: 6, DstIP: "8.8.8.8", DPort: 53, TS: time.Now().Unix()})
		enc.Encode(connEvent{Proto: 17, DstIP: "8.8.4.4", DPort: 53, TS: time.Now().Unix()})
		// Give tracker time to process.
		time.Sleep(100 * time.Millisecond)
		conn.Close()
		close(done)
	}()

	// Run tracker (will connect, read events, then fail on EOF).
	go tracker.connect(t.Context())

	<-done
	time.Sleep(50 * time.Millisecond)

	entries := p.Log.Entries()
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries from socket, got %d", len(entries))
	}
}

func TestNetTrackerDNSCache(t *testing.T) {
	cfg := &Config{Mode: ModeLogOnly, Rules: []Rule{}}
	p := NewProxy(cfg)
	tracker := NewNetTracker(p, "/nonexistent.sock")

	// Manually seed the DNS cache.
	tracker.mu.Lock()
	tracker.dnsCache["1.2.3.4"] = dnsCacheEntry{host: "cached.example.com", expires: time.Now().Add(5 * time.Minute)}
	tracker.mu.Unlock()

	host := tracker.resolveHost("1.2.3.4")
	if host != "cached.example.com" {
		t.Errorf("expected cached hostname, got %q", host)
	}
}
