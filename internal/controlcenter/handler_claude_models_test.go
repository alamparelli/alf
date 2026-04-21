package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeModelsHandler_ReturnsList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude_models.txt")
	if err := os.WriteFile(path, []byte("claude-opus-4-7\nclaude-sonnet-4-6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewFileClaudeModelsStore(path)
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	SetClaudeModelsStore(store)
	defer SetClaudeModelsStore(nil)

	req := httptest.NewRequest("GET", "/api/models/claude", nil)
	rec := httptest.NewRecorder()
	(&ClaudeModelsHandler{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Models []string `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Models) != 2 {
		t.Errorf("models = %v, want 2 entries", resp.Models)
	}
	if resp.Models[0] != "claude-opus-4-7" {
		t.Errorf("first model = %q, want claude-opus-4-7", resp.Models[0])
	}
}

func TestClaudeModelsHandler_EmptyWhenStoreUnset(t *testing.T) {
	SetClaudeModelsStore(nil)
	req := httptest.NewRequest("GET", "/api/models/claude", nil)
	rec := httptest.NewRecorder()
	(&ClaudeModelsHandler{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Models []string `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Models == nil {
		t.Error("models should be empty array, got nil (JSON would serialize as null)")
	}
	if len(resp.Models) != 0 {
		t.Errorf("models = %v, want empty", resp.Models)
	}
}

func TestClaudeModelsHandler_RejectsNonGET(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/models/claude", nil)
	rec := httptest.NewRecorder()
	(&ClaudeModelsHandler{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
