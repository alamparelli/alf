package controlcenter

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/vault"
	vaultclient "github.com/alessandrolamparelli/vault-proxy/pkg/client"
)

// VaultHandler proxies requests to the vault-server via the Manager.
type VaultHandler struct {
	Manager     *vault.Manager // nil = vault binaries not present
	ContextDir  string         // path to context/ dir for toolbox regeneration
	DataDir     string         // path to data dir for toolbox regeneration
	OnUnlock    func()         // called after successful unlock (e.g. to migrate secrets)
	EventBroker *EventBroker
}

func (h *VaultHandler) emitVault() {
	if h.EventBroker != nil {
		h.EventBroker.Emit(EventVault)
	}
}

func (h *VaultHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Manager == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "vault not available"})
		return
	}

	// Strip prefix to get sub-route: status, unlock, lock, services, tokens.
	path := strings.TrimPrefix(r.URL.Path, "/api/vault")
	path = strings.TrimPrefix(path, "/")

	// Routes that need a valid admin token - auto-recover if revoked.
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
	case path == "export" && r.Method == http.MethodPost:
		h.handleExport(w, r)
	case path == "import" && r.Method == http.MethodPost:
		h.handleImport(w, r)
	case path == "secrets" && r.Method == http.MethodGet:
		h.handleListSecrets(w, r)
	case path == "secrets" && r.Method == http.MethodPost:
		h.handleSetSecret(w, r)
	case strings.HasPrefix(path, "secrets/") && r.Method == http.MethodDelete:
		name := strings.TrimPrefix(path, "secrets/")
		if !isVaultSafeName(name) {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
			return
		}
		h.handleDeleteSecret(w, r, name)
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
	case strings.HasPrefix(path, "services/") && r.Method == http.MethodPut:
		name := strings.TrimPrefix(path, "services/")
		if !isVaultSafeName(name) {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
			return
		}
		h.handleUpdateService(w, r, name)
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
	case path == "mobile-token":
		h.handleMobileToken(w, r)
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
		methodNotAllowed(w)
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
	resp := map[string]any{
		"available":  true,
		"status":     status,
		"first_time": h.Manager.IsFirstTime(),
	}
	// Include obfuscated built-in tokens when unlocked.
	if status == "unlocked" {
		if at := h.Manager.AdminToken(); at != "" {
			resp["admin_token"] = obfuscateToken(at)
		}
		if pt := h.Manager.ProxyToken(); pt != "" {
			resp["proxy_token"] = obfuscateToken(pt)
		}
	}
	respondJSON(w, http.StatusOK, resp)
}

func (h *VaultHandler) handleUnlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySmall)).Decode(&req); err != nil || req.Password == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "password required"})
		return
	}
	if err := h.Manager.AutoUnlock(req.Password); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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
	// Note: LLM subprocesses access vault via VAULT_PROXY_SOCK (set by daemon).
	// No VAULT_ADDR/VAULT_TOKEN env vars needed.
	// Regenerate toolbox so LLM sees vault as "ready".
	if h.ContextDir != "" {
		memory.GenerateToolbox(h.ContextDir, h.DataDir)
	}
	// Run post-unlock hook (e.g. migrate Telegram credentials into vault).
	if h.OnUnlock != nil {
		h.OnUnlock()
	}
	h.emitVault()
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *VaultHandler) handleLock(w http.ResponseWriter, r *http.Request) {
	c := h.Manager.Client()
	if err := c.Lock(); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.Manager.ClearTokens()
	// Note: VAULT_TOKEN is no longer in env (Unix socket proxy handles auth).
	// Regenerate toolbox so LLM sees vault as "locked".
	if h.ContextDir != "" {
		memory.GenerateToolbox(h.ContextDir, h.DataDir)
	}
	h.emitVault()
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

func (h *VaultHandler) handleUpdateService(w http.ResponseWriter, r *http.Request, name string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyLarge))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
		return
	}
	body = h.resolveSecretRefs(body)
	c := h.Manager.Client()
	if err := c.UpdateService(name, strings.NewReader(string(body))); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *VaultHandler) handleAddService(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyLarge))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "read body: " + err.Error()})
		return
	}
	// Resolve secret refs: if auth contains *_ref fields, look up the secret value.
	body = h.resolveSecretRefs(body)
	c := h.Manager.Client()
	if err := c.AddService(strings.NewReader(string(body))); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// resolveSecretRefs replaces *_ref fields in service auth with actual secret values.
