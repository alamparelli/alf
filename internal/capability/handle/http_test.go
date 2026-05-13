package handle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

func TestHTTPScope_AllowedExactHost(t *testing.T) {
	s := HTTPScope{Patterns: []HTTPPattern{{Host: "api.example.com"}}}
	if !s.Allows(mustParse(t, "https://api.example.com/x"), "GET") {
		t.Error("exact host match rejected")
	}
	if s.Allows(mustParse(t, "https://evil.example.com/x"), "GET") {
		t.Error("non-matching host accepted")
	}
}

// TestHTTPScope_NoSubdomainImplied pins #421's "no wildcards" rule
// at the handle layer: pattern "example.com" matches ONLY
// "example.com", not "api.example.com". Subdomains require an
// explicit pattern per host.
func TestHTTPScope_NoSubdomainImplied(t *testing.T) {
	s := HTTPScope{Patterns: []HTTPPattern{{Host: "example.com"}}}
	for _, u := range []string{
		"https://api.example.com/x",
		"https://deep.sub.example.com/x",
		"https://sneaky-example.com/x",
	} {
		if s.Allows(mustParse(t, u), "GET") {
			t.Errorf("%s: unexpected match — patterns are exact, not subdomain", u)
		}
	}
	if !s.Allows(mustParse(t, "https://example.com/x"), "GET") {
		t.Error("exact host should match")
	}
}

// TestHTTPScope_HostWithPort pins the port-custom decision: a
// pattern that carries ":port" matches only requests whose URL also
// carries the explicit port; a pattern without a port matches only
// the default-port form.
func TestHTTPScope_HostWithPort(t *testing.T) {
	s := HTTPScope{Patterns: []HTTPPattern{{Host: "homelab.local:8443"}}}
	if !s.Allows(mustParse(t, "https://homelab.local:8443/"), "GET") {
		t.Error("matching port rejected")
	}
	if s.Allows(mustParse(t, "https://homelab.local/"), "GET") {
		t.Error("port-less request matched a ported pattern")
	}
	if s.Allows(mustParse(t, "https://homelab.local:1234/"), "GET") {
		t.Error("different port matched")
	}
}

// TestHTTPScope_PathPrefixMatching pins the segment-aware matching:
// "/books/v1" matches "/books/v1" and "/books/v1/X" but NOT
// "/books/v10". Empty PathPrefix means any path.
func TestHTTPScope_PathPrefixMatching(t *testing.T) {
	s := HTTPScope{Patterns: []HTTPPattern{{Host: "api.example.com", PathPrefix: "/books/v1"}}}
	cases := []struct {
		url  string
		want bool
	}{
		{"https://api.example.com/books/v1", true},
		{"https://api.example.com/books/v1/", true},
		{"https://api.example.com/books/v1/12345", true},
		{"https://api.example.com/books/v10", false}, // prefix-collision footgun
		{"https://api.example.com/books/v1x", false},
		{"https://api.example.com/", false},
		{"https://api.example.com/admin", false},
	}
	for _, tc := range cases {
		got := s.Allows(mustParse(t, tc.url), "GET")
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.url, got, tc.want)
		}
	}
}

// TestHTTPScope_RequireHTTPSRejectsHTTP pins the HTTPS-only decision:
// when the scope is built with RequireHTTPS=true (the only mode the
// #421 forge uses), an http:// request is refused even if the host
// pattern would otherwise match.
func TestHTTPScope_RequireHTTPSRejectsHTTP(t *testing.T) {
	s := HTTPScope{
		Patterns:     []HTTPPattern{{Host: "api.example.com"}},
		RequireHTTPS: true,
	}
	if s.Allows(mustParse(t, "http://api.example.com/x"), "GET") {
		t.Error("plain HTTP must be rejected when RequireHTTPS=true")
	}
	if !s.Allows(mustParse(t, "https://api.example.com/x"), "GET") {
		t.Error("HTTPS request rejected unexpectedly")
	}
}

// TestHTTPScope_RequireHTTPSFalseAllowsHTTP pins the test-fixture
// path: when RequireHTTPS=false (e.g. for httptest servers that
// only speak plain HTTP), http:// is permitted.
func TestHTTPScope_RequireHTTPSFalseAllowsHTTP(t *testing.T) {
	s := HTTPScope{Patterns: []HTTPPattern{{Host: "api.example.com"}}}
	if !s.Allows(mustParse(t, "http://api.example.com/x"), "GET") {
		t.Error("RequireHTTPS=false should allow http://")
	}
}

