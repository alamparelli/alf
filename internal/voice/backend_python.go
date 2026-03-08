//go:build !(linux && arm64)

package voice

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Transcriber manages a persistent faster-whisper Python process.
// Used on amd64 and macOS where faster-whisper has good INT8 performance.
type Transcriber struct {
	mu         sync.Mutex
	scriptPath string
	model      string
	modelsDir  string
	timeout    time.Duration

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	ready  bool
}

// New creates a new Transcriber. Does NOT start the process — call Start().
func New(scriptPath, model, modelsDir string, timeout time.Duration) (*Transcriber, error) {
	if _, err := exec.LookPath("python3"); err != nil {
		return nil, fmt.Errorf("python3 not found in PATH: %w", err)
	}

	if model == "" {
		model = "small"
	}
	if modelsDir == "" {
		modelsDir = "/home/alf/data/models"
	}

	return &Transcriber{
		scriptPath: scriptPath,
		model:      model,
		modelsDir:  modelsDir,
		timeout:    timeout,
	}, nil
}

// Start launches the persistent Python process and waits for model load.
func (t *Transcriber) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ready {
		return nil
	}

	cmd := exec.Command("python3", t.scriptPath,
		"--server",
		"--model", t.model,
		"--models-dir", t.modelsDir,
	)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("start process: %w", err)
	}

	reader := bufio.NewReader(stdout)

	// Wait for the "ready" message from the Python process.
	line, err := reader.ReadString('\n')
	if err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("waiting for ready signal: %w", err)
	}

	var status struct {
		Status string `json:"status"`
		Model  string `json:"model"`
	}
	if err := json.Unmarshal([]byte(line), &status); err != nil || status.Status != "ready" {
		cmd.Process.Kill()
		return fmt.Errorf("unexpected ready signal: %s", line)
	}

	t.cmd = cmd
	t.stdin = stdin
	t.reader = reader
	t.ready = true

	log.Printf("voice: faster-whisper ready (model=%s, pid=%d)", t.model, cmd.Process.Pid)
	return nil
}

// Stop kills the persistent process.
func (t *Transcriber) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cmd != nil && t.cmd.Process != nil {
		t.stdin.Close()
		t.cmd.Process.Kill()
		t.cmd.Wait()
		t.cmd = nil
		t.ready = false
		log.Println("voice: faster-whisper stopped")
	}
}

// IsReady returns true if the model is loaded and ready.
func (t *Transcriber) IsReady() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ready
}

// Transcribe sends an audio file to the persistent process and returns the result.
func (t *Transcriber) Transcribe(audioPath string) (*TranscribeResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.ready {
		return nil, fmt.Errorf("transcriber not ready")
	}

	req := map[string]string{"audio_file": audioPath}
	reqBytes, _ := json.Marshal(req)
	reqBytes = append(reqBytes, '\n')

	if _, err := t.stdin.Write(reqBytes); err != nil {
		t.ready = false
		return nil, fmt.Errorf("write to whisper process: %w", err)
	}

	type readResult struct {
		line string
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := t.reader.ReadString('\n')
		ch <- readResult{line, err}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			t.ready = false
			return nil, fmt.Errorf("read from whisper process: %w", res.err)
		}
		var result TranscribeResult
		if err := json.Unmarshal([]byte(res.line), &result); err != nil {
			return nil, fmt.Errorf("parse response: %w (raw: %s)", err, res.line)
		}
		if result.Error != "" {
			return nil, fmt.Errorf("whisper: %s", result.Error)
		}
		if result.Text == "" {
			return nil, fmt.Errorf("empty transcription")
		}
		return &result, nil

	case <-time.After(t.timeout):
		t.ready = false
		t.cmd.Process.Kill()
		return nil, fmt.Errorf("transcription timeout after %v", t.timeout)
	}
}

// DownloadAndTranscribe downloads a voice file from Telegram, transcribes it, cleans up.
func (t *Transcriber) DownloadAndTranscribe(client *http.Client, botToken, fileID string) (*TranscribeResult, error) {
	return downloadAndTranscribe(t.Transcribe, client, botToken, fileID)
}

// IsAvailable checks if python3 and the transcription script exist.
func IsAvailable(scriptPath string) bool {
	if _, err := exec.LookPath("python3"); err != nil {
		return false
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return false
	}
	return true
}
