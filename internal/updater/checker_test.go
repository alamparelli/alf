package updater

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestChecker creates a Checker pointing at a test server instead of GitHub.
func newTestChecker(t *testing.T, current string, srv *httptest.Server) *Checker {
	t.Helper()
	c := New("ghcr.io/test/repo", current, 0, nil)
	// Override client to route through test server. The checker builds
	// URLs like https://api.github.com/repos/test/repo/releases/latest;
	// we replace the transport to redirect all requests to our test server.
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

func TestLatestVersion_EmptyInitially(t *testing.T) {
	c := New("ghcr.io/test/repo", "v1.0.0", 0, nil)
	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion() = %q, want empty", got)
	}
}

func TestCheckOnce_NewerVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v2.0.0"})
	}))
	defer srv.Close()

	var notified string
	c := newTestChecker(t, "v1.0.0", srv)
	c.notify = func(cur, lat string) { notified = lat }

	c.CheckOnce()

	if got := c.LatestVersion(); got != "v2.0.0" {
		t.Errorf("LatestVersion() = %q, want %q", got, "v2.0.0")
	}
	if notified != "v2.0.0" {
		t.Errorf("notify called with %q, want %q", notified, "v2.0.0")
	}
}

func TestCheckOnce_UpToDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.0.0"})
	}))
	defer srv.Close()

	c := newTestChecker(t, "v1.0.0", srv)
	c.CheckOnce()

	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion() = %q, want empty (up-to-date)", got)
	}
}

func TestCheckOnce_OlderVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.9.0"})
	}))
	defer srv.Close()

	c := newTestChecker(t, "v1.0.0", srv)
	c.CheckOnce()

	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion() = %q, want empty (older)", got)
	}
}

func TestCheckOnce_NoReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := newTestChecker(t, "v1.0.0", srv)
	c.CheckOnce()

	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion() = %q, want empty (no releases)", got)
	}
}

func TestCheckOnce_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := newTestChecker(t, "v1.0.0", srv)
	c.CheckOnce()

	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion() = %q, want empty (server error)", got)
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
		image    string
		wantRepo string
	}{
		{"ghcr.io/owner/repo", "owner/repo"},
		{"owner/repo", "owner/repo"}, // no registry prefix
	}
	for _, tt := range tests {
		c := New(tt.image, "v1.0.0", 0, nil)
		if c.repo != tt.wantRepo {
			t.Errorf("New(%q).repo = %q, want %q", tt.image, c.repo, tt.wantRepo)
		}
	}
}
