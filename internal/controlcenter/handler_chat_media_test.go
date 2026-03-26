package controlcenter

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/chatdb"
)

func TestChatMediaHandler_UploadPhoto(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatMediaHandler{Service: svc}

	// Create multipart body.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "test.jpg")
	fw.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0}) // JPEG magic bytes
	w.WriteField("type", "photo")
	w.Close()

	req := httptest.NewRequest("POST", "/api/chat/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result UploadResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("JSON decode: %v", err)
	}
	if result.UploadID == "" {
		t.Error("expected non-empty upload_id")
	}
	if result.FileName != "test.jpg" {
		t.Errorf("expected file_name %q, got %q", "test.jpg", result.FileName)
	}
	if result.MimeType != "image/jpeg" {
		t.Errorf("expected mime_type image/jpeg, got %q", result.MimeType)
	}

	// Verify entry is registered.
	entry := svc.GetUpload(result.UploadID)
	if entry == nil {
		t.Fatal("upload entry not registered in service")
	}
	// Cleanup.
	os.Remove(entry.TempPath)
}

func TestChatMediaHandler_UploadMissingFile(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatMediaHandler{Service: svc}

	req := httptest.NewRequest("POST", "/api/chat/upload", nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestChatMediaHandler_ServeMedia(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatMediaHandler{Service: svc}

	// Upload first.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "hello.txt")
	fw.Write([]byte("hello world"))
	w.WriteField("type", "document")
	w.Close()

	uploadReq := httptest.NewRequest("POST", "/api/chat/upload", &buf)
	uploadReq.Header.Set("Content-Type", w.FormDataContentType())
	uploadRec := httptest.NewRecorder()
	h.ServeHTTP(uploadRec, uploadReq)

	var result UploadResult
	json.Unmarshal(uploadRec.Body.Bytes(), &result)

	// Serve it back.
	serveReq := httptest.NewRequest("GET", "/api/chat/media/"+result.UploadID, nil)
	serveRec := httptest.NewRecorder()
	h.ServeHTTP(serveRec, serveReq)

	if serveRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", serveRec.Code)
	}
	body, _ := io.ReadAll(serveRec.Body)
	if string(body) != "hello world" {
		t.Errorf("expected %q, got %q", "hello world", string(body))
	}

	// Cleanup.
	entry := svc.GetUpload(result.UploadID)
	if entry != nil {
		os.Remove(entry.TempPath)
	}
}

func TestChatMediaHandler_ServeMediaNotFound(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatMediaHandler{Service: svc}

	req := httptest.NewRequest("GET", "/api/chat/media/nonexistent", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestChatMediaHandler_MethodNotAllowed(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatMediaHandler{Service: svc}

	req := httptest.NewRequest("DELETE", "/api/chat/upload", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestChatMediaHandler_FilenameSanitized(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatMediaHandler{Service: svc}

	// Path traversal filename (without newlines since those break multipart).
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "../../etc/passwd")
	fw.Write([]byte("payload"))
	w.WriteField("type", "document")
	w.Close()

	req := httptest.NewRequest("POST", "/api/chat/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result UploadResult
	json.Unmarshal(rec.Body.Bytes(), &result)

	entry := svc.GetUpload(result.UploadID)
	if entry == nil {
		t.Fatal("upload entry not found")
	}
	// filepath.Base strips path components: "../../etc/passwd" → "passwd"
	if entry.FileName != "passwd" {
		t.Errorf("expected sanitized filename %q, got %q", "passwd", entry.FileName)
	}
	// Must not contain path separators.
	for _, c := range entry.FileName {
		if c == '/' || c == '\\' {
			t.Errorf("filename contains path separator: %q", entry.FileName)
			break
		}
	}
	os.Remove(entry.TempPath)
}

func TestChatMediaHandler_InvalidMediaType(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatMediaHandler{Service: svc}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "test.txt")
	fw.Write([]byte("content"))
	w.WriteField("type", "malicious_type")
	w.Close()

	req := httptest.NewRequest("POST", "/api/chat/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result UploadResult
	json.Unmarshal(rec.Body.Bytes(), &result)

	entry := svc.GetUpload(result.UploadID)
	if entry == nil {
		t.Fatal("upload entry not found")
	}
	// Invalid type should be normalized to "document".
	if entry.MediaType != "document" {
		t.Errorf("expected mediaType 'document', got %q", entry.MediaType)
	}
	os.Remove(entry.TempPath)
}

func TestServeMedia_InMemoryHit(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatMediaHandler{Service: svc}

	// Upload a file via the handler so it lands in the in-memory registry.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "inmem.txt")
	fw.Write([]byte("in-memory content"))
	w.WriteField("type", "document")
	w.Close()

	uploadReq := httptest.NewRequest("POST", "/api/chat/upload", &buf)
	uploadReq.Header.Set("Content-Type", w.FormDataContentType())
	uploadRec := httptest.NewRecorder()
	h.ServeHTTP(uploadRec, uploadReq)

	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", uploadRec.Code, uploadRec.Body.String())
	}

	var result UploadResult
	json.Unmarshal(uploadRec.Body.Bytes(), &result)

	// Request the media by its upload ID — should hit in-memory path.
	req := httptest.NewRequest("GET", "/api/chat/media/"+result.UploadID, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("serve: expected 200, got %d", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "in-memory content" {
		t.Errorf("body = %q, want %q", string(body), "in-memory content")
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "text/plain; charset=utf-8" && ct != "text/plain" {
		t.Errorf("content-type = %q, want text/plain", ct)
	}

	// Cleanup.
	if entry := svc.GetUpload(result.UploadID); entry != nil {
		os.Remove(entry.TempPath)
	}
}

func TestServeMedia_DBFallback(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatMediaHandler{Service: svc}

	// Create a temp file on disk that the DB ref will point to.
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "persisted.png")
	fileContent := []byte("fake-png-bytes")
	if err := os.WriteFile(filePath, fileContent, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	// Insert a media ref directly into the DB (not in the in-memory registry).
	uploadID := "db-only-media-123"
	svc.ChatDB.EnsureConversation("c1", "test", "cc")
	svc.ChatDB.InsertMessage(chatdb.Message{
		ID: "m1", ConvID: "c1", Role: "user", Text: "see attachment",
	})
	if err := svc.ChatDB.InsertMediaRef(chatdb.MediaRef{
		UploadID:  uploadID,
		FileName:  "persisted.png",
		MimeType:  "image/png",
		MediaType: "photo",
		FilePath:  filePath,
	}, "m1", "c1"); err != nil {
		t.Fatalf("InsertMediaRef: %v", err)
	}

	// Verify the in-memory registry does NOT have it.
	if svc.GetUpload(uploadID) != nil {
		t.Fatal("upload should not be in memory")
	}

	// Request it — should fall back to DB.
	req := httptest.NewRequest("GET", "/api/chat/media/"+uploadID, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != string(fileContent) {
		t.Errorf("body = %q, want %q", string(body), string(fileContent))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want %q", ct, "image/png")
	}
}

func TestDeleteConversation_CleansUpExpiredMedia(t *testing.T) {
	svc := newTestChatService(t)

	// Create temp files simulating uploaded media.
	tmpDir := t.TempDir()
	oldFile := filepath.Join(tmpDir, "old.jpg")
	newFile := filepath.Join(tmpDir, "new.jpg")
	os.WriteFile(oldFile, []byte("old"), 0o644)
	os.WriteFile(newFile, []byte("new"), 0o644)

	// Set up conversation with two media refs.
	svc.ChatDB.EnsureConversation("c1", "test", "cc")
	svc.ChatDB.InsertMessage(chatdb.Message{ID: "m1", ConvID: "c1", Role: "user", Text: "old"})
	svc.ChatDB.InsertMessage(chatdb.Message{ID: "m2", ConvID: "c1", Role: "user", Text: "new"})

	// Old media (10 days ago).
	svc.ChatDB.InsertMediaRef(chatdb.MediaRef{
		UploadID: "old-1", FileName: "old.jpg", MimeType: "image/jpeg", MediaType: "photo", FilePath: oldFile,
	}, "m1", "c1")
	// Backdate created_at.
	svc.ChatDB.Exec("UPDATE media SET created_at = ? WHERE upload_id = ?",
		time.Now().AddDate(0, 0, -10), "old-1")

	// Recent media (today).
	svc.ChatDB.InsertMediaRef(chatdb.MediaRef{
		UploadID: "new-1", FileName: "new.jpg", MimeType: "image/jpeg", MediaType: "photo", FilePath: newFile,
	}, "m2", "c1")

	// Config with 7-day retention.
	cfgStore := &mockConfigStore{cfg: &Config{MediaRetentionDays: 7}}
	h := &ChatConversationHandler{Service: svc, ConfigStore: cfgStore}

	req := httptest.NewRequest("DELETE", "/api/chat/conversations/c1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Old file should be deleted from disk.
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old media file should have been deleted")
	}
	// Old media ref should be deleted from DB.
	ref, _ := svc.ChatDB.GetMediaByUploadID("old-1")
	if ref != nil {
		t.Error("old media ref should have been deleted from DB")
	}

	// New file should still exist.
	if _, err := os.Stat(newFile); err != nil {
		t.Error("new media file should still exist")
	}
	// New media ref should still exist.
	ref, _ = svc.ChatDB.GetMediaByUploadID("new-1")
	if ref == nil {
		t.Error("new media ref should still exist in DB")
	}
}

func TestDeleteConversation_DefaultRetention(t *testing.T) {
	svc := newTestChatService(t)

	tmpDir := t.TempDir()
	oldFile := filepath.Join(tmpDir, "old.jpg")
	os.WriteFile(oldFile, []byte("old"), 0o644)

	svc.ChatDB.EnsureConversation("c1", "test", "cc")
	svc.ChatDB.InsertMessage(chatdb.Message{ID: "m1", ConvID: "c1", Role: "user", Text: "old"})
	svc.ChatDB.InsertMediaRef(chatdb.MediaRef{
		UploadID: "old-1", FileName: "old.jpg", MimeType: "image/jpeg", MediaType: "photo", FilePath: oldFile,
	}, "m1", "c1")
	svc.ChatDB.Exec("UPDATE media SET created_at = ? WHERE upload_id = ?",
		time.Now().AddDate(0, 0, -10), "old-1")

	// nil ConfigStore → default 7 days.
	h := &ChatConversationHandler{Service: svc, ConfigStore: nil}

	req := httptest.NewRequest("DELETE", "/api/chat/conversations/c1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Old file should be deleted (default 7 days, media is 10 days old).
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old media file should have been deleted with default retention")
	}
}

func TestServeMedia_NotFound(t *testing.T) {
	svc := newTestChatService(t)
	h := &ChatMediaHandler{Service: svc}

	// Neither in-memory nor DB has this ID.
	req := httptest.NewRequest("GET", "/api/chat/media/totally-absent-id", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}
