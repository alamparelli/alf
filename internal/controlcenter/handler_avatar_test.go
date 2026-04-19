package controlcenter

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Regression lock for the avatar handler. Covers the full state
// lifecycle: upload, serve, reset, and the programmatic helpers
// used by the native tool.

func newTestAvatarHandler(t *testing.T) (*AvatarHandler, string) {
	t.Helper()
	dir := t.TempDir()
	return &AvatarHandler{
		DataDir:     dir,
		EventBroker: NewEventBroker(),
	}, dir
}

// tinyPNG builds a valid 1×1 PNG encoded as base64, the smallest
// legitimate image the PUT path can accept.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode tiny png: %v", err)
	}
	return buf.Bytes()
}

// ----- avatarPath ------------------------------------------------------

func TestAvatar_PathDerivedFromDataDir(t *testing.T) {
	h := &AvatarHandler{DataDir: "/opt/alf"}
	if got, want := h.avatarPath(), "/opt/alf/config/avatar.png"; got != want {
		t.Errorf("avatarPath = %q, want %q", got, want)
	}
}

// ----- GET serves default then custom ---------------------------------

func TestAvatar_GET_DefaultWhenMissing(t *testing.T) {
	h, _ := newTestAvatarHandler(t)

	req := httptest.NewRequest("GET", "/api/settings/avatar", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET default: expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	// Hardening headers must be present.
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff header missing")
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("CSP missing")
	}
	if w.Body.Len() == 0 {
		t.Error("default avatar body empty")
	}
}

func TestAvatar_GET_ServesCustomAfterPut(t *testing.T) {
	h, dir := newTestAvatarHandler(t)

	imgBytes := tinyPNG(t)
	if err := h.SetFromBytes(imgBytes); err != nil {
		t.Fatalf("SetFromBytes: %v", err)
	}

	// Confirm the file lives where avatarPath says.
	if _, err := os.Stat(filepath.Join(dir, "config", "avatar.png")); err != nil {
		t.Fatalf("avatar file not written: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/settings/avatar", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET custom: expected 200, got %d", w.Code)
	}
}

// ----- PUT validates + sanitizes --------------------------------------

func TestAvatar_PUT_HappyPath(t *testing.T) {
	h, dir := newTestAvatarHandler(t)

	imgBytes := tinyPNG(t)
	body := `{"image":"` + base64.StdEncoding.EncodeToString(imgBytes) + `"}`

	req := httptest.NewRequest(http.MethodPut, "/api/settings/avatar", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "config", "avatar.png")); err != nil {
		t.Errorf("avatar file missing after PUT: %v", err)
	}
	if !h.HasCustomAvatar() {
		t.Error("HasCustomAvatar should be true after PUT")
	}
}

func TestAvatar_PUT_RejectsInvalidBase64(t *testing.T) {
	h, _ := newTestAvatarHandler(t)
	body := `{"image":"!!!not-base64!!!"}`

	req := httptest.NewRequest(http.MethodPut, "/api/settings/avatar", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on bad base64, got %d", w.Code)
	}
}

