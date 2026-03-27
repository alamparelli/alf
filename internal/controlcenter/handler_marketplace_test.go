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

// ---------------------------------------------------------------------------
// SEC-001: Marketplace slug path traversal prevention
// ---------------------------------------------------------------------------

func newMarketplaceHandler(t *testing.T) *MarketplaceHandler {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "marketplace"), 0o755)
	return &MarketplaceHandler{
		Manager: marketplace.NewManager(filepath.Join(dir, "marketplace")),
	}
}

func TestMarketplace_SlugPathTraversal_DotDot(t *testing.T) {
	h := newMarketplaceHandler(t)
	// ../config should be rejected before hitting the manager
	req := httptest.NewRequest("POST", "/api/marketplace/../config/install", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("slug '../config' should return 400, got %d", rec.Code)
	}
}

func TestMarketplace_SlugPathTraversal_Slash(t *testing.T) {
	h := newMarketplaceHandler(t)
	req := httptest.NewRequest("POST", "/api/marketplace/evil%2Fpath/install", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("slug with slash should return 400, got %d", rec.Code)
	}
}

func TestMarketplace_SlugPathTraversal_NullByte(t *testing.T) {
	h := newMarketplaceHandler(t)
	req := httptest.NewRequest("POST", "/api/marketplace/app%00evil/install", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("slug with null byte should return 400, got %d", rec.Code)
	}
}

func TestMarketplace_SlugPathTraversal_DotOnly(t *testing.T) {
	h := newMarketplaceHandler(t)
	req := httptest.NewRequest("POST", "/api/marketplace/./install", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("slug '.' should return 400, got %d", rec.Code)
	}
}

func TestMarketplace_ValidSlug_Accepted(t *testing.T) {
	h := newMarketplaceHandler(t)
	// A valid slug with allowed characters should pass validation
	// (it will then fail with 404 since the app doesn't exist, but not 400)
	req := httptest.NewRequest("POST", "/api/marketplace/my-app_123/install", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		t.Errorf("valid slug 'my-app_123' should not return 400, got body: %s", rec.Body.String())
	}
}

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
