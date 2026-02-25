package controlcenter

import (
	"io/fs"
	"net/http"
	"path/filepath"
)

// Deps holds all dependencies needed to build the control center.
type Deps struct {
	ConfigStore    ConfigStore
	TierStore      TierStore
	LogReader      LogReader
	StatusProvider StatusProvider
	Notifier       Notifier
	AuthToken      string
	DashboardHTML  string
	WebFS          fs.FS // embedded web assets (style.css, app.js)
}

// StoreFactory creates concrete store implementations from a data directory.
func StoreFactory(dataDir string) (ConfigStore, TierStore) {
	cs := NewFileConfigStore(ConfigPath(dataDir))
	ts := NewFileTierStore(TiersPath(dataDir))
	return cs, ts
}

// LogReaderFactory creates a LogReader from a data directory.
func LogReaderFactory(dataDir string) LogReader {
	logDir := filepath.Join(dataDir, "logs")
	return NewFileLogReader(logDir, nil)
}

// HandlerFactory builds the HTTP mux with all routes and middleware.
func HandlerFactory(deps Deps) http.Handler {
	mux := http.NewServeMux()

	// API routes.
	mux.Handle("/api/config", &ConfigHandler{
		Store:    deps.ConfigStore,
		Notifier: deps.Notifier,
	})
	mux.Handle("/api/tiers", &TiersHandler{
		Store:    deps.TierStore,
		Notifier: deps.Notifier,
	})
	mux.Handle("/api/status", &StatusHandler{
		Provider: deps.StatusProvider,
	})
	mux.Handle("/api/logs", &LogsHandler{
		Reader: deps.LogReader,
	})

	// Health (exempt from auth).
	mux.Handle("/health", &HealthHandler{})

	// Static assets (CSS, JS) — served from embedded web/ directory.
	if deps.WebFS != nil {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(deps.WebFS))))
	}

	// Dashboard.
	mux.Handle("/", &DashboardHandler{
		HTML:  deps.DashboardHTML,
		Token: deps.AuthToken,
	})

	// Apply middleware stack (outermost first).
	exempt := map[string]bool{"/health": true}
	var handler http.Handler = mux
	handler = jsonMiddleware(handler)
	handler = authMiddleware(deps.AuthToken, exempt)(handler)
	handler = corsMiddleware(handler)
	handler = newRateLimiter(60).middleware(handler)
	handler = loggingMiddleware(handler)

	return handler
}
