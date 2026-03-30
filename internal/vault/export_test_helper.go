package vault

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	vaultclient "github.com/alessandrolamparelli/vault-proxy/pkg/client"
)

// NewTestManager creates a Manager with a fake vault-server on a Unix socket.
// The handler serves vault API requests. Returns the manager and a cleanup function.
func NewTestManager(t *testing.T, handler http.Handler, adminToken string) (*Manager, func()) {
	t.Helper()

	// Use /tmp directly to avoid socket path length limit (~104 chars on macOS).
	tmpDir, err := os.MkdirTemp("", "vault-test-*")
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	sockPath := filepath.Join(tmpDir, "v.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}

	srv := &httptest.Server{
		Listener: ln,
		Config:   &http.Server{Handler: handler},
	}
	srv.Start()

	mgr := &Manager{
		socketPath: sockPath,
		adminToken: adminToken,
	}

	cleanup := func() {
		srv.Close()
		os.RemoveAll(tmpDir)
	}

	return mgr, cleanup
}

// TestClient creates a vault client connected to the manager's socket.
// Useful in tests that need direct client access.
func (m *Manager) TestClient(token string) *vaultclient.Client {
	return vaultclient.NewWithSocket(m.socketPath, token)
}
