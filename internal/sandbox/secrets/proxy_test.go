package secrets

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeVaultUpstream simulates vault-server for proxy tests.
// It records the last request and returns a fixed response.
func fakeVaultUpstream(t *testing.T) (socketPath string, cleanup func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "vp-test-*")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	sockPath := dir + "/v.sock"
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/", func(w http.ResponseWriter, r *http.Request) {
		// Echo back service name and auth header for verification.
		w.Header().Set("Content-Type", "application/json")
		auth := r.Header.Get("Authorization")
		w.Write([]byte(`{"path":"` + r.URL.Path + `","auth":"` + auth + `"}`))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"unlocked"}`))
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)

	return sockPath, func() {
		srv.Close()
		os.RemoveAll(dir)
	}
}

func TestVaultProxy_LLMTier_AllowsAnyService(t *testing.T) {
	upstream, cleanup := fakeVaultUpstream(t)
	defer cleanup()

	proxy := NewVaultProxy(upstream, "test-token", nil) // nil = LLM tier

	req := httptest.NewRequest("GET", "/proxy/openrouter/v1/models", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"path":"/proxy/openrouter/v1/models"`) {
		t.Fatalf("unexpected body: %s", body)
	}
	if !strings.Contains(body, `"auth":"Bearer test-token"`) {
		t.Fatalf("token not injected: %s", body)
	}
}

func TestVaultProxy_AppTier_AllowsDeclaredService(t *testing.T) {
	upstream, cleanup := fakeVaultUpstream(t)
	defer cleanup()

	proxy := NewVaultProxy(upstream, "test-token", []string{"openrouter"})

	req := httptest.NewRequest("GET", "/proxy/openrouter/v1/models", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVaultProxy_AppTier_BlocksUndeclaredService(t *testing.T) {
	upstream, cleanup := fakeVaultUpstream(t)
	defer cleanup()

	proxy := NewVaultProxy(upstream, "test-token", []string{"openrouter"})

	req := httptest.NewRequest("GET", "/proxy/github/repos", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVaultProxy_BlocksAdminEndpoints(t *testing.T) {
	upstream, cleanup := fakeVaultUpstream(t)
	defer cleanup()

	proxy := NewVaultProxy(upstream, "test-token", nil)

	// Admin/sensitive paths must be blocked.
	for _, path := range []string{"/tokens", "/auth/unlock", "/files", "/files/secret.json"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Errorf("path %s: expected 403, got %d", path, w.Code)
		}
	}
}

func TestVaultProxy_AllowsSSHAndHealth(t *testing.T) {
	upstream, cleanup := fakeVaultUpstream(t)
	defer cleanup()

	// Add /ssh/ and /services handlers to upstream.
	proxy := NewVaultProxy(upstream, "test-token", nil)

	for _, path := range []string{"/health", "/services", "/ssh/homelab/exec", "/proxy/openrouter/v1/models"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, req)
		if w.Code == 403 {
			t.Errorf("path %s: should not be blocked, got 403", path)
		}
	}
}

func TestVaultProxy_AppTier_BlocksSSHForUndeclaredService(t *testing.T) {
	upstream, cleanup := fakeVaultUpstream(t)
	defer cleanup()

	proxy := NewVaultProxy(upstream, "test-token", []string{"openrouter"})

	// SSH to undeclared service should be blocked.
	req := httptest.NewRequest("POST", "/ssh/homelab/exec", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Errorf("expected 403 for undeclared SSH service, got %d", w.Code)
	}
}

func TestVaultProxy_TokenInjection_NoLeakToClient(t *testing.T) {
	upstream, cleanup := fakeVaultUpstream(t)
	defer cleanup()

	proxy := NewVaultProxy(upstream, "secret-proxy-token", nil)

	// Client sends its own auth header — proxy should replace it.
	req := httptest.NewRequest("GET", "/proxy/openrouter/v1/models", nil)
	req.Header.Set("Authorization", "Bearer client-token-should-not-reach-vault")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"auth":"Bearer secret-proxy-token"`) {
		t.Fatalf("proxy token not injected correctly: %s", body)
	}
}

func TestVaultProxy_UpdateToken(t *testing.T) {
	upstream, cleanup := fakeVaultUpstream(t)
	defer cleanup()

	proxy := NewVaultProxy(upstream, "old-token", nil)
	proxy.UpdateToken("new-token")

	req := httptest.NewRequest("GET", "/proxy/test/path", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `"auth":"Bearer new-token"`) {
		t.Fatalf("token not updated: %s", body)
	}
}

func TestVaultProxy_ListenAndServe(t *testing.T) {
	upstream, cleanup := fakeVaultUpstream(t)

	dir, _ := os.MkdirTemp("", "vp-ln-*")
	sockPath := dir + "/p.sock"

	proxy := NewVaultProxy(upstream, "test-token", []string{"svc"})
	ln, err := proxy.ListenAndServe(sockPath)
	if err != nil {
		os.RemoveAll(dir)
		cleanup()
		t.Fatalf("ListenAndServe: %v", err)
	}
	t.Cleanup(func() {
		ln.Close()
		time.Sleep(10 * time.Millisecond) // let serve goroutine exit
		os.RemoveAll(dir)
		cleanup()
	})

	// Connect via Unix socket HTTP client.
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}

	resp, err := client.Get("http://localhost/proxy/svc/path")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestVaultProxy_MissingService(t *testing.T) {
	upstream, cleanup := fakeVaultUpstream(t)
	defer cleanup()

	proxy := NewVaultProxy(upstream, "test-token", nil)

	req := httptest.NewRequest("GET", "/proxy/", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
