package controlcenter

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxUploadSize = 10 << 20 // 10MB

// safeFilename strips path separators and dangerous characters from a filename.
var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

func sanitizeFilename(name string) string {
	name = filepath.Base(name) // strip directory components
	name = unsafeChars.ReplaceAllString(name, "_")
	if name == "" || name == "." || name == ".." {
		name = "upload"
	}
	return name
}

// AppUploadHandler handles file uploads for apps.
//
//	POST /api/apps/{slug}/upload → saves file to DataDir/apps/{slug}/data/uploads/
type AppUploadHandler struct {
	DataDir string
}

func (h *AppUploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	// Parse slug from: /api/apps/{slug}/upload
	rest := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 || parts[1] != "upload" {
		http.NotFound(w, r)
		return
	}
	slug := parts[0]
	if !validName.MatchString(slug) {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid app name"})
		return
	}

	// Limit total request size
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)

	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "file too large (max 10MB)"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "missing 'file' field"})
		return
	}
	defer file.Close()

	safeName := sanitizeFilename(header.Filename)
	uploadsDir := filepath.Join(h.DataDir, "apps", slug, "data", "uploads")
	os.MkdirAll(uploadsDir, 0o775)

	destPath := filepath.Join(uploadsDir, safeName)

	// Verify destination stays within uploads dir (defense in depth)
	absUploads, _ := filepath.Abs(uploadsDir)
	absDest, _ := filepath.Abs(destPath)
	if !strings.HasPrefix(absDest, absUploads+string(filepath.Separator)) && absDest != absUploads {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid filename"})
		return
	}

	// SEC-P01: Prevent symlink following — remove existing symlink before creating file
	if info, err := os.Lstat(destPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			os.Remove(destPath)
		}
	}

	dst, err := os.Create(destPath)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "create file failed"})
		return
	}
	defer dst.Close()

	n, err := io.Copy(dst, file)
	if err != nil {
		os.Remove(destPath)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"path": "uploads/" + safeName,
		"name": safeName,
		"size": n,
	})
}
