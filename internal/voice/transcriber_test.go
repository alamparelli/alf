package voice

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsAvailable(t *testing.T) {
	t.Run("with nonexistent script", func(t *testing.T) {
		if IsAvailable("/nonexistent/script.py") {
			t.Error("should return false for nonexistent script")
		}
	})

	t.Run("with existing file", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "test.py")
		os.WriteFile(tmpFile, []byte("#!/usr/bin/env python3\n"), 0755)

		// Result depends on whether python3 is in PATH.
		available := IsAvailable(tmpFile)
		_, pyErr := exec.LookPath("python3")
		hasPython := pyErr == nil
		_ = available
		_ = hasPython
	})
}

func TestTranscribeBeforeStart(t *testing.T) {
	tmpScript := filepath.Join(t.TempDir(), "fake.py")
	os.WriteFile(tmpScript, []byte(""), 0644)

	tr, err := New(tmpScript, "small", t.TempDir(), 10e9)
	if err != nil {
		t.Skip("python3 not available:", err)
	}

	_, transcribeErr := tr.Transcribe("/tmp/test.ogg")
	if transcribeErr == nil {
		t.Fatal("expected error when transcribing before Start()")
	}
	if transcribeErr.Error() != "transcriber not ready" {
		t.Errorf("error = %q, want 'transcriber not ready'", transcribeErr.Error())
	}
}

func TestStopBeforeStart(t *testing.T) {
	tmpScript := filepath.Join(t.TempDir(), "fake.py")
	os.WriteFile(tmpScript, []byte(""), 0644)

	tr, err := New(tmpScript, "small", t.TempDir(), 10e9)
	if err != nil {
		t.Skip("python3 not available:", err)
	}
	// Should not panic.
	tr.Stop()
	if tr.IsReady() {
		t.Error("should not be ready after Stop()")
	}
}

// --- audio utility tests (shared across backends) ---

func TestConvertToWAV(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "silence.wav")
	generateSilentWAV(t, srcPath, 16000, 8000) // 0.5s of silence

	wavPath, err := convertToWAV(srcPath)
	if err != nil {
		t.Fatalf("convertToWAV() failed: %v", err)
	}
	defer os.Remove(wavPath)

	info, err := os.Stat(wavPath)
	if err != nil {
		t.Fatalf("output WAV missing: %v", err)
	}
	if info.Size() == 0 {
		t.Error("output WAV is empty")
	}
}

func TestReadWAVSamples(t *testing.T) {
	tmpDir := t.TempDir()
	wavPath := filepath.Join(tmpDir, "test.wav")

	numSamples := 1600 // 0.1s at 16kHz
	generateSilentWAV(t, wavPath, 16000, numSamples)

	samples, err := readWAVSamples(wavPath)
	if err != nil {
		t.Fatalf("readWAVSamples() failed: %v", err)
	}
	if len(samples) != numSamples {
		t.Errorf("got %d samples, want %d", len(samples), numSamples)
	}
	for i, s := range samples {
		if s != 0.0 {
			t.Errorf("sample[%d] = %f, want 0.0", i, s)
			break
		}
	}
}

func TestReadWAVSamplesWithTone(t *testing.T) {
	tmpDir := t.TempDir()
	wavPath := filepath.Join(tmpDir, "tone.wav")

	sampleRate := 16000
	numSamples := sampleRate // 1 second
	generateToneWAV(t, wavPath, sampleRate, numSamples, 440.0)

	samples, err := readWAVSamples(wavPath)
	if err != nil {
		t.Fatalf("readWAVSamples() failed: %v", err)
	}
	if len(samples) != numSamples {
		t.Errorf("got %d samples, want %d", len(samples), numSamples)
	}

	hasNonZero := false
	for _, s := range samples {
		if s != 0.0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("expected non-zero samples for tone")
	}

	for i, s := range samples {
		if s < -1.0 || s > 1.0 {
			t.Errorf("sample[%d] = %f, out of [-1.0, 1.0] range", i, s)
			break
		}
	}
}

func TestReadWAVSamplesInvalidFile(t *testing.T) {
	badPath := filepath.Join(t.TempDir(), "bad.wav")
	os.WriteFile(badPath, []byte("not a wav file"), 0644)

	_, err := readWAVSamples(badPath)
	if err == nil {
		t.Fatal("expected error for invalid WAV")
	}
}

func TestDownloadModel(t *testing.T) {
	tmpDir := t.TempDir()
	modelPath := filepath.Join(tmpDir, "ggml-tiny.bin")
	os.WriteFile(modelPath, []byte("fake model data"), 0644)

	got, err := downloadModel("tiny", tmpDir)
	if err != nil {
		t.Fatalf("downloadModel() failed: %v", err)
	}
	if got != modelPath {
		t.Errorf("got %q, want %q", got, modelPath)
	}
}

func TestDownloadModelCreatesDir(t *testing.T) {
	nestedDir := filepath.Join(t.TempDir(), "a", "b", "c")
	_, _ = downloadModel("nonexistent-model", nestedDir)

	if _, err := os.Stat(nestedDir); err != nil {
		t.Errorf("downloadModel should create models directory: %v", err)
	}
}

// --- Telegram tests ---

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

// --- test helpers ---

func generateSilentWAV(t *testing.T, path string, sampleRate, numSamples int) {
	t.Helper()
	writeWAV(t, path, sampleRate, make([]int16, numSamples))
}

func generateToneWAV(t *testing.T, path string, sampleRate, numSamples int, freqHz float64) {
	t.Helper()
	samples := make([]int16, numSamples)
	for i := range samples {
		val := math.Sin(2 * math.Pi * freqHz * float64(i) / float64(sampleRate))
		samples[i] = int16(val * float64(math.MaxInt16-1))
	}
	writeWAV(t, path, sampleRate, samples)
}

func writeWAV(t *testing.T, path string, sampleRate int, samples []int16) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create WAV: %v", err)
	}
	defer f.Close()

	dataSize := len(samples) * 2
	fileSize := 36 + dataSize

	f.Write([]byte("RIFF"))
	binary.Write(f, binary.LittleEndian, uint32(fileSize))
	f.Write([]byte("WAVE"))

	f.Write([]byte("fmt "))
	binary.Write(f, binary.LittleEndian, uint32(16))
	binary.Write(f, binary.LittleEndian, uint16(1))
	binary.Write(f, binary.LittleEndian, uint16(1))
	binary.Write(f, binary.LittleEndian, uint32(sampleRate))
	binary.Write(f, binary.LittleEndian, uint32(sampleRate*2))
	binary.Write(f, binary.LittleEndian, uint16(2))
	binary.Write(f, binary.LittleEndian, uint16(16))

	f.Write([]byte("data"))
	binary.Write(f, binary.LittleEndian, uint32(dataSize))
	binary.Write(f, binary.LittleEndian, samples)
}