// e.g. {"auth":{"type":"bearer","token_ref":"my_secret"}} → {"auth":{"type":"bearer","token":"<value>"}}
func (h *VaultHandler) resolveSecretRefs(body []byte) []byte {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return body
	}
	auth, ok := raw["auth"].(map[string]any)
	if !ok {
		return body
	}
	refMap := map[string]string{
		"token_ref":        "token",
		"header_value_ref": "header_value",
		"password_ref":     "password",
	}
	changed := false
	for ref, field := range refMap {
		if refName, ok := auth[ref].(string); ok && refName != "" {
			val, err := h.Manager.GetSecret(refName)
			if err == nil && val != "" {
				auth[field] = val
				delete(auth, ref)
				changed = true
				log.Printf("[vault] resolved secret ref %q for service field %q", refName, field)
			} else {
				log.Printf("[vault] warning: could not resolve secret ref %q: %v", refName, err)
			}
		}
	}
	if !changed {
		return body
	}
	raw["auth"] = auth
	result, err := json.Marshal(raw)
	if err != nil {
		return body
	}
	return result
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySmall)).Decode(&req); err != nil || req.Scope == "" {
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
	// Lock first if unlocked (ignore errors - may already be locked).
	c := h.Manager.Client()
	_ = c.Lock()
	if err := h.Manager.Reset(); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Note: VAULT_TOKEN is no longer in env (Unix socket proxy handles auth).
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
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filepath.Base(name)))
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
	// Proxy the Google OAuth2 callback to vault-server via Unix socket.
	// This is a browser redirect - no auth token, just query params (code, state).
	vc := vaultclient.NewWithSocket(h.Manager.SocketPath(), "")
	resp, err := vc.Do("GET", "/auth/oauth2/callback?"+r.URL.RawQuery, nil)
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
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodySmall))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	vc := vaultclient.NewWithSocket(h.Manager.SocketPath(), h.Manager.AdminToken())
	vaultResp, err := vc.Do("POST", "/auth/oauth2/authorize", strings.NewReader(string(body)))
	if err != nil {
		respondJSON(w, http.StatusBadGateway, map[string]string{"error": "vault unreachable: " + err.Error()})
		return
	}
	defer vaultResp.Body.Close()

	respBody, _ := io.ReadAll(vaultResp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(vaultResp.StatusCode)
	w.Write(respBody)
}

// --- Export / Import ---

func (h *VaultHandler) handleExport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySmall)).Decode(&req); err != nil || req.Password == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "password required"})
		return
	}
	c := h.Manager.Client()

	// Export secrets (files)
	files, err := c.ListFiles()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	type exportEntry struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	var entries []exportEntry
	for _, f := range files {
		data, err := c.GetFile(f.Name)
		if err != nil {
			log.Printf("[vault] export: skip %s: %v", f.Name, err)
			continue
		}
		entries = append(entries, exportEntry{Name: f.Name, Value: string(data)})
	}

	// Export service metadata (credentials are not exposed by vault-server API,
	// but the secrets they reference are included in the entries above).
	services, err := c.ListServices()
	if err != nil {
		log.Printf("[vault] export: skip services: %v", err)
	}

	exportData := map[string]any{"secrets": entries}
	if len(services) > 0 {
		exportData["services"] = services
	}

	jsonData, err := json.Marshal(exportData)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "marshal: " + err.Error()})
		return
	}
	encrypted, err := EncryptVaultExport(jsonData, req.Password)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "encrypt: " + err.Error()})
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=vault-export.enc")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(encrypted)
}

