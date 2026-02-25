package controlcenter

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"
)

//go:embed web/*
var webFS embed.FS

// Server is the Control Center HTTP server.
type Server struct {
	httpServer *http.Server
	addr       string
}

// New creates a Control Center server.
// dataDir is the path to config/tiers/logs directory.
// stats, version, authToken, and reloadCh are provided by the daemon.
func New(dataDir string, stats *Stats, version string, authToken string, reloadCh chan ReloadEvent) (*Server, error) {
	configStore, tierStore := StoreFactory(dataDir)
	logReader := LogReaderFactory(dataDir)
	statusProvider := NewStatusProvider(stats, version)
	notifier := NewChannelNotifier(reloadCh)

	// Load initial tiers into memory.
	if err := tierStore.Reload(); err != nil {
		log.Printf("[CC] warning: failed to load tiers: %v", err)
	}

	htmlBytes, err := webFS.ReadFile("web/index.html")
	if err != nil {
		return nil, fmt.Errorf("read dashboard HTML: %w", err)
	}

	// Sub-filesystem rooted at web/ for static asset serving.
	webSub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, fmt.Errorf("create web sub-fs: %w", err)
	}

	handler := HandlerFactory(Deps{
		ConfigStore:    configStore,
		TierStore:      tierStore,
		LogReader:      logReader,
		StatusProvider: statusProvider,
		Notifier:       notifier,
		AuthToken:      authToken,
		DashboardHTML:  string(htmlBytes),
		WebFS:          webSub,
	})

	addr := "127.0.0.1:8080"
	return &Server{
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      handler,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		addr: addr,
	}, nil
}

// Start begins listening. Blocks until the server stops.
func (s *Server) Start() error {
	log.Printf("[CC] listening on %s", s.addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
