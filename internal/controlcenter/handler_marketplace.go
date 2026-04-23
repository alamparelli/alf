package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/alamparelli/alf/internal/marketplace"
	vault "github.com/alamparelli/alf/internal/sandbox/secrets"
	vaultclient "github.com/alessandrolamparelli/vault-proxy/pkg/client"
)

// MarketplaceHandler handles /api/marketplace routes.
type MarketplaceHandler struct {
	Manager      *marketplace.Manager
	EventBroker  *EventBroker
	VaultManager *vault.Manager
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

	// GET /api/marketplace/developer → check if user is a developer
	if r.Method == http.MethodGet && path == "developer" {
		h.developerStatus(w)
		return
	}

	// GET /api/marketplace/catalog → list remote registry apps
	if r.Method == http.MethodGet && path == "catalog" {
		remote, err := h.Manager.FetchCatalog()
		if err != nil {
			respondError(w, http.StatusBadGateway, err.Error())
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
		respondError(w, http.StatusBadRequest, "expected /api/marketplace/{slug}/{action}")
		return
	}

	slug := parts[0]
	action := parts[1]

	// Validate slug to prevent path traversal.
	if !validName.MatchString(slug) {
		respondError(w, http.StatusBadRequest, "invalid slug")
		return
	}

	var err error
	switch action {
	case "install":
		err = h.Manager.Install(slug)
	case "update":
		err = h.Manager.Update(slug)
	case "uninstall":
		err = h.Manager.Uninstall(slug)
	default:
		respondError(w, http.StatusBadRequest, "unknown action: " + action)
		return
	}

	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no such file") {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.EventBroker.Emit(EventMarketplace)
	h.EventBroker.Emit(EventApps)

	// Include trust info in response for install actions
	resp := map[string]any{"ok": true}
	if action == "install" {
		apps := h.Manager.List()
		for _, app := range apps {
			if app.Slug == slug {
				resp["trusted"] = app.Trusted
				resp["permissions"] = app.Permissions
				break
			}
		}
	}
	respondJSON(w, http.StatusOK, resp)
}

// developerStatus checks if the user has a marketplace vault service configured
// and returns the developer name if connected.
func (h *MarketplaceHandler) developerStatus(w http.ResponseWriter) {
	notDev := map[string]any{"is_developer": false}

	if h.VaultManager == nil {
		respondJSON(w, http.StatusOK, notDev)
		return
	}

	socketPath := h.VaultManager.SocketPath()
	token := h.VaultManager.ProxyToken()
	if socketPath == "" || token == "" {
		respondJSON(w, http.StatusOK, notDev)
		return
	}

	// Proxy health check through vault-proxy to the marketplace service.
	vc := vaultclient.NewWithSocket(socketPath, token)
	resp, err := vc.Do("GET", "/proxy/marketplace/api/health", nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		respondJSON(w, http.StatusOK, notDev)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		respondJSON(w, http.StatusOK, notDev)
		return
	}

	var health struct {
		Status    string `json:"status"`
		Developer string `json:"developer"`
	}
	if err := json.Unmarshal(body, &health); err != nil || health.Status != "connected" {
		respondJSON(w, http.StatusOK, notDev)
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"is_developer": true,
		"developer":    health.Developer,
	})
}
