package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

const nettrackCtrlSock = "/run/alf-nettrack-ctrl.sock"

// connEvent mirrors the JSON from nettrack-helper.
type connEvent struct {
	Proto uint8  `json:"proto"`
	SrcIP string `json:"src"`
	DstIP string `json:"dst"`
	DPort uint16 `json:"dport"`
	TS    int64  `json:"ts"`
}

type connKey struct {
	DstIP string
	DPort uint16
	Proto uint8
}

// NetTracker receives conntrack events from the nettrack-helper process
// and feeds them into the firewall's RingBuffer and Store.
type NetTracker struct {
	proxy    *Proxy
	sockPath string

	mu       sync.Mutex
	seen     map[connKey]time.Time // dedup: skip if seen within window
	dnsCache map[string]dnsCacheEntry

	// Kill switch: when true, log and mark as "blocked".
	// ctrlConn forwards the command to nettrack-helper for actual iptables enforcement.
	killSwitch bool
	killMu     sync.RWMutex
	ctrlConn   net.Conn
	ctrlMu     sync.Mutex
}

type dnsCacheEntry struct {
	host    string
	expires time.Time
}

const (
	dedupeWindow  = 30 * time.Second
	dnsCacheTTL   = 5 * time.Minute
	cleanInterval = 60 * time.Second
	reconnectWait = 3 * time.Second
)

// NewNetTracker creates a tracker that connects to the nettrack-helper socket.
func NewNetTracker(proxy *Proxy, sockPath string) *NetTracker {
	return &NetTracker{
		proxy:    proxy,
		sockPath: sockPath,
		seen:     make(map[connKey]time.Time),
		dnsCache: make(map[string]dnsCacheEntry),
	}
}

// connectCtrl attempts to connect to the nettrack-helper control socket for iptables enforcement.
// Falls back gracefully (log-only) if the socket is unavailable.
func (t *NetTracker) connectCtrl() {
	t.ctrlMu.Lock()
	defer t.ctrlMu.Unlock()
	conn, err := net.DialTimeout("unix", nettrackCtrlSock, 2*time.Second)
	if err != nil {
		log.Printf("[nettrack] control socket unavailable — kill switch will be log-only: %v", err)
		return
	}
	t.ctrlConn = conn
	log.Println("[nettrack] connected to control socket (iptables enforcement enabled)")
}

// sendCtrl sends a kill switch command to the nettrack-helper.
func (t *NetTracker) sendCtrl(on bool) {
	t.ctrlMu.Lock()
	defer t.ctrlMu.Unlock()
	if t.ctrlConn == nil {
		return
	}
	msg, _ := json.Marshal(map[string]bool{"kill_switch": on})
	msg = append(msg, '\n')
	if _, err := t.ctrlConn.Write(msg); err != nil {
		log.Printf("[nettrack] control send error: %v — kill switch is log-only", err)
		t.ctrlConn.Close()
		t.ctrlConn = nil
	}
}

// Run connects to the helper socket and processes events until ctx is cancelled.
// Reconnects automatically if the connection drops.
func (t *NetTracker) Run(ctx context.Context) {
	log.Printf("[nettrack] starting (socket: %s)", t.sockPath)

	// Connect to the control socket for iptables enforcement.
	t.connectCtrl()

	// Periodic cleanup of dedup map and DNS cache.
	go t.cleanupLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := t.connect(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[nettrack] connection error: %v, retrying in %v", err, reconnectWait)
			select {
			case <-ctx.Done():
				return
			case <-time.After(reconnectWait):
			}
		}
	}
}

// SetKillSwitch enables or disables the network kill switch.
// Sends the command to nettrack-helper for actual iptables enforcement;
// also updates local state so in-flight connections are logged as blocked.
func (t *NetTracker) SetKillSwitch(on bool) {
	t.killMu.Lock()
	t.killSwitch = on
	t.killMu.Unlock()
	// Forward to nettrack-helper for real iptables enforcement.
	t.sendCtrl(on)
	if on {
		log.Println("[nettrack] KILL SWITCH ENABLED")
	} else {
		log.Println("[nettrack] kill switch disabled")
	}
}

