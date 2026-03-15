package controlcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"
)

// BashHandler executes a bash command and returns the output.
type BashHandler struct{}

type bashRequest struct {
	Command string `json:"command"`
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Command == "" {
		http.Error(w, "command required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// Daemon already runs as uid 1000 (alf) — no credential switch needed.
	cmd := exec.CommandContext(ctx, "bash", "-c", req.Command)
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
