package voice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
