package memstore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Embedder manages a persistent sentence-transformers Python process.
// The model is loaded once and kept in memory for fast embedding generation.
type Embedder struct {
	mu         sync.Mutex
	scriptPath string
	model      string
	modelsDir  string
	timeout    time.Duration
	dims       int

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	ready  bool
}

// embedRequest is sent to the Python sidecar.
type embedRequest struct {
	ID    string   `json:"id"`
	Texts []string `json:"texts"`
}

// embedResponse is received from the Python sidecar.
type embedResponse struct {
	ID         string      `json:"id"`
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error,omitempty"`
}

// NewEmbedder creates a new Embedder. Does NOT start the process — call Start().
func NewEmbedder(scriptPath, model, modelsDir string, timeout time.Duration) (*Embedder, error) {
	if _, err := exec.LookPath("python3"); err != nil {
		return nil, fmt.Errorf("python3 not found in PATH: %w", err)
	}

	if model == "" {
		model = "all-MiniLM-L6-v2"
	}
	if modelsDir == "" {
		modelsDir = "/home/node/data/models"
	}

	return &Embedder{
		scriptPath: scriptPath,
		model:      model,
		modelsDir:  modelsDir,
		timeout:    timeout,
	}, nil
}

// Start launches the persistent Python process and waits for model load.
func (e *Embedder) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ready {
		return nil
	}

	cmd := exec.Command("python3", e.scriptPath,
		"--server",
		"--model", e.model,
		"--models-dir", e.modelsDir,
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

	// Wait for the "ready" message.
	line, err := reader.ReadString('\n')
	if err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("waiting for ready signal: %w", err)
	}

	var status struct {
		Status string `json:"status"`
		Dims   int    `json:"dims"`
	}
	if err := json.Unmarshal([]byte(line), &status); err != nil || status.Status != "ready" {
		cmd.Process.Kill()
		return fmt.Errorf("unexpected ready signal: %s", line)
	}

	e.cmd = cmd
	e.stdin = stdin
	e.reader = reader
	e.ready = true
	e.dims = status.Dims

	log.Printf("memstore: embedder ready (model=%s, dims=%d, pid=%d)", e.model, e.dims, cmd.Process.Pid)
	return nil
}

// Stop kills the persistent process.
func (e *Embedder) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.cmd != nil && e.cmd.Process != nil {
		e.stdin.Close()
		e.cmd.Process.Kill()
		e.cmd.Wait()
		e.cmd = nil
		e.ready = false
		log.Println("memstore: embedder stopped")
	}
}

// IsReady returns true if the model is loaded and ready.
func (e *Embedder) IsReady() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ready
}

// Dims returns the embedding dimension count.
func (e *Embedder) Dims() int {
	return e.dims
}

// Embed generates an embedding for a single text.
func (e *Embedder) Embed(text string) ([]float32, error) {
	vecs, err := e.EmbedBatch([]string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// EmbedBatch generates embeddings for multiple texts.
func (e *Embedder) EmbedBatch(texts []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.ready {
		return nil, fmt.Errorf("embedder not ready")
	}

	req := embedRequest{
		ID:    fmt.Sprintf("%d", time.Now().UnixNano()),
		Texts: texts,
	}
	reqBytes, _ := json.Marshal(req)
	reqBytes = append(reqBytes, '\n')

	if _, err := e.stdin.Write(reqBytes); err != nil {
		e.ready = false
		return nil, fmt.Errorf("write to embedder: %w", err)
	}

	// Read response with timeout.
	type readResult struct {
		line string
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := e.reader.ReadString('\n')
		ch <- readResult{line, err}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			e.ready = false
			return nil, fmt.Errorf("read from embedder: %w", res.err)
		}
		var resp embedResponse
		if err := json.Unmarshal([]byte(res.line), &resp); err != nil {
			return nil, fmt.Errorf("parse response: %w (raw: %s)", err, res.line)
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("embedder: %s", resp.Error)
		}
		return resp.Embeddings, nil

	case <-time.After(e.timeout):
		e.ready = false
		e.cmd.Process.Kill()
		return nil, fmt.Errorf("embedding timeout after %v", e.timeout)
	}
}

// IsAvailable checks if python3 and the embedding script exist.
func IsAvailable(scriptPath string) bool {
	if _, err := exec.LookPath("python3"); err != nil {
		return false
	}
	if _, err := os.Stat(scriptPath); err != nil {
		return false
	}
	return true
}
