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
	"path/filepath"
	"sync"
	"time"
)

// TranscribeResult holds the output from the transcription.
type TranscribeResult struct {
	Text                string  `json:"text"`
	DurationS           float64 `json:"duration_s"`
	Language            string  `json:"language"`
	LanguageProbability float64 `json:"language_probability"`
	Error               string  `json:"error,omitempty"`
}

// Transcriber manages a persistent faster-whisper Python process.
// The model is loaded once and kept in memory for fast transcription.
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
		modelsDir = "/home/node/data/models"
	}

	return &Transcriber{
		scriptPath: scriptPath,
		model:      model,
		modelsDir:  modelsDir,
		timeout:    timeout,
	}, nil
}

// Start launches the persistent Python process and waits for model load.
// This blocks until the model is ready (can take minutes on first run).
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

	log.Printf("voice: whisper server ready (model=%s, pid=%d)", t.model, cmd.Process.Pid)
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
		log.Println("voice: whisper server stopped")
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

	// Send request
	req := map[string]string{"audio_file": audioPath}
	reqBytes, _ := json.Marshal(req)
	reqBytes = append(reqBytes, '\n')

	if _, err := t.stdin.Write(reqBytes); err != nil {
		// Process died — mark as not ready
		t.ready = false
		return nil, fmt.Errorf("write to whisper process: %w", err)
	}

	// Read response with timeout
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
		// Process might be stuck — kill and restart
		t.ready = false
		t.cmd.Process.Kill()
		return nil, fmt.Errorf("transcription timeout after %v", t.timeout)
	}
}

// DownloadAndTranscribe downloads a voice file from Telegram, transcribes it, cleans up.
func (t *Transcriber) DownloadAndTranscribe(client *http.Client, botToken, fileID string) (*TranscribeResult, error) {
	// Get file path from Telegram
	filePath, err := telegramGetFilePath(client, botToken, fileID)
	if err != nil {
		return nil, fmt.Errorf("get file path: %w", err)
	}

	// Download to temp file
	tmpFile, err := downloadTelegramFile(client, botToken, filePath)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer os.Remove(tmpFile)

	log.Printf("voice: downloaded %s → %s", fileID, tmpFile)

	result, err := t.Transcribe(tmpFile)
	if err != nil {
		return nil, err
	}

	log.Printf("voice: transcribed in %.1fs → %q (%s, %.0f%%)",
		result.DurationS, truncate(result.Text, 80), result.Language, result.LanguageProbability*100)

	return result, nil
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

// telegramGetFilePath calls Telegram's getFile API.
func telegramGetFilePath(client *http.Client, botToken, fileID string) (string, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", botToken, fileID)
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if !result.OK || result.Result.FilePath == "" {
		return "", fmt.Errorf("telegram getFile failed for %s", fileID)
	}
	return result.Result.FilePath, nil
}

// downloadTelegramFile downloads and saves to a temp file. Caller must clean up.
func downloadTelegramFile(client *http.Client, botToken, filePath string) (string, error) {
	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", botToken, filePath)
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ext := filepath.Ext(filePath)
	if ext == "" {
		ext = ".ogg"
	}

	tmpFile, err := os.CreateTemp("", "alf-voice-*"+ext)
	if err != nil {
		return "", err
	}

	if _, err := tmpFile.ReadFrom(resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", err
	}
	tmpFile.Close()
	return tmpFile.Name(), nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
