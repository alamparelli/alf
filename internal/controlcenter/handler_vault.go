package controlcenter

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/alamparelli/alf/internal/vault"
)

// VaultHandler proxies requests to the vault-server via the Manager.
type VaultHandler struct {
	Manager *vault.Manager // nil = vault binaries not present
}

func (h *VaultHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Manager == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}

	// Strip prefix to get sub-route: status, unlock, lock, services, tokens.
	path := strings.TrimPrefix(r.URL.Path, "/api/vault")
	path = strings.TrimPrefix(path, "/")

	// Routes that need a valid admin token — auto-recover if revoked.
	needsAuth := path != "" && path != "status" && path != "unlock"
	if needsAuth {
		if err := h.Manager.EnsureAuth(); err != nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "vault auth failed: " + err.Error()})
			return
		}
	}

	switch {
	case path == "" || path == "status":
		h.handleStatus(w, r)
	case path == "unlock" && r.Method == http.MethodPost:
		h.handleUnlock(w, r)
	case path == "lock" && r.Method == http.MethodPost:
		h.handleLock(w, r)
	case path == "services" && r.Method == http.MethodGet:
		h.handleListServices(w, r)
	case path == "services" && r.Method == http.MethodPost:
		h.handleAddService(w, r)
	case strings.HasPrefix(path, "services/") && strings.HasSuffix(path, "/test") && r.Method == http.MethodPost:
		name := strings.TrimPrefix(path, "services/")
		name = strings.TrimSuffix(name, "/test")
		if !isVaultSafeName(name) {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
			return
		}
		h.handleTestService(w, r, name)
	case strings.HasPrefix(path, "services/") && r.Method == http.MethodDelete:
		name := strings.TrimPrefix(path, "services/")
		if !isVaultSafeName(name) {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
			return
		}
		h.handleDeleteService(w, r, name)
	case path == "tokens" && r.Method == http.MethodGet:
		h.handleListTokens(w, r)
	case path == "tokens" && r.Method == http.MethodPost:
		h.handleCreateToken(w, r)
	case strings.HasPrefix(path, "tokens/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(path, "tokens/")
		if !isVaultSafeName(id) {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		h.handleRevokeToken(w, r, id)
	default:
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *VaultHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	status, err := h.Manager.Health()
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{
			"available": true,
			"status":    "unreachable",
			"error":     err.Error(),
		})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"available": true,
		"status":    status,
	})
}

func (h *VaultHandler) handleUnlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Password == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "password required"})
		return
	}
	if err := h.Manager.AutoUnlock(req.Password); err != nil {
		respondJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	// Create proxy token for Claude subprocess.
	if _, err := h.Manager.CreateProxyToken(); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "proxy token: " + err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *VaultHandler) handleLock(w http.ResponseWriter, r *http.Request) {
	c := h.Manager.Client()
	if err := c.Lock(); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.Manager.ClearTokens()
	os.Unsetenv("VAULT_TOKEN")
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *VaultHandler) handleListServices(w http.ResponseWriter, r *http.Request) {
	c := h.Manager.Client()
	services, err := c.ListServices()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, services)
}

func (h *VaultHandler) handleAddService(w http.ResponseWriter, r *http.Request) {
	c := h.Manager.Client()
	if err := c.AddService(http.MaxBytesReader(w, r.Body, 1<<20)); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *VaultHandler) handleDeleteService(w http.ResponseWriter, _ *http.Request, name string) {
	c := h.Manager.Client()
	if err := c.RemoveService(name); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *VaultHandler) handleTestService(w http.ResponseWriter, _ *http.Request, name string) {
	c := h.Manager.Client()
	if err := c.TestService(name); err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *VaultHandler) handleListTokens(w http.ResponseWriter, r *http.Request) {
	c := h.Manager.Client()
	tokens, err := c.ListTokens()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, tokens)
}

func (h *VaultHandler) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope string `json:"scope"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Scope == "" {
		req.Scope = "proxy"
	}
	c := h.Manager.Client()
	token, err := c.CreateToken(req.Scope)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"id": token})
}

func (h *VaultHandler) handleRevokeToken(w http.ResponseWriter, _ *http.Request, id string) {
	c := h.Manager.Client()
	if err := c.RevokeToken(id); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// isVaultSafeName validates that a name/id has no path traversal characters.
func isVaultSafeName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.Contains(s, "/") && !strings.Contains(s, "\\")
}

