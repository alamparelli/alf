package controlcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"syscall"
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
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

	cmd := exec.CommandContext(ctx, "bash", "-c", req.Command)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 1001, Gid: 1000},
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
