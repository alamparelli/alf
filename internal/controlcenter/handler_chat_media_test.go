package controlcenter

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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