func TestHTTPScope_MethodRestriction(t *testing.T) {
	s := HTTPScope{
		Patterns: []HTTPPattern{{Host: "api.example.com"}},
		Methods:  []string{"GET", "HEAD"},
	}
	if !s.Allows(mustParse(t, "https://api.example.com"), "GET") {
		t.Error("GET rejected")
	}
	if !s.Allows(mustParse(t, "https://api.example.com"), "get") {
		t.Error("method comparison must be case-insensitive")
	}
	if s.Allows(mustParse(t, "https://api.example.com"), "POST") {
		t.Error("POST should be rejected")
	}
}

func TestHTTPScope_EmptyMethodsMeansAny(t *testing.T) {
	s := HTTPScope{Patterns: []HTTPPattern{{Host: "api.example.com"}}}
	for _, m := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
		if !s.Allows(mustParse(t, "https://api.example.com"), m) {
			t.Errorf("empty Methods should allow %s", m)
		}
	}
}

func TestHTTPScope_DenyNilAndEmpty(t *testing.T) {
	s := HTTPScope{Patterns: []HTTPPattern{{Host: "api.example.com"}}}
	if s.Allows(nil, "GET") {
		t.Error("nil URL must be denied")
	}
	if s.Allows(&url.URL{}, "GET") {
		t.Error("empty-host URL must be denied")
	}
	empty := HTTPScope{}
	if empty.Allows(mustParse(t, "https://api.example.com"), "GET") {
		t.Error("empty Patterns must be denied")
	}
}

func TestHTTPHandle_DoInScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	u := mustParse(t, srv.URL)
	h := NewHTTPHandle("cap", HTTPScope{Patterns: []HTTPPattern{{Host: u.Host}}}, srv.Client())
	inst := NewInstance(context.Background(), "cap", Grants{HTTP: h})
	defer inst.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	resp, err := inst.HTTP.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body=%q, want ok", body)
	}
}

func TestHTTPHandle_OutOfScope(t *testing.T) {
	h := NewHTTPHandle("cap", HTTPScope{Patterns: []HTTPPattern{{Host: "allowed.example.com"}}}, nil)
	inst := NewInstance(context.Background(), "cap", Grants{HTTP: h})
	defer inst.Close()

	req, _ := http.NewRequest("GET", "https://denied.example.com/x", nil)
	_, err := inst.HTTP.Do(context.Background(), req)
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("want ErrOutOfScope, got %v", err)
	}
}

func TestHTTPHandle_MethodOutOfScope(t *testing.T) {
	h := NewHTTPHandle("cap", HTTPScope{
		Patterns: []HTTPPattern{{Host: "api.example.com"}},
		Methods:  []string{"GET"},
	}, nil)
	inst := NewInstance(context.Background(), "cap", Grants{HTTP: h})
	defer inst.Close()

	req, _ := http.NewRequest("POST", "https://api.example.com/x", strings.NewReader(""))
	_, err := inst.HTTP.Do(context.Background(), req)
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("want ErrOutOfScope, got %v", err)
	}
}

func TestHTTPHandle_Revocation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	u := mustParse(t, srv.URL)
	h := NewHTTPHandle("cap", HTTPScope{Patterns: []HTTPPattern{{Host: u.Host}}}, srv.Client())
	inst := NewInstance(context.Background(), "cap", Grants{HTTP: h})

	start := time.Now()
	inst.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err := inst.HTTP.Do(context.Background(), req)
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("revocation took %v, want <100ms", elapsed)
	}
}

func TestHTTPHandle_LifecycleCancelsInFlight(t *testing.T) {
	// Server stalls indefinitely until the request context is cancelled.
	requestStarted := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	}))
	defer srv.Close()

	u := mustParse(t, srv.URL)
	h := NewHTTPHandle("cap", HTTPScope{Patterns: []HTTPPattern{{Host: u.Host}}}, srv.Client())
	inst := NewInstance(context.Background(), "cap", Grants{HTTP: h})

	var doErr atomic.Value
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, _ := http.NewRequest("GET", srv.URL, nil)
		_, err := inst.HTTP.Do(context.Background(), req)
		if err != nil {
			doErr.Store(err)
		}
	}()

	<-requestStarted
	inst.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request was not cancelled within 2s")
	}

	if v := doErr.Load(); v == nil {
		t.Fatal("expected an error from the cancelled request, got nil")
	}
}

func TestHTTPHandle_NonSerializable(t *testing.T) {
	h := NewHTTPHandle("cap", HTTPScope{}, nil)
	if _, err := json.Marshal(h); err == nil {
		t.Fatal("HTTPHandle must not be JSON-serializable")
	}
}

func TestHTTPHandle_Owner(t *testing.T) {
	h := NewHTTPHandle("cap-xyz", HTTPScope{}, nil)
	if got := h.Owner(); string(got) != "cap-xyz" {
		t.Errorf("Owner()=%q, want cap-xyz", got)
	}
}
