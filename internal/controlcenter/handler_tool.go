package controlcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/alamparelli/alf/internal/marketplace"
	"github.com/alamparelli/alf/internal/tooling"
)

// ToolHandler executes an app's own CLI tool binary without granting raw shell access.
// Apps declare "tool" permission instead of "bash" to use this endpoint.
// The handler invokes /home/alf/data/tools/{slug} with JSON on stdin — no shell interpretation.
type ToolHandler struct {
	Perms   marketplace.PermissionChecker
	DataDir string // e.g. /home/alf/data
}

type toolRequest struct {
	Action string         `json:"action"`
	Args   map[string]any `json:"args,omitempty"`
}

type toolResponse struct {
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func (h *ToolHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var req toolRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySmall)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Action == "" {
		respondError(w, http.StatusBadRequest, "action required")
		return
	}

	// Identify calling app from Referer (same as bash handler).
	appSlug := extractAppSlugFromReferer(r)
	if appSlug == "" {
		respondError(w, http.StatusForbidden, "tool endpoint is app-only")
		return
	}

	// System apps don't need this endpoint — they have bash.
	if systemApps[appSlug] {
		respondError(w, http.StatusForbidden, "system apps should use /api/bash")
		return
	}

	// Check "tool" permission.
	if h.Perms != nil && !h.Perms.HasPermission(appSlug, "tool") {
		respondError(w, http.StatusForbidden, "permission denied: tool — add to manifest.json permissions")
		return
	}

	// Verify the tool binary exists.
	binPath := filepath.Join(h.DataDir, "tools", appSlug)
	if info, err := os.Stat(binPath); err != nil || info.IsDir() {
		respondError(w, http.StatusNotFound, "no tool binary for " + appSlug)
		return
	}

	// Build JSON payload for stdin.
	payload := map[string]any{"action": req.Action}
	for k, v := range req.Args {
		payload[k] = v
	}
	stdinBytes, _ := json.Marshal(payload)

	appDataDir := filepath.Join(h.DataDir, "apps", appSlug, "data")

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Execute the tool binary directly — no shell interpretation of user input.
	// SandboxedCmd rewrites the command to run inside a chroot. The actual binary
	// is passed via __SANDBOX_CMD env var. Stdin flows through to the binary.
	cmd := exec.CommandContext(ctx, binPath)
	cmd.Stdin = bytes.NewReader(stdinBytes)

	sandboxCfg := tooling.SandboxConfig{
		AppSlug:    appSlug,
		AppDataDir: appDataDir,
		Network:    false, // tool invocations don't need network
	}
	tooling.SandboxedCmd(cmd, binPath, sandboxCfg)
	env := tooling.SandboxSafeEnv(appDataDir)
	env = append(env, "__SANDBOX_CMD="+binPath)
	cmd.Env = env

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	var resp toolResponse
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
