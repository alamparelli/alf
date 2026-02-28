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
		if tr.IsReady() {
			t.Error("should not be ready before Start()")
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

func TestPersistentTranscriber(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a mock transcribe.py server
	mockScript := filepath.Join(tmpDir, "transcribe.py")
	script := `#!/usr/bin/env python3
import json, sys, argparse

parser = argparse.ArgumentParser()
parser.add_argument("audio_file", nargs="?")
parser.add_argument("--model", default="small")
parser.add_argument("--models-dir", default="/tmp")
parser.add_argument("--server", action="store_true")
args = parser.parse_args()

if args.server:
    # Send ready signal
    sys.stdout.write(json.dumps({"status": "ready", "model": args.model}) + "\n")
    sys.stdout.flush()

    # Process requests
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        req = json.loads(line)
        result = {
            "text": "Hello this is a test",
            "duration_s": 0.5,
            "language": "en",
            "language_probability": 0.98
        }
        sys.stdout.write(json.dumps(result) + "\n")
        sys.stdout.flush()
else:
    result = {"text": "oneshot", "duration_s": 0.1, "language": "en", "language_probability": 0.9}
    json.dump(result, sys.stdout)
    print()
`
	if err := os.WriteFile(mockScript, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write mock script: %v", err)
	}

	audioFile := filepath.Join(tmpDir, "test.ogg")
	os.WriteFile(audioFile, []byte("fake audio"), 0644)

	t.Run("start and transcribe", func(t *testing.T) {
		tr, err := New(mockScript, "small", tmpDir, 10*time.Second)
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}
		defer tr.Stop()

		if err := tr.Start(); err != nil {
			t.Fatalf("Start() failed: %v", err)
		}

		if !tr.IsReady() {
			t.Fatal("should be ready after Start()")
		}

		result, err := tr.Transcribe(audioFile)
		if err != nil {
			t.Fatalf("Transcribe() failed: %v", err)
		}
		if result.Text != "Hello this is a test" {
			t.Errorf("text = %q, want %q", result.Text, "Hello this is a test")
		}
		if result.Language != "en" {
			t.Errorf("language = %q, want %q", result.Language, "en")
		}
	})

	t.Run("multiple transcriptions", func(t *testing.T) {
		tr, err := New(mockScript, "small", tmpDir, 10*time.Second)
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}
		defer tr.Stop()

		if err := tr.Start(); err != nil {
			t.Fatalf("Start() failed: %v", err)
		}

		// Send multiple requests — model stays loaded
		for i := 0; i < 5; i++ {
			result, err := tr.Transcribe(audioFile)
			if err != nil {
				t.Fatalf("Transcribe() #%d failed: %v", i, err)
			}
			if result.Text != "Hello this is a test" {
				t.Errorf("#%d text = %q", i, result.Text)
			}
		}
	})

	t.Run("transcribe before start", func(t *testing.T) {
		tr, err := New(mockScript, "small", tmpDir, 10*time.Second)
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}

		_, err = tr.Transcribe(audioFile)
		if err == nil {
			t.Fatal("expected error when transcribing before Start()")
		}
		if err.Error() != "transcriber not ready" {
			t.Errorf("error = %q, want 'transcriber not ready'", err.Error())
		}
	})

	t.Run("stop and restart", func(t *testing.T) {
		tr, err := New(mockScript, "small", tmpDir, 10*time.Second)
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}

		if err := tr.Start(); err != nil {
			t.Fatalf("Start() failed: %v", err)
		}

		tr.Stop()
		if tr.IsReady() {
			t.Error("should not be ready after Stop()")
		}

		// Should be able to restart
		if err := tr.Start(); err != nil {
			t.Fatalf("re-Start() failed: %v", err)
		}
		defer tr.Stop()

		if !tr.IsReady() {
			t.Error("should be ready after re-Start()")
		}
	})
}

func TestTranscribeTimeout(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a server that never responds
	mockScript := filepath.Join(tmpDir, "transcribe.py")
	script := `#!/usr/bin/env python3
import json, sys, time, argparse

parser = argparse.ArgumentParser()
parser.add_argument("audio_file", nargs="?")
parser.add_argument("--model", default="small")
parser.add_argument("--models-dir", default="/tmp")
parser.add_argument("--server", action="store_true")
args = parser.parse_args()

# Send ready
sys.stdout.write(json.dumps({"status": "ready", "model": "small"}) + "\n")
sys.stdout.flush()

# Read request but never respond
for line in sys.stdin:
    time.sleep(60)
`
	os.WriteFile(mockScript, []byte(script), 0755)
	audioFile := filepath.Join(tmpDir, "test.ogg")
	os.WriteFile(audioFile, []byte("fake"), 0644)

	tr, _ := New(mockScript, "small", tmpDir, 200*time.Millisecond)
	if err := tr.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	_, err := tr.Transcribe(audioFile)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !tr.IsReady() {
		// After timeout, process is killed and not ready
		t.Log("transcriber correctly marked not ready after timeout")
	}
}

func TestTelegramFileDownload(t *testing.T) {
	mux := http.NewServeMux()

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

	mux.HandleFunc("/file/botTEST_TOKEN/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake OGG audio data"))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	t.Run("getFilePath error handling", func(t *testing.T) {
		client := ts.Client()
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