func (h *VaultHandler) handleImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		Data     string `json:"data"` // base64-encoded encrypted data
		Secrets  []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"secrets"` // plain JSON import (backward compat)
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyImport)).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request: " + err.Error()})
		return
	}

	type secretEntry struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	type serviceEntry struct {
		Name           string   `json:"name"`
		BaseURL        string   `json:"base_url"`
		AuthType       string   `json:"auth_type"`
		TLSSkipVerify  bool     `json:"tls_skip_verify,omitempty"`
		SessionCookies bool     `json:"session_cookies,omitempty"`
		Scopes         []string `json:"scopes,omitempty"`
		SSHHost        string   `json:"ssh_host,omitempty"`
		SSHPort        int      `json:"ssh_port,omitempty"`
		SSHUser        string   `json:"ssh_user,omitempty"`
		SSHKeyFileRef  string   `json:"ssh_key_file_ref,omitempty"`
		HeaderName     string   `json:"header_name,omitempty"`
		Username       string   `json:"username,omitempty"`
	}

	var secrets []secretEntry
	var services []serviceEntry

	if req.Data != "" {
		// Encrypted import
		raw, err := base64.StdEncoding.DecodeString(req.Data)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid base64"})
			return
		}
		decrypted, err := DecryptVaultExport(raw, req.Password)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "decrypt failed: " + err.Error()})
			return
		}
		var parsed struct {
			Secrets  []secretEntry  `json:"secrets"`
			Services []serviceEntry `json:"services"`
		}
		if err := json.Unmarshal(decrypted, &parsed); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid decrypted data"})
			return
		}
		secrets = parsed.Secrets
		services = parsed.Services
	} else {
		// Plain JSON import (backward compat)
		for _, s := range req.Secrets {
			secrets = append(secrets, secretEntry{Name: s.Name, Value: s.Value})
		}
	}

	if len(secrets) == 0 && len(services) == 0 {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "no secrets or services to import"})
		return
	}

	// Import secrets
	imported := 0
	for _, s := range secrets {
		if s.Name == "" || !isVaultSafeName(s.Name) {
			log.Printf("[vault] import: skip invalid name %q", s.Name)
			continue
		}
		if err := h.Manager.SetSecret(s.Name, s.Value); err != nil {
			log.Printf("[vault] import: failed %s: %v", s.Name, err)
			continue
		}
		imported++
	}

	// Import services — restore full metadata including SSH config.
	// Credentials (tokens, passwords) are never exported; users re-configure them.
	// SSH services are fully restored (host, port, user, key file ref) since the
	// private key PEM is included in the secrets export above.
	svcImported := 0
	c := h.Manager.Client()
	for _, svc := range services {
		if svc.Name == "" {
			continue
		}
		authType := svc.AuthType
		if authType == "" {
			authType = "bearer"
		}

		auth := map[string]any{"type": authType}
		switch authType {
		case "ssh_key":
			// SSH services: restore full connection config.
			// Private key is in vault files (imported above via secrets).
			auth["ssh_host"] = svc.SSHHost
			auth["ssh_user"] = svc.SSHUser
			if svc.SSHPort > 0 {
				auth["ssh_port"] = svc.SSHPort
			}
			if svc.SSHKeyFileRef != "" {
				auth["ssh_key_file_ref"] = svc.SSHKeyFileRef
			}
		case "header":
			auth["header_name"] = svc.HeaderName
			auth["token"] = "__imported_reconfigure_me__"
		case "basic":
			auth["username"] = svc.Username
			auth["password"] = "__imported_reconfigure_me__"
		default:
			// bearer, oauth2_client, service_account
			auth["token"] = "__imported_reconfigure_me__"
		}

		payload := map[string]any{
			"name": svc.Name,
			"auth": auth,
		}
		if svc.BaseURL != "" {
			payload["base_url"] = svc.BaseURL
		}
		if svc.TLSSkipVerify {
			payload["tls_skip_verify"] = true
		}
		if svc.SessionCookies {
			payload["session_cookies"] = true
		}
		if len(svc.Scopes) > 0 {
			payload["scopes"] = svc.Scopes
		}

		body, _ := json.Marshal(payload)
		if err := c.AddService(strings.NewReader(string(body))); err != nil {
			log.Printf("[vault] import service %s: %v", svc.Name, err)
			continue
		}
		svcImported++
	}

	log.Printf("[vault] imported %d/%d secrets, %d/%d services", imported, len(secrets), svcImported, len(services))
	respondJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"imported":          imported,
		"services_imported": svcImported,
	})
}

// --- Secret (key-value) management ---

func (h *VaultHandler) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	c := h.Manager.Client()
	files, err := c.ListFiles()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Return secret names (values are never exposed).
	type secretEntry struct {
		Name string `json:"name"`
		Set  bool   `json:"set"`
	}
	var secrets []secretEntry
	for _, f := range files {
		secrets = append(secrets, secretEntry{Name: f.Name, Set: true})
	}
	respondJSON(w, http.StatusOK, secrets)
}

func (h *VaultHandler) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyMedium)).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.Name == "" || !isVaultSafeName(req.Name) {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
		return
	}
	if req.Value == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "value required"})
		return
	}
	if err := h.Manager.SetSecret(req.Name, req.Value); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("[vault] secret %q set via API", req.Name)
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *VaultHandler) handleDeleteSecret(w http.ResponseWriter, _ *http.Request, name string) {
	c := h.Manager.Client()
	if err := c.DeleteFile(name); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("[vault] secret %q deleted via API", name)
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Mobile API Token ---

const mobileTokenSecret = "cc_mobile_token"

// handleMobileToken handles GET (check), POST (generate), DELETE (revoke) for mobile API tokens.
func (h *VaultHandler) handleMobileToken(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleMobileTokenGet(w)
	case http.MethodPost:
		h.handleMobileTokenCreate(w)
	case http.MethodDelete:
		h.handleMobileTokenRevoke(w)
	default:
		methodNotAllowed(w)
	}
}

func (h *VaultHandler) handleMobileTokenGet(w http.ResponseWriter) {
	token, err := h.Manager.GetSecret(mobileTokenSecret)
	if err != nil || token == "" {
		respondJSON(w, http.StatusOK, map[string]any{"exists": false})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"exists":    true,
		"token_masked": obfuscateToken(token),
	})
}

func (h *VaultHandler) handleMobileTokenCreate(w http.ResponseWriter) {
	// Generate a cryptographically random 64-char hex token.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to generate token"})
		return
	}
	token := hex.EncodeToString(b)

	if err := h.Manager.SetSecret(mobileTokenSecret, token); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to store token: " + err.Error()})
		return
	}
	log.Printf("[vault] mobile API token generated")
	// Return the full token — shown only once.
	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "token": token})
}

func (h *VaultHandler) handleMobileTokenRevoke(w http.ResponseWriter) {
	c := h.Manager.Client()
	if err := c.DeleteFile(mobileTokenSecret); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("[vault] mobile API token revoked")
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GetMobileToken returns the current mobile token from vault, or empty string.
func GetMobileToken(vm *vault.Manager) string {
	if vm == nil {
		return ""
	}
	token, err := vm.GetSecret(mobileTokenSecret)
	if err != nil {
		return ""
	}
	return token
}

// isVaultSafeName validates that a name/id has no path traversal characters.
var isVaultSafeName = isSafeName

// obfuscateToken shows the first 8 and last 4 chars of a token, masking the rest.
// Returns "***" for tokens too short to obfuscate safely.
func obfuscateToken(t string) string {
	if len(t) < 8 {
		return "***"
	}
	if len(t) <= 16 {
		return t[:4] + "..." + t[len(t)-4:]
	}
	return t[:8] + "..." + t[len(t)-4:]
}

