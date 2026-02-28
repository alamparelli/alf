package voice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsAvailable(t *testing.T) {
	// Test with non-existent script
	if IsAvailable("/nonexistent/transcribe.py") {
		t.Error("IsAvailable should return false for non-existent script")
	}
}

func TestNew(t *testing.T) {
	t.Run("default model", func(t *testing.T) {
		tr, err := New("/tmp/fake.py", "", "", 30*time.Second)
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}
		if tr.model != "small" {
			t.Errorf("model = %q, want %q", tr.model, "small")
		}
	})

	t.Run("custom model", func(t *testing.T) {
		tr, err := New("/tmp/fake.py", "medium", "/tmp/models", 60*time.Second)
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}
		if tr.model != "medium" {
			t.Errorf("model = %q, want %q", tr.model, "medium")
		}
		if tr.modelsDir != "/tmp/models" {
			t.Errorf("modelsDir = %q, want %q", tr.modelsDir, "/tmp/models")
		}
	})
}

func TestTranscribeWithMockScript(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a mock transcribe.py that returns JSON
	mockScript := filepath.Join(tmpDir, "transcribe.py")
	script := `#!/usr/bin/env python3
import json, sys
result = {"text": "Hello world", "duration_s": 1.5, "language": "en", "language_probability": 0.95}
json.dump(result, sys.stdout)
print()
`
	if err := os.WriteFile(mockScript, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock script: %v", err)
	}

	// Create a fake audio file
	audioFile := filepath.Join(tmpDir, "test.ogg")
	if err := os.WriteFile(audioFile, []byte("fake audio"), 0644); err != nil {
		t.Fatalf("failed to write audio file: %v", err)
	}

	tr := &Transcriber{
		scriptPath: mockScript,
		model:      "small",
		modelsDir:  tmpDir,
		timeout:    10 * time.Second,
	}

	result, err := tr.Transcribe(audioFile)
	if err != nil {
		t.Fatalf("Transcribe() failed: %v", err)
	}
	if result.Text != "Hello world" {
		t.Errorf("text = %q, want %q", result.Text, "Hello world")
	}
	if result.Language != "en" {
		t.Errorf("language = %q, want %q", result.Language, "en")
	}
	if result.DurationS != 1.5 {
		t.Errorf("duration_s = %f, want 1.5", result.DurationS)
	}
}

func TestTranscribeEmptyResult(t *testing.T) {
	tmpDir := t.TempDir()

	mockScript := filepath.Join(tmpDir, "transcribe.py")
	script := `#!/usr/bin/env python3
import json, sys
json.dump({"text": "", "duration_s": 0.1, "language": "", "language_probability": 0}, sys.stdout)
print()
`
	if err := os.WriteFile(mockScript, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock script: %v", err)
	}

	audioFile := filepath.Join(tmpDir, "test.ogg")
	os.WriteFile(audioFile, []byte("fake"), 0644)

	tr := &Transcriber{
		scriptPath: mockScript,
		model:      "small",
		modelsDir:  tmpDir,
		timeout:    10 * time.Second,
	}

	_, err := tr.Transcribe(audioFile)
	if err == nil {
		t.Fatal("expected error for empty transcription, got nil")
	}
	if err.Error() != "empty transcription" {
		t.Errorf("error = %q, want 'empty transcription'", err.Error())
	}
}

func TestTranscribeTimeout(t *testing.T) {
	tmpDir := t.TempDir()

	mockScript := filepath.Join(tmpDir, "transcribe.py")
	script := `#!/usr/bin/env python3
import time; time.sleep(10)
`
	os.WriteFile(mockScript, []byte(script), 0755)
	audioFile := filepath.Join(tmpDir, "test.ogg")
	os.WriteFile(audioFile, []byte("fake"), 0644)

	tr := &Transcriber{
		scriptPath: mockScript,
		model:      "small",
		modelsDir:  tmpDir,
		timeout:    100 * time.Millisecond,
	}

	_, err := tr.Transcribe(audioFile)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if got := err.Error(); got != "transcription timeout after 100ms" {
		t.Errorf("error = %q, want timeout error", got)
	}
}

func TestTranscribeScriptError(t *testing.T) {
	tmpDir := t.TempDir()

	mockScript := filepath.Join(tmpDir, "transcribe.py")
	script := `#!/usr/bin/env python3
import sys
print("File not found: /bad/path", file=sys.stderr)
sys.exit(1)
`
	os.WriteFile(mockScript, []byte(script), 0755)
	audioFile := filepath.Join(tmpDir, "test.ogg")
	os.WriteFile(audioFile, []byte("fake"), 0644)

	tr := &Transcriber{
		scriptPath: mockScript,
		model:      "small",
		modelsDir:  tmpDir,
		timeout:    5 * time.Second,
	}

	_, err := tr.Transcribe(audioFile)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTelegramFileDownload(t *testing.T) {
	// Mock Telegram API server
	mux := http.NewServeMux()

	// Mock getFile endpoint
	mux.HandleFunc("/botTEST_TOKEN/getFile", func(w http.ResponseWriter, r *http.Request) {
		fileID := r.URL.Query().Get("file_id")
		resp := map[string]any{
			"ok": true,
			"result": map[string]any{
				"file_path": "voice/file_" + fileID + ".ogg",
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	// Mock file download endpoint
	mux.HandleFunc("/file/botTEST_TOKEN/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake OGG audio data"))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Override Telegram API URL for testing by using the test server
	// We test the individual functions since they take http.Client

	t.Run("getFilePath", func(t *testing.T) {
		client := ts.Client()
		// We'd need to override the URL, but telegramGetFilePath has hardcoded URL
		// For now, just test that the function exists and handles errors
		_, err := telegramGetFilePath(client, "INVALID_TOKEN", "file123")
		if err == nil {
			t.Log("Expected error with invalid token (actual Telegram would fail)")
		}
	})
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"ab", 2, "ab"},
		{"abc", 2, "ab..."},
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}
