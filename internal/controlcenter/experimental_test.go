package controlcenter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithExperimentalHeader_AddsHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	wrapped := WithExperimentalHeader(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	wrapped.ServeHTTP(rec, req)

	got := rec.Header().Get("X-ALF-Experimental")
	if got != "no-isolation" {
		t.Fatalf("X-ALF-Experimental = %q, want %q", got, "no-isolation")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("inner response not preserved: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestWithExperimentalHeader_SetBeforeInnerWrites(t *testing.T) {
	// Header must be set before the inner handler calls WriteHeader, otherwise
	// Go's net/http silently drops late Set() calls. Regression guard.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	wrapped := WithExperimentalHeader(inner)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusTeapot {
		t.Errorf("inner status not preserved: got %d", rec.Code)
	}
	if rec.Header().Get("X-ALF-Experimental") != "no-isolation" {
		t.Error("middleware lost the header when inner wrote status explicitly")
	}
}
