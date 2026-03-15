package controlcenter

import (
	"encoding/json"
	"net/http"
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

// jsonErr returns a JSON-encoded error string for use with http.Error.
func jsonErr(msg string) string {
	data, _ := json.Marshal(map[string]string{"error": msg})
	return string(data)
}
