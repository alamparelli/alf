package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRespondError_ContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	respondError(rec, http.StatusBadRequest, "test error")

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestRespondError_StatusCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"bad request", http.StatusBadRequest},
		{"not found", http.StatusNotFound},
		{"internal error", http.StatusInternalServerError},
		{"unauthorized", http.StatusUnauthorized},
		{"forbidden", http.StatusForbidden},
		{"bad gateway", http.StatusBadGateway},
		{"service unavailable", http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			respondError(rec, tc.status, "msg")
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}

func TestRespondError_JSONBody(t *testing.T) {
	rec := httptest.NewRecorder()
	respondError(rec, http.StatusBadRequest, "something went wrong")

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["error"] != "something went wrong" {
		t.Errorf("error = %q, want %q", body["error"], "something went wrong")
	}
}

func TestRespondError_SpecialCharsEscaped(t *testing.T) {
	// This is the bug that handler_chat_react.go:31 had — quotes in error messages
	// would break manually constructed JSON. respondError handles this safely.
	rec := httptest.NewRecorder()
	respondError(rec, http.StatusBadRequest, `error with "quotes" and \backslash`)

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("JSON decode failed (special chars not escaped): %v", err)
	}
	if body["error"] != `error with "quotes" and \backslash` {
		t.Errorf("error = %q, want message with special chars preserved", body["error"])
	}
}

func TestRespondJSON_ContentType(t *testing.T) {
	rec := httptest.NewRecorder()
	respondJSON(rec, http.StatusOK, map[string]string{"ok": "true"})

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	methodNotAllowed(rec)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}

	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["error"] != "method not allowed" {
		t.Errorf("error = %q, want %q", body["error"], "method not allowed")
	}
}
