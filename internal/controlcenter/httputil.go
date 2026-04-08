package controlcenter

import (
	"encoding/json"
	"net/http"
)

// Standard body size limits for MaxBytesReader.
const (
	maxBodySmall  = 4096       // 4 KB — simple JSON payloads (passwords, commands, tokens)
	maxBodyMedium = 16384      // 16 KB — secrets, structured payloads
	maxBodyLarge  = 1 << 20    // 1 MB — file content, LLM prompts, service configs
	maxBodyImport = 5 << 20    // 5 MB — vault import, multipart uploads
	maxBodyUpload = 50 << 20   // 50 MB — chat media uploads
)

// respondJSON writes a JSON response with the given status code.
func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// methodNotAllowed writes a standard 405 JSON error response.
func methodNotAllowed(w http.ResponseWriter) {
	respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
}

// respondError writes a JSON error response with the given status code.
// This is the standard way to return errors from API handlers.
func respondError(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, map[string]string{"error": msg})
}

// jsonErr returns a JSON-encoded error string for use with http.Error.
// Deprecated: use respondError instead.
func jsonErr(msg string) string {
	b, err := json.Marshal(msg)
	if err != nil {
		return `{"error":"internal error"}`
	}
	return `{"error":` + string(b) + `}`
}
