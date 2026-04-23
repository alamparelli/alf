package secrets

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Tests for the vault Manager's public API against a fake vault-server.
// Subprocess lifecycle (Start/Stop/Reset/watchdog) is covered by E2E tests —
// these tests focus on the wire protocol + in-memory state transitions.

// vaultStub holds the state a fake vault-server needs to mimic.
type vaultStub struct {
	mu            sync.Mutex
	status        string // "locked" | "unlocked"
	validPassword string
	adminTokens   map[string]bool
	proxyTokens   map[string]bool
	files         map[string][]byte

	// forceUnauthorized[endpoint] = true makes that endpoint return 401 once.
	forceUnauthorized map[string]int
}

func newVaultStub(password string) *vaultStub {
	return &vaultStub{
		status:            "locked",
		validPassword:     password,
		adminTokens:       map[string]bool{},
		proxyTokens:       map[string]bool{},
		files:             map[string][]byte{},
		forceUnauthorized: map[string]int{},
	}
}

func (s *vaultStub) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"status": s.status})
	})

	mux.HandleFunc("/auth/unlock", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if body.Password != s.validPassword {
			http.Error(w, "invalid password", http.StatusUnauthorized)
			return
		}
		s.status = "unlocked"
		tok := "admin-" + body.Password
		s.adminTokens[tok] = true
		json.NewEncoder(w).Encode(map[string]string{"id": tok})
	})

	mux.HandleFunc("/tokens", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if n := s.forceUnauthorized["/tokens"]; n > 0 {
			s.forceUnauthorized["/tokens"] = n - 1
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !s.adminTokens[auth] {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodGet:
			// ListTokens — return an empty list; EnsureAuth only cares about 2xx.
			w.Write([]byte(`[]`))
		case http.MethodPost:
			var body struct {
				Scope string `json:"scope"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			tok := body.Scope + "-token"
			if body.Scope == "proxy" {
				s.proxyTokens[tok] = true
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"id": tok})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !s.adminTokens[auth] {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		name := r.FormValue("name")
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.files[name] = data
	})

	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !s.adminTokens[auth] {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/files/")
		data, ok := s.files[name]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Write(data)
	})

	return mux
}

func (s *vaultStub) hasProxyToken(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proxyTokens[tok]
}

// ----- Pure helpers (no subprocess, no fake server) ----------------------

func TestManager_SocketPath_DerivedFromDataDir(t *testing.T) {
	m := NewManager("/opt/alf/vault-data")
	if got, want := m.SocketPath(), "/opt/alf/vault-data/vault.sock"; got != want {
		t.Fatalf("SocketPath = %q, want %q", got, want)
	}
}

func TestManager_PasswordFile_DerivedFromDataDir(t *testing.T) {
	m := NewManager("/opt/alf/vault-data")
	if got, want := m.PasswordFile(), "/opt/alf/vault-data/.master-password"; got != want {
		t.Fatalf("PasswordFile = %q, want %q", got, want)
	}
}

func TestManager_IsFirstTime(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	if !m.IsFirstTime() {
		t.Fatal("expected IsFirstTime true on empty dir")
	}

	if err := os.WriteFile(filepath.Join(dir, "vault.enc"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed vault.enc: %v", err)
	}

	if m.IsFirstTime() {
		t.Fatal("expected IsFirstTime false once vault.enc exists")
	}
}

func TestManager_ReadPasswordFile(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	if got := m.readPasswordFile(); got != "" {
		t.Fatalf("empty dir should return empty password, got %q", got)
	}

	if err := os.WriteFile(m.PasswordFile(), []byte("  hunter2\n"), 0o600); err != nil {
		t.Fatalf("write password: %v", err)
	}

	if got := m.readPasswordFile(); got != "hunter2" {
		t.Fatalf("readPasswordFile = %q, want trimmed %q", got, "hunter2")
	}
}

func TestManager_SetHTTPProxy_StoredForSpawn(t *testing.T) {
	m := NewManager(t.TempDir())
	m.SetHTTPProxy("http://proxy:3128")
	if m.httpProxyURL != "http://proxy:3128" {
		t.Fatalf("httpProxyURL = %q, want %q", m.httpProxyURL, "http://proxy:3128")
	}
}

// ----- Token state transitions (fake vault-server) -----------------------

func TestManager_AutoUnlock_StoresAdminToken(t *testing.T) {
	stub := newVaultStub("right-password")
	m, cleanup := NewTestManager(t, stub.handler(), "")
	defer cleanup()

	if err := m.AutoUnlock("right-password"); err != nil {
		t.Fatalf("AutoUnlock: %v", err)
	}
	if got, want := m.AdminToken(), "admin-right-password"; got != want {
		t.Fatalf("AdminToken = %q, want %q", got, want)
	}
}

func TestManager_AutoUnlock_WrongPassword_PreservesToken(t *testing.T) {
	stub := newVaultStub("right-password")
	m, cleanup := NewTestManager(t, stub.handler(), "prior-token")
	defer cleanup()

	err := m.AutoUnlock("wrong")
	if err == nil {
		t.Fatal("expected error from wrong password")
	}
	if !strings.Contains(err.Error(), "unlock") {
		t.Fatalf("error should mention unlock context: %v", err)
	}
	// Prior token must not be wiped by a failed unlock.
	if got := m.AdminToken(); got != "prior-token" {
		t.Fatalf("AdminToken wiped on failure: got %q", got)
	}
}

func TestManager_CreateProxyToken_StoresAndReturns(t *testing.T) {
	stub := newVaultStub("pw")
	m, cleanup := NewTestManager(t, stub.handler(), "admin-pw")
	defer cleanup()

	// Admin token must be valid on the stub side, so seed it.
	stub.mu.Lock()
	stub.adminTokens["admin-pw"] = true
	stub.mu.Unlock()

	tok, err := m.CreateProxyToken()
	if err != nil {
		t.Fatalf("CreateProxyToken: %v", err)
	}
	if tok != "proxy-token" {
		t.Fatalf("CreateProxyToken = %q, want %q", tok, "proxy-token")
	}
	if got := m.ProxyToken(); got != "proxy-token" {
		t.Fatalf("ProxyToken getter = %q, want %q", got, "proxy-token")
	}
	if !stub.hasProxyToken("proxy-token") {
		t.Fatal("stub never recorded the proxy token create")
	}
}

func TestManager_ClearTokens(t *testing.T) {
	m, cleanup := NewTestManager(t, http.NewServeMux(), "admin-xyz")
	defer cleanup()

	m.mu.Lock()
	m.proxyToken = "proxy-xyz"
	m.mu.Unlock()
	if m.AdminToken() == "" || m.ProxyToken() == "" {
		t.Fatal("precondition failed: tokens should be seeded")
	}

	m.ClearTokens()

	if got := m.AdminToken(); got != "" {
		t.Fatalf("AdminToken after Clear = %q, want empty", got)
	}
	if got := m.ProxyToken(); got != "" {
		t.Fatalf("ProxyToken after Clear = %q, want empty", got)
	}
}

// ----- EnsureAuth flow ---------------------------------------------------

func TestManager_EnsureAuth_NoOp_WhenTokenValid(t *testing.T) {
	stub := newVaultStub("pw")
	m, cleanup := NewTestManager(t, stub.handler(), "admin-pw")
	defer cleanup()

	stub.mu.Lock()
	stub.adminTokens["admin-pw"] = true
	stub.mu.Unlock()

	if err := m.EnsureAuth(); err != nil {
		t.Fatalf("EnsureAuth valid token: %v", err)
	}
	if got := m.AdminToken(); got != "admin-pw" {
		t.Fatalf("token changed on no-op path: %q", got)
	}
}

func TestManager_EnsureAuth_ReUnlock_WhenTokenInvalid(t *testing.T) {
	stub := newVaultStub("hunter2")
	m, cleanup := NewTestManager(t, stub.handler(), "stale-token")
	defer cleanup()

	// Seed the password file — EnsureAuth re-auths by reading it.
	dir := t.TempDir()
	m.dataDir = dir
	if err := os.WriteFile(m.PasswordFile(), []byte("hunter2\n"), 0o600); err != nil {
		t.Fatalf("seed password: %v", err)
	}

	if err := m.EnsureAuth(); err != nil {
		t.Fatalf("EnsureAuth re-unlock: %v", err)
	}
	if got := m.AdminToken(); got != "admin-hunter2" {
		t.Fatalf("AdminToken after re-unlock = %q, want %q", got, "admin-hunter2")
	}
}

func TestManager_EnsureAuth_NoPasswordFile_ReturnsError(t *testing.T) {
	stub := newVaultStub("pw")
	m, cleanup := NewTestManager(t, stub.handler(), "stale-token")
	defer cleanup()

	// dataDir is the zero value — PasswordFile() points nowhere readable.
	err := m.EnsureAuth()
	if err == nil {
		t.Fatal("expected error when token invalid and no password file")
	}
	if !strings.Contains(err.Error(), "password file") {
		t.Fatalf("error should mention password file: %v", err)
	}
}

// ----- Health + Client ---------------------------------------------------

func TestManager_Health_ReportsStubStatus(t *testing.T) {
	stub := newVaultStub("pw")
	m, cleanup := NewTestManager(t, stub.handler(), "")
	defer cleanup()

	status, err := m.Health()
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if status != "locked" {
		t.Fatalf("Health = %q, want %q", status, "locked")
	}

	// Flip status server-side.
	stub.mu.Lock()
	stub.status = "unlocked"
	stub.mu.Unlock()

	status, _ = m.Health()
	if status != "unlocked" {
		t.Fatalf("Health after unlock = %q, want %q", status, "unlocked")
	}
}

func TestManager_Client_UsesAdminToken(t *testing.T) {
	m, cleanup := NewTestManager(t, http.NewServeMux(), "my-admin")
	defer cleanup()

	c := m.Client()
	if c.Token != "my-admin" {
		t.Fatalf("Client token = %q, want %q", c.Token, "my-admin")
	}
}

// ----- Secret roundtrip via multipart ------------------------------------

func TestManager_SetSecret_GetSecret_Roundtrip(t *testing.T) {
	stub := newVaultStub("pw")
	m, cleanup := NewTestManager(t, stub.handler(), "admin-pw")
	defer cleanup()

	stub.mu.Lock()
	stub.adminTokens["admin-pw"] = true
	stub.mu.Unlock()

	if err := m.SetSecret("openrouter-key", "sk-test-value"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}

	got, err := m.GetSecret("openrouter-key")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if got != "sk-test-value" {
		t.Fatalf("GetSecret = %q, want %q", got, "sk-test-value")
	}
}

func TestManager_GetSecret_Missing_Errors(t *testing.T) {
	stub := newVaultStub("pw")
	m, cleanup := NewTestManager(t, stub.handler(), "admin-pw")
	defer cleanup()

	stub.mu.Lock()
	stub.adminTokens["admin-pw"] = true
	stub.mu.Unlock()

	_, err := m.GetSecret("does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestManager_SetSecret_WrongToken_Errors(t *testing.T) {
	stub := newVaultStub("pw")
	m, cleanup := NewTestManager(t, stub.handler(), "not-an-admin-token")
	defer cleanup()

	err := m.SetSecret("x", "y")
	if err == nil {
		t.Fatal("expected 401 error when admin token invalid")
	}
}

// ----- Lexical sanity on the multipart helper ----------------------------

// Guards against a regression where the multipart form name/file field
// could be swapped or mis-ordered and still yield a 200 from an
// overly-permissive upstream.
func TestManager_SetSecret_UsesMultipartNameAndFile(t *testing.T) {
	var received struct {
		name string
		data []byte
	}
	var gotErr error

	mux := http.NewServeMux()
	mux.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			gotErr = err
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		received.name = r.FormValue("name")

		file, _, err := r.FormFile("file")
		if err != nil {
			gotErr = err
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		received.data, _ = io.ReadAll(file)
	})

	m, cleanup := NewTestManager(t, mux, "ok")
	defer cleanup()

	if err := m.SetSecret("github-token", "ghp_abc"); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if gotErr != nil {
		t.Fatalf("multipart parse: %v", gotErr)
	}
	if received.name != "github-token" {
		t.Fatalf("field name = %q, want %q", received.name, "github-token")
	}
	if string(received.data) != "ghp_abc" {
		t.Fatalf("file body = %q, want %q", string(received.data), "ghp_abc")
	}
}
