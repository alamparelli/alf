package controlcenter

import (
	"log"
	"net/http"
	"os"
	"time"
)

// RestartHandler handles POST /api/restart.
type RestartHandler struct{}

func (h *RestartHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "restarting"})
	log.Println("restart requested via API")
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
}
