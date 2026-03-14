package voice

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func newMockWhisperService(t *testing.T, secret string) *httptest.Server {
	t.Helper()
	var currentToken atomic.Value
	currentToken.Store("")

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "ready"})
	})

	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			InstanceID string `json:"instance_id"`
			Secret     string `json:"secret"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Secret != secret {
			http.Error(w, `{"detail":"Invalid secret"}`, 401)
			return
		}
		token := "test-token-" + req.InstanceID
		currentToken.Store(token)
		json.NewEncoder(w).Encode(map[string]string{
			"token":       token,
			"instance_id": req.InstanceID,
		})
	})

	mux.HandleFunc("/transcribe", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		tok := currentToken.Load().(string)
		if auth != "Bearer "+tok || tok == "" {
			http.Error(w, `{"detail":"Invalid token"}`, 401)
			return
		}

		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, `{"detail":"No file"}`, 400)
			return
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if len(data) == 0 {
			http.Error(w, `{"detail":"Empty file"}`, 400)
			return
		}

		json.NewEncoder(w).Encode(TranscribeResult{
			Text:                "hello world",
			DurationS:           1.5,
			Language:            "en",
			LanguageProbability: 0.95,
		})
	})

	return httptest.NewServer(mux)
}

func TestRegisterAndTranscribe(t *testing.T) {
	srv := newMockWhisperService(t, "test-secret")
	defer srv.Close()

	tr, err := New(srv.URL, "test-instance", "test-secret", 10e9)
	if err != nil {
		t.Fatal(err)
	}

	if err := tr.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if !tr.IsReady() {
		t.Fatal("expected ready after Start()")
	}

	// Create a temp audio file.
	audioPath := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(audioPath, []byte("fake audio data"), 0644)

	result, err := tr.Transcribe(audioPath)
	if err != nil {
		t.Fatalf("Transcribe() failed: %v", err)
	}
	if result.Text != "hello world" {
		t.Errorf("text = %q, want %q", result.Text, "hello world")
	}
	if result.Language != "en" {
		t.Errorf("language = %q, want %q", result.Language, "en")
	}
}

func TestRegisterBadSecret(t *testing.T) {
	srv := newMockWhisperService(t, "correct-secret")
	defer srv.Close()

	tr, err := New(srv.URL, "test-instance", "wrong-secret", 10e9)
	if err != nil {
		t.Fatal(err)
	}

	err = tr.Start()
	if err == nil {
		t.Fatal("expected error with wrong secret")
	}
}

func TestTranscribeBeforeStart(t *testing.T) {
	tr, err := New("http://localhost:99999", "test", "secret", 10e9)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tr.Transcribe("/tmp/test.ogg")
	if err == nil {
		t.Fatal("expected error when transcribing before Start()")
	}
	if err.Error() != "transcriber not ready" {
		t.Errorf("error = %q, want %q", err.Error(), "transcriber not ready")
	}
}

func TestTranscribe401ReRegister(t *testing.T) {
	// Create a server where token gets invalidated mid-session.
	var tokenValid atomic.Bool
	tokenValid.Store(true)

	mux := http.NewServeMux()
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			InstanceID string `json:"instance_id"`
			Secret     string `json:"secret"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		tokenValid.Store(true)
		json.NewEncoder(w).Encode(map[string]string{
			"token":       "valid-token",
			"instance_id": req.InstanceID,
		})
	})

	var transcribeCalls atomic.Int32
	mux.HandleFunc("/transcribe", func(w http.ResponseWriter, r *http.Request) {
		call := transcribeCalls.Add(1)
		if call == 1 {
			// First call: reject with 401
			http.Error(w, `{"detail":"Invalid token"}`, 401)
			return
		}
		// Second call after re-registration: succeed
		json.NewEncoder(w).Encode(TranscribeResult{
			Text:      "re-registered",
			DurationS: 0.5,
			Language:  "en",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	tr, err := New(srv.URL, "test", "secret", 10e9)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Start(); err != nil {
		t.Fatal(err)
	}

	audioPath := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(audioPath, []byte("audio"), 0644)

	result, err := tr.Transcribe(audioPath)
	if err != nil {
		t.Fatalf("expected re-registration to succeed: %v", err)
	}
	if result.Text != "re-registered" {
		t.Errorf("text = %q, want %q", result.Text, "re-registered")
	}
	if transcribeCalls.Load() != 2 {
		t.Errorf("expected 2 transcribe calls, got %d", transcribeCalls.Load())
	}
}

func TestTranscribeServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"token":       "tok",
			"instance_id": "test",
		})
	})
	mux.HandleFunc("/transcribe", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", 500)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	tr, err := New(srv.URL, "test", "secret", 10e9)
	if err != nil {
		t.Fatal(err)
	}
	tr.Start()

	audioPath := filepath.Join(t.TempDir(), "test.ogg")
	os.WriteFile(audioPath, []byte("audio"), 0644)

	_, err = tr.Transcribe(audioPath)
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestIsAvailable(t *testing.T) {
	srv := newMockWhisperService(t, "secret")
	defer srv.Close()

	if !IsAvailable(srv.URL) {
		t.Error("expected available for running mock server")
	}
	if IsAvailable("http://localhost:1") {
		t.Error("expected unavailable for bad address")
	}
}

func TestNewValidation(t *testing.T) {
	_, err := New("", "id", "secret", 10e9)
	if err == nil {
		t.Error("expected error for empty URL")
	}
	_, err = New("http://localhost", "id", "", 10e9)
	if err == nil {
		t.Error("expected error for empty secret")
	}
}
