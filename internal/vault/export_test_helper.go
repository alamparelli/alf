package vault

// NewTestManager creates a Manager pointing at a custom address with a preset admin token.
// Intended for integration tests that provide a fake vault HTTP server.
func NewTestManager(addr, adminToken string) *Manager {
	return &Manager{
		dataDir:    "",
		addr:       addr,
		adminToken: adminToken,
	}
}
