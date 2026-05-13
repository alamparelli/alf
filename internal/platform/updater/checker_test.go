package updater

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestChecker creates a Checker pointing at a test server instead of GHCR.
//
// SEC-080-004: production wiring guarantees a verifier is always set
// (real or permissive). Tests that don't exercise the verifier
// branch mirror this invariant via PermissiveCosignVerifier so they
// reach the notify path. Tests that DO exercise the verifier (e.g.
// TestCheckOnce_CosignVerifyFailureBlocksNotify) override via
// SetCosignVerifier after construction.
func newTestChecker(t *testing.T, current string, srv *httptest.Server) *Checker {
	t.Helper()
	c := New("ghcr.io/test/repo", current, 0, nil)
	c.SetCosignVerifier(PermissiveCosignVerifier())
	// Override registry to route through test server.
	c.client = srv.Client()
	c.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = srv.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(req)
	})
	return c
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// registryHandler serves the token, tags, and manifest endpoints
// the Checker walks. The manifest HEAD response carries a fixed
// digest so tests can assert on it; switching to a per-tag digest
// is overkill for the current coverage.
func registryHandler(tags []string) http.HandlerFunc {
	return registryHandlerWithDigest(tags, "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
}

func registryHandlerWithDigest(tags []string, digest string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/token"):
			json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
		case strings.Contains(r.URL.Path, "/tags/list"):
			json.NewEncoder(w).Encode(map[string]interface{}{"name": "test/repo", "tags": tags})
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Docker-Content-Digest", digest)
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}
}

func TestLatestVersion_EmptyInitially(t *testing.T) {
	c := New("ghcr.io/test/repo", "1.0.0", 0, nil)
	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion() = %q, want empty", got)
	}
}

func TestCheckOnce_NewerVersion(t *testing.T) {
	srv := httptest.NewServer(registryHandler([]string{"1.0.0", "2.0.0", "latest", "abc123"}))
	defer srv.Close()

	var notified string
	c := newTestChecker(t, "1.0.0", srv)
	c.notify = func(cur, lat, dig string) { notified = lat }

	c.CheckOnce()

	if got := c.LatestVersion(); got != "2.0.0" {
		t.Errorf("LatestVersion() = %q, want %q", got, "2.0.0")
	}
	if notified != "2.0.0" {
		t.Errorf("notify called with %q, want %q", notified, "2.0.0")
	}
}

func TestCheckOnce_UpToDate(t *testing.T) {
	srv := httptest.NewServer(registryHandler([]string{"1.0.0", "latest"}))
	defer srv.Close()

	c := newTestChecker(t, "1.0.0", srv)
	c.CheckOnce()

	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion() = %q, want empty (up-to-date)", got)
	}
}

func TestCheckOnce_OlderVersion(t *testing.T) {
	srv := httptest.NewServer(registryHandler([]string{"0.9.0", "latest"}))
	defer srv.Close()

	c := newTestChecker(t, "1.0.0", srv)
	c.CheckOnce()

	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion() = %q, want empty (older)", got)
	}
}

func TestCheckOnce_NoTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/token") {
			json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := newTestChecker(t, "1.0.0", srv)
	c.CheckOnce()

	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion() = %q, want empty (no tags)", got)
	}
}

func TestCheckOnce_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/token") {
			json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
			return
		}
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := newTestChecker(t, "1.0.0", srv)
	c.CheckOnce()

	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion() = %q, want empty (server error)", got)
	}
}

func TestCheckOnce_SkipsNonSemverTags(t *testing.T) {
	srv := httptest.NewServer(registryHandler([]string{"latest", "main", "abc123", "v1.0.0", "1.0.0"}))
	defer srv.Close()

	c := newTestChecker(t, "0.9.0", srv)
	c.CheckOnce()

	if got := c.LatestVersion(); got != "1.0.0" {
		t.Errorf("LatestVersion() = %q, want %q (should skip non-semver)", got, "1.0.0")
	}
}

func TestCheckOnce_PicksHighestSemver(t *testing.T) {
	srv := httptest.NewServer(registryHandler([]string{"0.5.0", "0.6.14", "0.6.15", "0.6.2", "latest"}))
	defer srv.Close()

	c := newTestChecker(t, "0.6.14", srv)
	c.CheckOnce()

	if got := c.LatestVersion(); got != "0.6.15" {
		t.Errorf("LatestVersion() = %q, want %q", got, "0.6.15")
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"2.0.0", "1.0.0", 1},
		{"1.0.0", "2.0.0", -1},
		{"1.1.0", "1.0.0", 1},
		{"1.0.1", "1.0.0", 1},
		{"0.6.15", "0.6.14", 1},
	}
	for _, tt := range tests {
		got := compareSemver(tt.a, tt.b)
		if (tt.want > 0 && got <= 0) || (tt.want < 0 && got >= 0) || (tt.want == 0 && got != 0) {
			t.Errorf("compareSemver(%q, %q) = %d, want sign %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNew_RepoExtraction(t *testing.T) {
	tests := []struct {
		image        string
		wantRepo     string
		wantRegistry string
	}{
		{"ghcr.io/owner/repo", "owner/repo", "ghcr.io"},
		{"owner/repo", "owner/repo", "ghcr.io"}, // no registry prefix: keeps default
	}
	for _, tt := range tests {
		c := New(tt.image, "1.0.0", 0, nil)
		if c.repo != tt.wantRepo {
			t.Errorf("New(%q).repo = %q, want %q", tt.image, c.repo, tt.wantRepo)
		}
		if c.registry != tt.wantRegistry {
			t.Errorf("New(%q).registry = %q, want %q", tt.image, c.registry, tt.wantRegistry)
		}
	}
}

func TestNew_StripVPrefix(t *testing.T) {
	c := New("ghcr.io/test/repo", "v1.2.3", 0, nil)
	if c.current != "1.2.3" {
		t.Errorf("current = %q, want %q (v prefix should be stripped)", c.current, "1.2.3")
	}
}

func TestCheckOnce_Pagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/token"):
			json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
		case strings.Contains(r.URL.Path, "/tags/list"):
			if r.URL.Query().Get("last") == "page1" {
				// Page 2: has the latest version
				json.NewEncoder(w).Encode(map[string]interface{}{
					"name": "test/repo",
					"tags": []string{"2.0.0", "latest"},
				})
			} else {
				// Page 1: old versions only, with Link to page 2
				w.Header().Set("Link", `</v2/test/repo/tags/list?last=page1&n=0>; rel="next"`)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"name": "test/repo",
					"tags": []string{"0.9.0", "1.0.0", "abc123"},
				})
			}
		case strings.Contains(r.URL.Path, "/manifests/"):
			w.Header().Set("Docker-Content-Digest", "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := newTestChecker(t, "1.0.0", srv)
	c.CheckOnce()

	if got := c.LatestVersion(); got != "2.0.0" {
		t.Errorf("LatestVersion() = %q, want %q (should follow pagination)", got, "2.0.0")
	}
}

func TestParseNextLink(t *testing.T) {
	tests := []struct {
		header   string
		registry string
		want     string
	}{
		{`</v2/owner/repo/tags/list?last=abc&n=0>; rel="next"`, "ghcr.io", "https://ghcr.io/v2/owner/repo/tags/list?last=abc&n=0"},
		{``, "ghcr.io", ""},
		{`</v2/x/tags/list?last=z>; rel="prev"`, "ghcr.io", ""},
	}
	for _, tt := range tests {
		got := parseNextLink(tt.header, tt.registry)
		if got != tt.want {
			t.Errorf("parseNextLink(%q) = %q, want %q", tt.header, got, tt.want)
		}
	}
}
