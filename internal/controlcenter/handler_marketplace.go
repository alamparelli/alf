package controlcenter

import (
	"net/http"
	"strings"

	"github.com/alamparelli/alf/internal/marketplace"
)

// MarketplaceHandler handles /api/marketplace routes.
type MarketplaceHandler struct {
	Manager *marketplace.Manager
}

func (h *MarketplaceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/marketplace")
	path = strings.TrimPrefix(path, "/")

	// GET /api/marketplace → list local apps
	if r.Method == http.MethodGet && path == "" {
		apps := h.Manager.List()
		respondJSON(w, http.StatusOK, apps)
		return
	}

	// GET /api/marketplace/updates → check for available updates
	if r.Method == http.MethodGet && path == "updates" {
		updates := h.Manager.CheckUpdates()
		respondJSON(w, http.StatusOK, updates)
		return
	}

	// GET /api/marketplace/catalog → list remote registry apps
	if r.Method == http.MethodGet && path == "catalog" {
		remote, err := h.Manager.FetchCatalog()
		if err != nil {
			respondJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		respondJSON(w, http.StatusOK, remote)
		return
	}

	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// POST /api/marketplace/{slug}/{action}
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "expected /api/marketplace/{slug}/{action}"})
		return
	}

	slug := parts[0]
	action := parts[1]

	var err error
	switch action {
	case "install":
		err = h.Manager.Install(slug)
	case "update":
		err = h.Manager.Update(slug)
	case "enable":
		err = h.Manager.Enable(slug)
	case "disable":
		err = h.Manager.Disable(slug)
	case "uninstall":
		err = h.Manager.Uninstall(slug)
	default:
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown action: " + action})
		return
	}

	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no such file") {
			respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
