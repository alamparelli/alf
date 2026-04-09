package controlcenter

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
		respondError(w, http.StatusBadRequest, "file too large or invalid multipart")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "missing file field")
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
		respondError(w, http.StatusInternalServerError, "upload failed")
		return
	}

	respondJSON(w, http.StatusOK, result)
}

func (h *ChatMediaHandler) serveMedia(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/chat/media/")
	if id == "" {
		respondError(w, http.StatusBadRequest, "missing media id")
		return
	}

	// Fast path: in-memory upload registry.
	entry := h.Service.GetUpload(id)
	if entry != nil {
		serveLocalFile(w, r, entry.TempPath, entry.MimeType, entry.FileName, entry.CreatedAt)
		return
	}

	// Fallback: persisted media ref in database.
	if h.Service.ChatDB != nil {
		ref, err := h.Service.ChatDB.GetMediaByUploadID(id)
		if err == nil && ref != nil && ref.FilePath != "" {
			serveLocalFile(w, r, ref.FilePath, ref.MimeType, ref.FileName, time.Time{})
			return
		}
	}

	respondError(w, http.StatusNotFound, "not found")
}

func serveLocalFile(w http.ResponseWriter, r *http.Request, path, mimeType, fileName string, modTime time.Time) {
	f, err := os.Open(path)
	if err != nil {
		respondError(w, http.StatusNotFound, "file not found")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename=%q`, filepath.Base(fileName)))
	w.Header().Set("Cache-Control", "private, max-age=604800, immutable")
	http.ServeContent(w, r, fileName, modTime, f)
}