// KillSwitchActive returns whether the kill switch is currently on.
func (t *NetTracker) KillSwitchActive() bool {
	t.killMu.RLock()
	defer t.killMu.RUnlock()
	return t.killSwitch
}

func (t *NetTracker) connect(ctx context.Context) error {
	conn, err := net.DialTimeout("unix", t.sockPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", t.sockPath, err)
	}
	defer conn.Close()
	log.Println("[nettrack] connected to helper")

	dec := json.NewDecoder(conn)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var ev connEvent
		if err := dec.Decode(&ev); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
		t.processEvent(ev)
	}
}

func (t *NetTracker) processEvent(ev connEvent) {
	// Detect private/internal IPs (Docker network services like whisper, embed).
	ip := net.ParseIP(ev.DstIP)
	isInternal := ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast())

	// Infrastructure ports are always internal.
	switch ev.DPort {
	case 4751, 8080, 8390:
		isInternal = true
	}

	key := connKey{DstIP: ev.DstIP, DPort: ev.DPort, Proto: ev.Proto}

	// Dedup.
	t.mu.Lock()
	if last, ok := t.seen[key]; ok && time.Since(last) < dedupeWindow {
		t.mu.Unlock()
		return
	}
	t.seen[key] = time.Now()
	t.mu.Unlock()

	// Skip if the HTTP proxy already logged this host recently (within 5s).
	host := t.resolveHost(ev.DstIP)
	if ev.DPort == 443 || ev.DPort == 80 {
		entries := t.proxy.Log.Entries()
		now := time.Now()
		for i := len(entries) - 1; i >= 0 && i >= len(entries)-20; i-- {
			e := entries[i]
			if e.Source == "nettrack" {
				continue
			}
			if e.Host == host && now.Sub(e.Time) < 5*time.Second {
				return // already captured by HTTP proxy
			}
		}
	}

	// Protocol label.
	method := "TCP"
	if ev.Proto == 17 {
		method = "UDP"
	}

	// Check kill switch.
	t.killMu.RLock()
	killed := t.killSwitch
	t.killMu.RUnlock()

	// Check firewall rules.
	rule, _, blocked := t.proxy.Check(host)
	if killed {
		blocked = true
		rule = "kill-switch"
	}

	source := "nettrack"
	if isInternal {
		source = "internal"
	}

	entry := RequestEntry{
		Time:    time.Now(),
		Method:  method,
		Host:    host,
		Path:    fmt.Sprintf(":%d", ev.DPort),
		Status:  0,
		Blocked: blocked,
		Rule:    rule,
		Source:  source,
	}

	t.proxy.Record(entry)
}

func (t *NetTracker) resolveHost(ip string) string {
	t.mu.Lock()
	if cached, ok := t.dnsCache[ip]; ok && time.Now().Before(cached.expires) {
		t.mu.Unlock()
		return cached.host
	}
	t.mu.Unlock()

	// Reverse DNS lookup (non-blocking timeout via the OS resolver).
	names, err := net.LookupAddr(ip)
	host := ip
	if err == nil && len(names) > 0 {
		// Remove trailing dot.
		h := names[0]
		if len(h) > 0 && h[len(h)-1] == '.' {
			h = h[:len(h)-1]
		}
		host = h
	}

	t.mu.Lock()
	t.dnsCache[ip] = dnsCacheEntry{host: host, expires: time.Now().Add(dnsCacheTTL)}
	t.mu.Unlock()

	return host
}

func (t *NetTracker) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(cleanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			t.mu.Lock()
			for k, ts := range t.seen {
				if now.Sub(ts) > dedupeWindow {
					delete(t.seen, k)
				}
			}
			for ip, cached := range t.dnsCache {
				if now.After(cached.expires) {
					delete(t.dnsCache, ip)
				}
			}
			t.mu.Unlock()
		}
	}
}
