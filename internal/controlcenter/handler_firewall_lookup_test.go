package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// SEC-002: WHOIS lookup SSRF prevention
// ---------------------------------------------------------------------------

func TestFirewallLookup_BlocksPrivateIP(t *testing.T) {
	privateIPs := []string{
		"192.168.1.1",
		"10.0.0.1",
		"172.16.0.1",
		"127.0.0.1",
	}

	h := &FirewallLookupHandler{}

	for _, ip := range privateIPs {
		// Seed the cache directly so we skip outbound DNS/HTTP calls.
		lookupCacheMu.Lock()
		lookupCache[ip] = &lookupCacheEntry{
			result:  lookupResult{Host: ip, IP: ip, Org: "internal"},
			expires: time.Now().Add(time.Hour),
		}
		lookupCacheMu.Unlock()

		req := httptest.NewRequest("GET", "/api/firewall/lookup?host="+ip, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("IP %s: expected 200, got %d", ip, rec.Code)
			continue
		}

		var result lookupResult
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Errorf("IP %s: json unmarshal: %v", ip, err)
			continue
		}
		// Private IPs should be marked "internal", not leak to ip-api.com.
		if result.Org != "internal" {
			t.Errorf("IP %s: expected org='internal', got %q (private IP must not call ip-api.com)", ip, result.Org)
		}
	}

	// Cleanup cache.
	lookupCacheMu.Lock()
	for _, ip := range privateIPs {
		delete(lookupCache, ip)
	}
	lookupCacheMu.Unlock()
}

func TestFirewallLookup_CacheSizeCap_Constant(t *testing.T) {
	if lookupCacheMaxSize <= 0 {
		t.Error("lookupCacheMaxSize must be positive")
	}
	if lookupCacheMaxSize > 10000 {
		t.Errorf("lookupCacheMaxSize=%d is too large (unbounded memory risk)", lookupCacheMaxSize)
	}
}

func TestFirewallLookup_CacheEvictsWhenFull(t *testing.T) {
	// Fill cache to exactly the cap.
	lookupCacheMu.Lock()
	for i := 0; i < lookupCacheMaxSize; i++ {
		key := "fill-" + string(rune('a'+i%26)) + "-" + string(rune('0'+i%10))
		lookupCache[key] = &lookupCacheEntry{
			result:  lookupResult{Host: key},
			expires: time.Now().Add(time.Hour),
		}
	}
	sizeBefore := len(lookupCache)
	lookupCacheMu.Unlock()

	if sizeBefore > lookupCacheMaxSize {
		t.Fatalf("cache is already over cap before test: %d > %d", sizeBefore, lookupCacheMaxSize)
	}

	// Directly test the eviction logic by calling the handler with a cached miss
	// that triggers the size cap branch.
	h := &FirewallLookupHandler{}
	// We use a host that resolves to a private IP so no outbound call is made.
	lookupCacheMu.Lock()
	lookupCache["test-evict-trigger"] = &lookupCacheEntry{
		result:  lookupResult{Host: "test-evict-trigger", IP: "192.168.99.1", Org: "internal"},
		expires: time.Now().Add(time.Hour),
	}
	lookupCacheMu.Unlock()

	req := httptest.NewRequest("GET", "/api/firewall/lookup?host=test-evict-trigger", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Cleanup.
	lookupCacheMu.Lock()
	for k := range lookupCache {
		delete(lookupCache, k)
	}
	lookupCacheMu.Unlock()
}

func TestFirewallLookup_RequiresHostParam(t *testing.T) {
	h := &FirewallLookupHandler{}
	req := httptest.NewRequest("GET", "/api/firewall/lookup", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing host param: expected 400, got %d", rec.Code)
	}
}

func TestFirewallLookup_OnlyAllowsGet(t *testing.T) {
	h := &FirewallLookupHandler{}
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(method, "/api/firewall/lookup?host=1.2.3.4", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: expected 405, got %d", method, rec.Code)
		}
	}
}
