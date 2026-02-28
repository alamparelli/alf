package voice

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Transcriber transcribes audio files using the whisper CLI
type Transcriber struct {
	executable string // path to whisper executable
	timeout    time.Duration
}

// New creates a new Transcriber
// If whisper is not found in PATH, returns an error
func New(timeout time.Duration) (*Transcriber, error) {
	path, err := exec.LookPath("whisper")
	if err != nil {
		return nil, fmt.Errorf("whisper not found in PATH: %w", err)
	}
	return &Transcriber{
		executable: path,
		timeout:    timeout,
	}, nil
}

// Transcribe transcribes an audio file and returns the text
func (t *Transcriber) Transcribe(audioPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	// Run: whisper --output-format txt --quiet {audioPath}
	cmd := exec.CommandContext(ctx, t.executable, "--output-format", "txt", "--quiet", audioPath)

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("whisper transcription timeout")
		}
		return "", fmt.Errorf("whisper error: %w", err)
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		return "", fmt.Errorf("empty transcription")
	}

	return result, nil
}

// IsAvailable checks if whisper is available in the system
func IsAvailable() bool {
	_, err := exec.LookPath("whisper")
	return err == nil
}

// TranscribeFile is a convenience function that creates a Transcriber and transcribes
func TranscribeFile(audioPath string, timeout time.Duration) (string, error) {
	t, err := New(timeout)
	if err != nil {
		return "", err
	}
	return t.Transcribe(audioPath)
}

// DownloadAndTranscribe downloads an audio file from Telegram and transcribes it
// Returns the transcription text or an error
func DownloadAndTranscribe(client interface{}, botToken, fileID string, timeout time.Duration) (string, error) {
	// This would need the Telegram client implementation
	// Placeholder for now
	return "", fmt.Errorf("not implemented")
}
