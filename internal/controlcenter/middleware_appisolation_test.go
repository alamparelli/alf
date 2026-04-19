package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func runAppIsolation(t *testing.T, referer, path string) *httptest.ResponseRecorder {
	t.Helper()
	handler := appIsolationMiddleware("https://cc.test")(okHandler())
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestAppIsolation_NoReferer_PassesThrough(t *testing.T) {
	rec := runAppIsolation(t, "", "/api/anything")
	if rec.Code != http.StatusOK {
		t.Errorf("no-referer API request should pass, got %d", rec.Code)
	}
}

func TestAppIsolation_NonAPIRequest_PassesThrough(t *testing.T) {
	rec := runAppIsolation(t, "https://cc.test/apps/myapp/", "/static/foo.css")
	if rec.Code != http.StatusOK {
		t.Errorf("non-/api/ request from app referer should pass, got %d", rec.Code)
	}
}

func TestAppIsolation_AppCanReachOwnProxy(t *testing.T) {
	rec := runAppIsolation(t, "https://cc.test/apps/myapp/index.html", "/apps/myapp/api/data")
	if rec.Code != http.StatusOK {
		t.Errorf("app should reach its own proxy, got %d", rec.Code)
	}
}

func TestAppIsolation_AppAllowedRoutes(t *testing.T) {
	for _, path := range []string{
		"/api/app-action",
		"/api/bash",
		"/api/events",
	} {
		rec := runAppIsolation(t, "https://cc.test/apps/myapp/", path)
		if rec.Code != http.StatusOK {
			t.Errorf("path %s must be allowed from app iframe, got %d", path, rec.Code)
		}
	}
}

func TestAppIsolation_OwnAppsStorage(t *testing.T) {
	rec := runAppIsolation(t, "https://cc.test/apps/myapp/", "/api/apps/myapp/storage")
	if rec.Code != http.StatusOK {
		t.Errorf("own apps/{slug} must be allowed, got %d", rec.Code)
	}
}

func TestAppIsolation_OtherAppStorageForbidden(t *testing.T) {
	rec := runAppIsolation(t, "https://cc.test/apps/myapp/", "/api/apps/other/storage")
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-app storage must be blocked, got %d", rec.Code)
	}
}

func TestAppIsolation_AdminEndpointsForbidden(t *testing.T) {
	for _, path := range []string{
		"/api/config",
		"/api/vault/status",
		"/api/tiers",
		"/api/memory/search",
	} {
		rec := runAppIsolation(t, "https://cc.test/apps/myapp/index.html", path)
		if rec.Code != http.StatusForbidden {
			t.Errorf("admin path %s must be forbidden from app iframe, got %d", path, rec.Code)
		}
	}
}

func TestAppIsolation_RefererWithoutAppSlug_PassesThrough(t *testing.T) {
	// Referer is set but not an app URL → appSlug="" → allow all.
	rec := runAppIsolation(t, "https://cc.test/", "/api/vault/status")
	if rec.Code != http.StatusOK {
		t.Errorf("non-app referer must be allowed, got %d", rec.Code)
	}
}
