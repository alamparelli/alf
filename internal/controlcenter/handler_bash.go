package controlcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alamparelli/alf/internal/marketplace"
	"github.com/alamparelli/alf/internal/tooling"
	"github.com/alamparelli/alf/internal/vault"
)

// systemApps are platform-level apps that bypass sandbox and permission checks.
// They are bundled in the daemon image and not marketplace-managed.
var systemApps = map[string]bool{
	"developer": true,
}

// BashHandler executes a bash command and returns the output.
type BashHandler struct {
	Perms   marketplace.PermissionChecker
	DataDir string // e.g. /home/alf/data

	// Vault proxy support for sandboxed apps with network permission.
	// When set, apps with "network" permission get a per-app vault proxy socket
	// mounted into their sandbox so they can use vault CLI without direct token access.
	VaultManager *vault.Manager

	// ServiceGetter returns the declared vault services for an app slug.
	// Used to scope the vault proxy to only the services the app declared.
	ServiceGetter func(slug string) []string

	mu             sync.Mutex
	vaultProxies   map[string]*vault.VaultProxy // slug → proxy
	vaultListeners map[string]net.Listener      // slug → listener
}

type bashRequest struct {
	Command string `json:"command"`
	AppSlug string `json:"app_slug,omitempty"` // set by SDK to identify calling app
}

type bashResponse struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func (h *BashHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req bashRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySmall)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Command == "" {
		http.Error(w, "command required", http.StatusBadRequest)
		return
	}

	// SEC-001: Server-side app identification via Referer header.
	// Iframe apps at /apps/{slug}/ send Referer automatically.
	// If Referer indicates an app, enforce bash permission — regardless of app_slug in body.
	// Non-app callers (LLM, terminal, tools-socket) have no /apps/ Referer.
	appSlug := extractAppSlugFromReferer(r)
	// SEC-003: System app bypass ONLY from verified Referer or tools socket.
	// The body-provided app_slug is never trusted for privilege escalation.
	isSystemApp := appSlug != "" && systemApps[appSlug]
	if appSlug == "" {
		appSlug = req.AppSlug // fallback to body for non-browser callers (no privilege gain)
	}
	// Cross-check: if request was authenticated via app Bearer token,
	// enforce the token's slug. This prevents:
	// 1. Forging Referer to impersonate another app (slug mismatch → 403)
	// 2. Omitting Referer + app_slug to escape sandbox (token slug forced)
	if tokenSlug := AppTokenSlugFromContext(r.Context()); tokenSlug != "" {
		if appSlug == "" {
			appSlug = tokenSlug
		} else if tokenSlug != appSlug {
			respondJSON(w, http.StatusForbidden, map[string]string{"error": "token/app slug mismatch"})
			return
		}
	}
	if appSlug != "" && !isSystemApp && h.Perms != nil && !h.Perms.HasPermission(appSlug, "bash") {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "permission denied: bash — add to manifest.json permissions"})
		return
	}

	// Sandbox ALL app-initiated bash except system apps.
	// Both marketplace and local apps get full namespace isolation.
	sandboxApp := appSlug != "" && !isSystemApp

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", req.Command)

	if sandboxApp {
		// Marketplace app-initiated bash: full namespace sandbox.
		appDataDir := filepath.Join("/home/alf/data/apps", appSlug, "data")
		hasNetwork := h.Perms != nil && h.Perms.HasPermission(appSlug, "network")
		sandboxCfg := tooling.SandboxConfig{
			AppSlug:    appSlug,
			AppDataDir: appDataDir,
			Network:    hasNetwork,
		}

		// Vault proxy: apps with network get a per-app proxy socket.
		var vaultSockPath string
		if hasNetwork && h.VaultManager != nil && h.VaultManager.ProxyToken() != "" {
			vaultSockPath = h.ensureVaultProxy(appSlug)
			if vaultSockPath != "" {
				sandboxCfg.VaultSocket = vaultSockPath
			}
		}

		tooling.SandboxedCmd(cmd, req.Command, sandboxCfg)
		env := tooling.SandboxSafeEnv(appDataDir)
		env = append(env, "__SANDBOX_CMD="+req.Command)
		if vaultSockPath != "" {
			env = append(env, "VAULT_PROXY_SOCK="+vaultSockPath)
		}
		cmd.Env = env
	} else {
		// LLM/terminal/internal-app bash: no sandbox, just uid drop.
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
		}
	}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	var resp bashResponse
	if err := cmd.Run(); err != nil {
		resp.ExitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			resp.ExitCode = exitErr.ExitCode()
		}
		resp.Error = err.Error()
	}

	output := out.String()
	if len(output) > 64*1024 {
		output = output[:64*1024] + "\n... (truncated)"
	}
	resp.Output = output

	respondJSON(w, http.StatusOK, resp)
}

// ensureVaultProxy creates or reuses a per-app vault proxy socket.
// The socket is placed in the app's data dir so it's accessible inside the sandbox.
// Returns the socket path, or empty string on failure.
func (h *BashHandler) ensureVaultProxy(slug string) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.vaultProxies == nil {
		h.vaultProxies = make(map[string]*vault.VaultProxy)
		h.vaultListeners = make(map[string]net.Listener)
	}

	currentToken := h.VaultManager.ProxyToken()

	// Reuse existing proxy, refresh token if vault was restarted.
	if proxy, ok := h.vaultProxies[slug]; ok {
		proxy.UpdateToken(currentToken)
		return h.vaultSockPath(slug)
	}

	// Create new proxy scoped to the app's declared vault services.
	sockPath := h.vaultSockPath(slug)
	var services []string
	if h.ServiceGetter != nil {
		services = h.ServiceGetter(slug)
	}
	proxy := vault.NewVaultProxy(h.VaultManager.SocketPath(), currentToken, services)
	ln, err := proxy.ListenAndServe(sockPath)
	if err != nil {
		log.Printf("[bash] vault proxy for %s failed: %v", slug, err)
		return ""
	}

	h.vaultProxies[slug] = proxy
	h.vaultListeners[slug] = ln
	log.Printf("[bash] vault proxy for %s on %s", slug, sockPath)
	return sockPath
}

func (h *BashHandler) vaultSockPath(slug string) string {
	return filepath.Join(h.DataDir, "apps", slug, "vault.sock")
}

// extractAppSlugFromReferer extracts the app slug from a Referer like
// "https://host/apps/my-app/" or "https://host/apps/my-app/index.html".
// Returns empty string if the Referer doesn't match the /apps/{slug}/ pattern.
func extractAppSlugFromReferer(r *http.Request) string {
	ref := r.Header.Get("Referer")
	if ref == "" {
		return ""
	}
	// Find /apps/ in the path
	idx := strings.Index(ref, "/apps/")
	if idx < 0 {
		return ""
	}
	rest := ref[idx+len("/apps/"):]
	// Extract slug (up to next / or end)
	if slashIdx := strings.Index(rest, "/"); slashIdx > 0 {
		rest = rest[:slashIdx]
	}
	if rest == "" || !validName.MatchString(rest) {
		return ""
	}
	return rest
}
