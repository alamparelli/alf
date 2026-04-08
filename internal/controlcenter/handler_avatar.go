package controlcenter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	avatarMaxInputBytes = 256 << 10 // 256KB raw input limit
	avatarSize          = 128       // output dimensions (square)
)

// AvatarHandler manages the LLM's profile avatar.
//
//	PUT    /api/settings/avatar — upload (JSON {"image":"<base64>"})
//	GET    /api/settings/avatar — serve sanitized PNG
//	DELETE /api/settings/avatar — reset to default
type AvatarHandler struct {
	DataDir string
}

func (h *AvatarHandler) avatarPath() string {
	return filepath.Join(h.DataDir, "config", "avatar.png")
}

func (h *AvatarHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r)
	case http.MethodPut:
		h.handlePut(w, r)
	case http.MethodDelete:
		h.handleDelete(w)
	default:
		methodNotAllowed(w)
	}
}

func (h *AvatarHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	path := h.avatarPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, path)
}

func (h *AvatarHandler) handlePut(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, avatarMaxInputBytes+1))
	if err != nil {
		respondError(w, http.StatusBadRequest, "read failed")
		return
	}
	if len(body) > avatarMaxInputBytes {
		respondError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("image too large (max %dKB)", avatarMaxInputBytes>>10))
		return
	}

	var req struct {
		Image string `json:"image"` // base64-encoded image data
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Image == "" {
		respondError(w, http.StatusBadRequest, "image field required")
		return
	}

	imgBytes, err := base64.StdEncoding.DecodeString(req.Image)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid base64")
		return
	}

	sanitized, err := sanitizeImage(imgBytes)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid image: "+err.Error())
		return
	}

	path := h.avatarPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, sanitized, 0o644); err != nil {
		respondError(w, http.StatusInternalServerError, "save failed")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AvatarHandler) handleDelete(w http.ResponseWriter) {
	os.Remove(h.avatarPath())
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// SetFromBytes sanitizes and saves an avatar image. Used by the native tool.
func (h *AvatarHandler) SetFromBytes(imgBytes []byte) error {
	if len(imgBytes) > avatarMaxInputBytes {
		return fmt.Errorf("image too large (max %dKB)", avatarMaxInputBytes>>10)
	}
	sanitized, err := sanitizeImage(imgBytes)
	if err != nil {
		return fmt.Errorf("invalid image: %w", err)
	}
	path := h.avatarPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	return os.WriteFile(path, sanitized, 0o644)
}

// Reset removes the custom avatar.
func (h *AvatarHandler) Reset() {
	os.Remove(h.avatarPath())
}

// HasCustomAvatar returns true if a custom avatar is set.
func (h *AvatarHandler) HasCustomAvatar() bool {
	_, err := os.Stat(h.avatarPath())
	return err == nil
}
