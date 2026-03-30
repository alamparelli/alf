package controlcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alamparelli/alf/internal/marketplace"
	"github.com/alamparelli/alf/internal/tooling"
)

// systemApps are platform-level apps that bypass sandbox and permission checks.
// They are bundled in the daemon image and not marketplace-managed.
var systemApps = map[string]bool{
	"developer": true,
}

// BashHandler executes a bash command and returns the output.
type BashHandler struct {
	Perms marketplace.PermissionChecker
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
	if appSlug != "" && !isSystemApp && h.Perms != nil && !h.Perms.HasPermission(appSlug, "bash") {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "permission denied: bash"})
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
		tooling.SandboxedCmd(cmd, req.Command, tooling.SandboxConfig{
			AppSlug:    appSlug,
			AppDataDir: appDataDir,
			Network:    hasNetwork,
		})
		cmd.Env = append(tooling.SandboxSafeEnv(appDataDir), "__SANDBOX_CMD="+req.Command)
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
