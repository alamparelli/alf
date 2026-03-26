package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alamparelli/alf/internal/marketplace"
)

func TestMarketplaceDeveloperStatus_NoVault(t *testing.T) {
	dir := t.TempDir()
	mgr := marketplace.NewManager(filepath.Join(dir, "marketplace"))
	// Ensure marketplace data dir exists so List() doesn't panic.
	os.MkdirAll(filepath.Join(dir, "marketplace"), 0o755)

	h := &MarketplaceHandler{
		Manager:      mgr,
		VaultManager: nil, // no vault configured
	}

	req := httptest.NewRequest("GET", "/api/marketplace/developer", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}

	isDev, ok := resp["is_developer"].(bool)
	if !ok {
		t.Fatalf("expected is_developer to be bool, got %T", resp["is_developer"])
	}
	if isDev {
		t.Error("expected is_developer=false when VaultManager is nil")
	}
}
