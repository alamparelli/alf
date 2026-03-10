package controlcenter

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/vault"
)

// VaultHandler proxies requests to the vault-server via the Manager.
type VaultHandler struct {
	Manager    *vault.Manager // nil = vault binaries not present
	ContextDir string         // path to context/ dir for toolbox regeneration
	DataDir    string         // path to data dir for toolbox regeneration
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
	needsAuth := path != "" && path != "status" && path != "unlock" && path != "reset" && path != "oauth2/callback"
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
	case path == "reset" && r.Method == http.MethodPost:
		h.handleReset(w, r)
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
	case path == "oauth2/authorize" && r.Method == http.MethodPost:
		h.handleOAuth2Authorize(w, r)
	case path == "oauth2/callback" && r.Method == http.MethodGet:
		h.handleOAuth2Callback(w, r)
	case path == "files" && r.Method == http.MethodGet:
		h.handleListFiles(w, r)
	case path == "files" && r.Method == http.MethodPost:
		h.handleUploadFile(w, r)
	case strings.HasPrefix(path, "files/") && r.Method == http.MethodGet:
		name := strings.TrimPrefix(path, "files/")
		if !isVaultSafeName(name) {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
			return
		}
		h.handleGetFile(w, r, name)
	case strings.HasPrefix(path, "files/") && r.Method == http.MethodDelete:
		name := strings.TrimPrefix(path, "files/")
		if !isVaultSafeName(name) {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
			return
		}
		h.handleDeleteFile(w, r, name)
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
			"available":  true,
			"status":     "unreachable",
			"first_time": h.Manager.IsFirstTime(),
			"error":      err.Error(),
		})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"available":  true,
		"status":     status,
		"first_time": h.Manager.IsFirstTime(),
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
	// Persist master password so auto-unlock works after container restart.
	// Docker secrets are read-only, so write to the vault data directory instead.
	if pwFile := h.Manager.PasswordFile(); pwFile != "" {
		if err := os.WriteFile(pwFile, []byte(req.Password), 0600); err != nil {
			log.Printf("[vault] warning: failed to persist master password: %v", err)
		}
	}
	// Propagate env vars so the LLM subprocess can use the vault CLI tool.
	os.Setenv("VAULT_ADDR", h.Manager.Addr())
	os.Setenv("VAULT_TOKEN", h.Manager.ProxyToken())
	// Regenerate toolbox so LLM sees vault as "ready".
	if h.ContextDir != "" {
		memory.GenerateToolbox(h.ContextDir, h.DataDir)
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
	// Regenerate toolbox so LLM sees vault as "locked".
	if h.ContextDir != "" {
		memory.GenerateToolbox(h.ContextDir, h.DataDir)
	}
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

func (h *VaultHandler) handleReset(w http.ResponseWriter, r *http.Request) {
	// Lock first if unlocked (ignore errors — may already be locked).
	c := h.Manager.Client()
	_ = c.Lock()
	if err := h.Manager.Reset(); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	os.Unsetenv("VAULT_TOKEN")
	// Clear persisted master password so auto-unlock doesn't use stale credentials.
	if pwFile := h.Manager.PasswordFile(); pwFile != "" {
		os.Remove(pwFile)
	}
	if h.ContextDir != "" {
		memory.GenerateToolbox(h.ContextDir, h.DataDir)
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- File management ---

func (h *VaultHandler) handleListFiles(w http.ResponseWriter, r *http.Request) {
	c := h.Manager.Client()
	files, err := c.ListFiles()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, files)
}

func (h *VaultHandler) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	// Parse multipart from browser, save to temp, proxy via client.
	if err := r.ParseMultipartForm(5 << 20); err != nil { // 5MB
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart: " + err.Error()})
		return
	}
	name := r.FormValue("name")
	if name == "" || !isVaultSafeName(name) {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "file required"})
		return
	}
	defer file.Close()

	// Write to temp file for the client.
	tmp, err := os.CreateTemp("", "vault-upload-*")
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "temp file: " + err.Error()})
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "write temp: " + err.Error()})
		return
	}
	tmp.Close()

	c := h.Manager.Client()
	if err := c.UploadFile(name, tmpPath); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (h *VaultHandler) handleGetFile(w http.ResponseWriter, _ *http.Request, name string) {
	c := h.Manager.Client()
	data, err := c.GetFile(name)
	if err != nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+filepath.Base(name))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

func (h *VaultHandler) handleDeleteFile(w http.ResponseWriter, _ *http.Request, name string) {
	c := h.Manager.Client()
	if err := c.DeleteFile(name); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *VaultHandler) handleOAuth2Callback(w http.ResponseWriter, r *http.Request) {
	// Proxy the Google OAuth2 callback to vault-server.
	// This is a browser redirect — no auth token, just query params (code, state).
	addr := h.Manager.Addr()
	proxyURL := addr + "/auth/oauth2/callback?" + r.URL.RawQuery
	resp, err := http.Get(proxyURL)
	if err != nil {
		http.Error(w, "vault unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (h *VaultHandler) handleOAuth2Authorize(w http.ResponseWriter, r *http.Request) {
	// Proxy POST /auth/oauth2/authorize to vault-server.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	addr := h.Manager.Addr()
	token := h.Manager.AdminToken()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, addr+"/auth/oauth2/authorize", strings.NewReader(string(body)))
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]string{"error": "vault unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// isVaultSafeName validates that a name/id has no path traversal characters.
func isVaultSafeName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.Contains(s, "/") && !strings.Contains(s, "\\")
}

