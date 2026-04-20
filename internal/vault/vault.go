// Package vault is a thin re-export shim. The Secrets facet of Sandbox now
// lives at internal/sandbox/secrets (moved during #339 Step 3). Existing
// consumers keep importing internal/vault until Runtime (#340) rewires them.
package vault

import (
	"net/http"
	"testing"

	"github.com/alamparelli/alf/internal/sandbox/secrets"
)

// Manager is an alias for secrets.Manager.
type Manager = secrets.Manager

// VaultProxy is an alias for secrets.VaultProxy.
type VaultProxy = secrets.VaultProxy

// NewManager creates a new vault manager. Re-exports secrets.NewManager.
func NewManager(dataDir string) *Manager {
	return secrets.NewManager(dataDir)
}

// NewVaultProxy creates a new vault proxy. Re-exports secrets.NewVaultProxy.
func NewVaultProxy(vaultSocket, proxyToken string, services []string) *VaultProxy {
	return secrets.NewVaultProxy(vaultSocket, proxyToken, services)
}

// NewTestManager re-exports secrets.NewTestManager.
func NewTestManager(t *testing.T, handler http.Handler, adminToken string) (*Manager, func()) {
	return secrets.NewTestManager(t, handler, adminToken)
}
