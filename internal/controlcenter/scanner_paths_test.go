package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestScannerPaths_RejectedWithoutAuth verifies that paths commonly probed by
// automated scanners (referenced in issue #267) return 401, not 200 or leaked
// content. Guards against regressions where a route is added without going
// through the auth middleware.
func TestScannerPaths_RejectedWithoutAuth(t *testing.T) {
	exempt := map[string]bool{
		"/health": true,
		"/auth":   true,
	}
	handler := authMiddleware("test-token", nil, exempt)(okHandler())

	scannerPaths := []string{
		// Env/config leaks
		"/.env",
		"/.env.local",
		"/.env.prod",
		"/.env.dev",
		"/.env.backup",
		// VCS
		"/.git/config",
		"/.git/HEAD",
		"/.gitignore",
		// Vite dev traversal
		"/@fs/etc/passwd",
		"/@fs/app/.git/config",
		// Wordpress probes
		"/wp-admin",
		"/wp-login.php",
		"/xmlrpc.php",
		// Admin panels
		"/phpmyadmin",
		"/admin",
		"/administrator",
		// Config dumps
		"/config.json",
		"/config.yml",
		"/docker-compose.yml",
		// Ops endpoints
		"/server-status",
		"/metrics",
		"/debug/pprof",
		"/debug/pprof/heap",
		// API fuzzing
		"/api",
		"/api/v1/users",
	}

	for _, path := range scannerPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("scanner path %s: expected 401, got %d (body=%q)",
					path, rec.Code, rec.Body.String())
			}
		})
	}
}
