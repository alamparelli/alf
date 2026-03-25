package controlcenter

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ChatMediaHandler handles POST /api/chat/upload and GET /api/chat/media/:id.
type ChatMediaHandler struct {
	Service *ChatService
}

func (h *ChatMediaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Route: /api/chat/media/<id> → serve file
	// Route: /api/chat/upload → upload file
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/chat/media/") {
		h.serveMedia(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	h.upload(w, r)
}

func (h *ChatMediaHandler) upload(w http.ResponseWriter, r *http.Request) {
	// 50MB max.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyUpload)
	if err := r.ParseMultipartForm(50 * 1024 * 1024); err != nil {
		http.Error(w, `{"error":"file too large or invalid multipart"}`, http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"missing file field"}`, http.StatusBadRequest)
		return
	}
	defer file.Close()

	mediaType := r.FormValue("type")
	if mediaType == "" {
		mediaType = "document" // default
	}

	fileName := header.Filename
	result, err := h.Service.Upload(file, fileName, mediaType)
	if err != nil {
		log.Printf("[chat-api] upload error: %v", err)
		http.Error(w, `{"error":"upload failed"}`, http.StatusInternalServerError)
		return
	}

	respondJSON(w, http.StatusOK, result)
}

func (h *ChatMediaHandler) serveMedia(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/chat/media/")
	if id == "" {
		http.Error(w, `{"error":"missing media id"}`, http.StatusBadRequest)
		return
	}

	entry := h.Service.GetUpload(id)
	if entry == nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	f, err := os.Open(entry.TempPath)
	if err != nil {
		http.Error(w, `{"error":"file not found"}`, http.StatusNotFound)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", entry.MimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename=%q`, filepath.Base(entry.FileName)))
	http.ServeContent(w, r, entry.FileName, entry.CreatedAt, f)
}
