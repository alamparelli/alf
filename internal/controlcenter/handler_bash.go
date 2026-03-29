package controlcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"syscall"
	"time"

	"github.com/alamparelli/alf/internal/marketplace"
)

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

	// Permission check: if request identifies an app, verify bash permission.
	// Absent app_slug = direct LLM/terminal call = always allowed.
	if req.AppSlug != "" && h.Perms != nil && !h.Perms.HasPermission(req.AppSlug, "bash") {
		respondJSON(w, http.StatusForbidden, map[string]string{"error": "permission denied: bash"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Drop to alf (uid 1000) — daemon runs as alfd (uid 1001) which has secret access.
	cmd := exec.CommandContext(ctx, "bash", "-c", req.Command)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 1000, Gid: 1000},
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
