package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// TranscribeResult holds the output from the Python transcription script.
type TranscribeResult struct {
	Text                string  `json:"text"`
	DurationS           float64 `json:"duration_s"`
	Language            string  `json:"language"`
	LanguageProbability float64 `json:"language_probability"`
}

// Transcriber transcribes audio files using faster-whisper via Python subprocess.
type Transcriber struct {
	scriptPath string // path to transcribe.py
	model      string // whisper model name (tiny, small, medium, large)
	modelsDir  string // directory to store/cache models
	timeout    time.Duration
}

// New creates a new Transcriber.
// scriptPath: path to the transcribe.py script
// model: whisper model name (default "small")
// modelsDir: directory for cached models
func New(scriptPath, model, modelsDir string, timeout time.Duration) (*Transcriber, error) {
	// Verify python3 is available
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		return nil, fmt.Errorf("python3 not found in PATH: %w", err)
	}
	_ = pythonPath

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

// Transcribe transcribes an audio file and returns the result.
func (t *Transcriber) Transcribe(audioPath string) (*TranscribeResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "python3", t.scriptPath,
		audioPath,
		"--model", t.model,
		"--models-dir", t.modelsDir,
	)

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("transcription timeout after %v", t.timeout)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("transcription failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("transcription error: %w", err)
	}

	var result TranscribeResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse transcription output: %w (raw: %s)", err, string(output))
	}

	if result.Text == "" {
		return nil, fmt.Errorf("empty transcription")
	}

	return &result, nil
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

// DownloadAndTranscribe downloads a voice file from Telegram, saves it
// to a temp file, transcribes it, then cleans up.
func (t *Transcriber) DownloadAndTranscribe(client *http.Client, botToken, fileID string) (*TranscribeResult, error) {
	// Step 1: Get file path from Telegram
	filePath, err := telegramGetFilePath(client, botToken, fileID)
	if err != nil {
		return nil, fmt.Errorf("get file path: %w", err)
	}

	// Step 2: Download file to temp dir
	tmpFile, err := downloadTelegramFile(client, botToken, filePath)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer os.Remove(tmpFile)

	log.Printf("voice: downloaded %s → %s", fileID, tmpFile)

	// Step 3: Transcribe
	result, err := t.Transcribe(tmpFile)
	if err != nil {
		return nil, err
	}

	log.Printf("voice: transcribed in %.1fs → %q (%s, %.0f%%)",
		result.DurationS, truncate(result.Text, 80), result.Language, result.LanguageProbability*100)

	return result, nil
}

// telegramGetFilePath calls Telegram's getFile API to get the download path.
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

// downloadTelegramFile downloads a file from Telegram and saves to a temp file.
// Returns the temp file path. Caller must clean up.
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