func TestAvatar_PUT_RejectsEmptyImage(t *testing.T) {
	h, _ := newTestAvatarHandler(t)
	req := httptest.NewRequest(http.MethodPut, "/api/settings/avatar", strings.NewReader(`{"image":""}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on empty image, got %d", w.Code)
	}
}

func TestAvatar_PUT_RejectsMalformedJSON(t *testing.T) {
	h, _ := newTestAvatarHandler(t)
	req := httptest.NewRequest(http.MethodPut, "/api/settings/avatar", strings.NewReader("{not json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on malformed JSON, got %d", w.Code)
	}
}

func TestAvatar_PUT_RejectsOversizedPayload(t *testing.T) {
	h, _ := newTestAvatarHandler(t)
	// Build an oversized raw body (larger than avatarMaxInputBytes).
	big := bytes.Repeat([]byte("A"), avatarMaxInputBytes+100)
	body := `{"image":"` + string(big) + `"}`

	req := httptest.NewRequest(http.MethodPut, "/api/settings/avatar", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 on oversized input, got %d", w.Code)
	}
}

func TestAvatar_PUT_RejectsNonImageBytes(t *testing.T) {
	h, _ := newTestAvatarHandler(t)
	// Valid base64, but decodes to garbage that is not a valid image.
	body := `{"image":"` + base64.StdEncoding.EncodeToString([]byte("not an image")) + `"}`

	req := httptest.NewRequest(http.MethodPut, "/api/settings/avatar", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on non-image, got %d: %s", w.Code, w.Body.String())
	}
}

// ----- DELETE resets to default ---------------------------------------

func TestAvatar_DELETE_ReturnsToDefault(t *testing.T) {
	h, dir := newTestAvatarHandler(t)

	// Seed a custom avatar first.
	if err := h.SetFromBytes(tinyPNG(t)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !h.HasCustomAvatar() {
		t.Fatal("precondition: custom avatar should be set")
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/settings/avatar", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on DELETE, got %d", w.Code)
	}
	if h.HasCustomAvatar() {
		t.Error("custom avatar still present after DELETE")
	}
	// Next GET must serve default without error (file is gone).
	if _, err := os.Stat(filepath.Join(dir, "config", "avatar.png")); !os.IsNotExist(err) {
		t.Errorf("avatar file not removed: err=%v", err)
	}

	// GET after delete must serve the embedded default.
	gr := httptest.NewRequest("GET", "/api/settings/avatar", nil)
	gw := httptest.NewRecorder()
	h.ServeHTTP(gw, gr)
	if gw.Code != http.StatusOK || gw.Body.Len() == 0 {
		t.Errorf("GET after DELETE: code=%d body_len=%d", gw.Code, gw.Body.Len())
	}
}

// ----- Method not allowed ---------------------------------------------

func TestAvatar_MethodNotAllowed(t *testing.T) {
	h, _ := newTestAvatarHandler(t)
	for _, m := range []string{http.MethodPost, http.MethodPatch} {
		req := httptest.NewRequest(m, "/api/settings/avatar", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", m, w.Code)
		}
	}
}

// ----- Programmatic helpers -------------------------------------------

func TestAvatar_SetFromBytes_RejectsOversize(t *testing.T) {
	h, _ := newTestAvatarHandler(t)
	big := bytes.Repeat([]byte("A"), avatarMaxInputBytes+1)
	if err := h.SetFromBytes(big); err == nil {
		t.Error("SetFromBytes should reject oversize input")
	}
}

func TestAvatar_SetFromBytes_RejectsNonImage(t *testing.T) {
	h, _ := newTestAvatarHandler(t)
	if err := h.SetFromBytes([]byte("not an image")); err == nil {
		t.Error("SetFromBytes should reject non-image bytes")
	}
}

func TestAvatar_Reset(t *testing.T) {
	h, _ := newTestAvatarHandler(t)
	h.SetFromBytes(tinyPNG(t))
	if !h.HasCustomAvatar() {
		t.Fatal("seed failed")
	}
	h.Reset()
	if h.HasCustomAvatar() {
		t.Error("Reset did not remove the custom avatar")
	}
}

func TestAvatar_HasCustomAvatar_FalseInitially(t *testing.T) {
	h, _ := newTestAvatarHandler(t)
	if h.HasCustomAvatar() {
		t.Error("fresh handler should not report custom avatar")
	}
}

// ----- ModelCache -----------------------------------------------------
// Isolated state tests (no registry → refreshAll is a no-op).

func TestModelCache_SeedsCliModels(t *testing.T) {
	mc := NewModelCache(nil, 24*365*time.Hour) // long interval — don't tick
	defer mc.Stop()

	cli := mc.Get("cli")
	if len(cli) == 0 {
		t.Fatal("cli models not seeded")
	}
	ids := map[string]bool{}
	for _, m := range cli {
		ids[m.ID] = true
	}
	for _, want := range []string{"haiku", "sonnet", "opus"} {
		if !ids[want] {
			t.Errorf("cli seed missing %q: %+v", want, cli)
		}
	}
}

func TestModelCache_All_ReturnsCopy(t *testing.T) {
	mc := NewModelCache(nil, 24*365*time.Hour)
	defer mc.Stop()

	got := mc.All()
	if _, ok := got["cli"]; !ok {
		t.Fatal("All() missing cli seed")
	}

	// Mutate the returned map — internal state must not change.
	delete(got, "cli")
	second := mc.All()
	if _, ok := second["cli"]; !ok {
		t.Error("All() leaks internal map — caller delete bled into state")
	}
}

func TestModelCache_Get_UnknownReturnsNil(t *testing.T) {
	mc := NewModelCache(nil, 24*365*time.Hour)
	defer mc.Stop()

	if got := mc.Get("openrouter"); got != nil {
		t.Errorf("unknown backend should return nil, got %+v", got)
	}
}

func TestModelCache_RefreshBackend_NilRegistryNoOp(t *testing.T) {
	mc := NewModelCache(nil, 24*365*time.Hour)
	defer mc.Stop()

	mc.RefreshBackend("anything") // must not panic
	if got := mc.Get("anything"); got != nil {
		t.Errorf("refresh with nil registry should leave cache empty: %+v", got)
	}
}

// ----- ChainHandler ---------------------------------------------------

func TestChainHandler_MethodNotAllowed(t *testing.T) {
	h := &ChainHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/chain", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET chain: got %d", w.Code)
	}
}

func TestChainHandler_RejectsSingleStep(t *testing.T) {
	h := &ChainHandler{}
	body, _ := json.Marshal(map[string]any{
		"steps": []map[string]string{{"tier": "sonnet", "prompt": "hi"}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/chain", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on single-step chain, got %d: %s", w.Code, w.Body.String())
	}
}

func TestChainHandler_MalformedJSON(t *testing.T) {
	h := &ChainHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/chain", strings.NewReader("{not json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on malformed JSON, got %d", w.Code)
	}
}
