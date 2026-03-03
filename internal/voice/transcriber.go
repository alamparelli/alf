package voice

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

// TranscribeResult holds the output from the transcription.
type TranscribeResult struct {
	Text                string  `json:"text"`
	DurationS           float64 `json:"duration_s"`
	Language            string  `json:"language"`
	LanguageProbability float64 `json:"language_probability"`
	Error               string  `json:"error,omitempty"`
}

// downloadAndTranscribe is the shared implementation for DownloadAndTranscribe.
// Both backends delegate to this, passing their Transcribe method.
func downloadAndTranscribe(transcribeFn func(string) (*TranscribeResult, error), client *http.Client, botToken, fileID string) (*TranscribeResult, error) {
	filePath, err := telegramGetFilePath(client, botToken, fileID)
	if err != nil {
		return nil, fmt.Errorf("get file path: %w", err)
	}

	tmpFile, err := downloadTelegramFile(client, botToken, filePath)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer os.Remove(tmpFile)

	log.Printf("voice: downloaded %s → %s", fileID, tmpFile)

	result, err := transcribeFn(tmpFile)
	if err != nil {
		return nil, err
	}

	log.Printf("voice: transcribed in %.1fs → %q (%s, %.0f%%)",
		result.DurationS, truncate(result.Text, 80), result.Language, result.LanguageProbability*100)

	return result, nil
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
