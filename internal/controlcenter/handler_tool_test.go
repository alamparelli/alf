package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockPermChecker struct {
	perms map[string]map[string]bool // slug → perm → allowed
}

func (m *mockPermChecker) HasPermission(slug, perm string) bool {
	if m.perms == nil {
		return false
	}
	return m.perms[slug][perm]
}

func (m *mockPermChecker) IsTracked(slug string) bool {
	_, ok := m.perms[slug]
	return ok
}

func TestToolHandler_MethodNotAllowed(t *testing.T) {
	h := &ToolHandler{DataDir: t.TempDir()}
	req := httptest.NewRequest("GET", "/api/tool", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestToolHandler_NoReferer(t *testing.T) {
	h := &ToolHandler{DataDir: t.TempDir()}
	req := httptest.NewRequest("POST", "/api/tool",
		strings.NewReader(`{"action":"list"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != "tool endpoint is app-only" {
		t.Errorf("unexpected error: %s", resp["error"])
	}
}

func TestToolHandler_NoPermission(t *testing.T) {
	perms := &mockPermChecker{perms: map[string]map[string]bool{
		"myapp": {"storage": true}, // has storage but not tool
	}}
	h := &ToolHandler{DataDir: t.TempDir(), Perms: perms}
	req := httptest.NewRequest("POST", "/api/tool",
		strings.NewReader(`{"action":"list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://cc.example.com/apps/myapp/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "permission denied: tool") {
		t.Errorf("unexpected error: %s", resp["error"])
	}
}

func TestToolHandler_MissingAction(t *testing.T) {
	h := &ToolHandler{DataDir: t.TempDir()}
	req := httptest.NewRequest("POST", "/api/tool",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://cc.example.com/apps/myapp/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestToolHandler_NoBinary(t *testing.T) {
	perms := &mockPermChecker{perms: map[string]map[string]bool{
		"myapp": {"tool": true},
	}}
	h := &ToolHandler{DataDir: t.TempDir(), Perms: perms}
	req := httptest.NewRequest("POST", "/api/tool",
		strings.NewReader(`{"action":"list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://cc.example.com/apps/myapp/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestToolHandler_SystemAppBlocked(t *testing.T) {
	h := &ToolHandler{DataDir: t.TempDir()}
	req := httptest.NewRequest("POST", "/api/tool",
		strings.NewReader(`{"action":"list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", "https://cc.example.com/apps/developer/index.html")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}
