package controlcenter

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// FirewallLookupHandler returns whois-like info for a host or IP.
// GET /api/firewall/lookup?host=1.2.3.4
type FirewallLookupHandler struct{}

type lookupResult struct {
	Host    string `json:"host"`
	IP      string `json:"ip,omitempty"`
	Org     string `json:"org,omitempty"`
	ISP     string `json:"isp,omitempty"`
	Country string `json:"country,omitempty"`
	City    string `json:"city,omitempty"`
	AS      string `json:"as,omitempty"`
	Reverse string `json:"reverse,omitempty"`
}

// Simple cache to avoid hammering the API.
// Capped at 1000 entries to prevent unbounded memory growth.
const lookupCacheMaxSize = 1000

var (
	lookupCache   = make(map[string]*lookupCacheEntry, lookupCacheMaxSize)
	lookupCacheMu sync.Mutex
)

type lookupCacheEntry struct {
	result  lookupResult
	expires time.Time
}

func (h *FirewallLookupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	host := r.URL.Query().Get("host")
	if host == "" {
		respondError(w, http.StatusBadRequest, "host parameter required")
		return
	}

	// Check cache.
	lookupCacheMu.Lock()
	if entry, ok := lookupCache[host]; ok && time.Now().Before(entry.expires) {
		lookupCacheMu.Unlock()
		respondJSON(w, http.StatusOK, entry.result)
		return
	}
	lookupCacheMu.Unlock()

	// Resolve hostname to IP if needed.
	ip := host
	var reverse string
	if net.ParseIP(host) == nil {
		// It's a hostname — resolve to IP.
		ips, err := net.LookupHost(host)
		if err == nil && len(ips) > 0 {
			ip = ips[0]
		}
	} else {
		// It's an IP — do reverse DNS.
		names, err := net.LookupAddr(host)
		if err == nil && len(names) > 0 {
			reverse = strings.TrimSuffix(names[0], ".")
		}
	}

	// Fetch from ip-api.com (free, no auth, 45 req/min).
	// Skip for private/loopback/link-local IPs to prevent SSRF.
	result := lookupResult{Host: host, IP: ip, Reverse: reverse}
	if parsed := net.ParseIP(ip); parsed != nil {
		if parsed.IsPrivate() || parsed.IsLoopback() || parsed.IsLinkLocalUnicast() {
			result.Org = "internal"
		} else if info, err := fetchIPInfo(ip); err == nil {
			result.Org = info.Org
			result.ISP = info.ISP
			result.Country = info.Country
			result.City = info.City
			result.AS = info.AS
		}
	}

	// Cache for 1 hour (evict oldest entry if at capacity).
	lookupCacheMu.Lock()
	if len(lookupCache) >= lookupCacheMaxSize {
		// Remove an arbitrary entry to make room.
		for k := range lookupCache {
			delete(lookupCache, k)
			break
		}
	}
	lookupCache[host] = &lookupCacheEntry{result: result, expires: time.Now().Add(time.Hour)}
	lookupCacheMu.Unlock()

	respondJSON(w, http.StatusOK, result)
}

type ipAPIResponse struct {
	Org     string `json:"org"`
	ISP     string `json:"isp"`
	Country string `json:"country"`
	City    string `json:"city"`
	AS      string `json:"as"`
}

func fetchIPInfo(ip string) (*ipAPIResponse, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://ip-api.com/json/%s?fields=org,isp,country,city,as", ip))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ip-api returned %d", resp.StatusCode)
	}

	var info ipAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}
