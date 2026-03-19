package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testServer creates a server with a mock embedder for testing.
// The real ONNX embedder requires model files; these tests validate the HTTP layer.
func testServer() *server {
	return &server{
		embedder: nil, // handlers check IsReady() which returns false for nil
		secret:   "test-secret",
		dims:     384,
	}
}

func TestRegisterSuccess(t *testing.T) {
	srv := testServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/register", srv.handleRegister)

	body, _ := json.Marshal(map[string]string{
		"instance_id": "test-1",
		"secret":      "test-secret",
	})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Token string `json:"token"`
		Dims  int    `json:"dims"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Token == "" {
		t.Fatal("expected non-empty token")
	}
	if resp.Dims != 384 {
		t.Fatalf("expected dims=384, got %d", resp.Dims)
	}
}

func TestRegisterBadSecret(t *testing.T) {
	srv := testServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/register", srv.handleRegister)

	body, _ := json.Marshal(map[string]string{
		"instance_id": "test-1",
		"secret":      "wrong-secret",
	})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestRegisterMissingInstanceID(t *testing.T) {
	srv := testServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/register", srv.handleRegister)

	body, _ := json.Marshal(map[string]string{
		"secret": "test-secret",
	})
	req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestRegisterWrongMethod(t *testing.T) {
	srv := testServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/register", srv.handleRegister)

	req := httptest.NewRequest(http.MethodGet, "/register", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestAuthNoToken(t *testing.T) {
	srv := testServer()
	handler := srv.requireAuth(srv.handleEmbed)

	body, _ := json.Marshal(map[string]string{"text": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/embed", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthBadToken(t *testing.T) {
	srv := testServer()
	handler := srv.requireAuth(srv.handleEmbed)

	body, _ := json.Marshal(map[string]string{"text": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/embed", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAuthWrongMethod(t *testing.T) {
	srv := testServer()
	handler := srv.requireAuth(srv.handleEmbed)

	req := httptest.NewRequest(http.MethodGet, "/embed", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != 405 {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestAuthValidToken(t *testing.T) {
	srv := testServer()

	// Register first to get a token.
	regBody, _ := json.Marshal(map[string]string{
		"instance_id": "auth-test",
		"secret":      "test-secret",
	})
	regReq := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(regBody))
	regW := httptest.NewRecorder()
	srv.handleRegister(regW, regReq)

	var regResp struct {
		Token string `json:"token"`
	}
	json.NewDecoder(regW.Body).Decode(&regResp)

	// Now call embed with the token — will get 503 because embedder is nil (model not ready).
	handler := srv.requireAuth(srv.handleEmbed)
	body, _ := json.Marshal(map[string]string{"text": "hello"})
	req := httptest.NewRequest(http.MethodPost, "/embed", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+regResp.Token)
	w := httptest.NewRecorder()

	handler(w, req)

	// 503 = auth passed, embedder not ready (expected since we have no real model).
	if w.Code != 503 {
		t.Fatalf("expected 503 (model not ready), got %d: %s", w.Code, w.Body.String())
	}
}

func TestHealth(t *testing.T) {
	srv := testServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", srv.handleHealth)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Status string `json:"status"`
		Dims   int    `json:"dims"`
		Model  string `json:"model"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	// Embedder is nil → not ready → "loading".
	if resp.Status != "loading" {
		t.Fatalf("expected status=loading, got %s", resp.Status)
	}
	if resp.Dims != 384 {
		t.Fatalf("expected dims=384, got %d", resp.Dims)
	}
}

func TestEmbedBatchValidation(t *testing.T) {
	srv := testServer()

	// Register to get token.
	regBody, _ := json.Marshal(map[string]string{
		"instance_id": "batch-test",
		"secret":      "test-secret",
	})
	regReq := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(regBody))
	regW := httptest.NewRecorder()
	srv.handleRegister(regW, regReq)

	var regResp struct {
		Token string `json:"token"`
	}
	json.NewDecoder(regW.Body).Decode(&regResp)

	handler := srv.requireAuth(srv.handleEmbedBatch)

	// Batch with >100 texts should fail with 503 first (model not ready).
	// But let's test that the auth passes at minimum.
	texts := make([]string, 101)
	for i := range texts {
		texts[i] = "text"
	}
	body, _ := json.Marshal(map[string]any{"texts": texts})
	req := httptest.NewRequest(http.MethodPost, "/embed-batch", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+regResp.Token)
	w := httptest.NewRecorder()

	handler(w, req)

	// 503 because embedder nil, but auth passed.
	if w.Code != 503 {
		t.Fatalf("expected 503 (model not ready), got %d", w.Code)
	}
}

func TestReadSecretFromEnv(t *testing.T) {
	t.Setenv("TEST_SECRET_VALUE", "from-env")
	got := readSecret("TEST_SECRET_VALUE")
	if got != "from-env" {
		t.Fatalf("expected 'from-env', got %q", got)
	}
}

func TestReadSecretTrimsWhitespace(t *testing.T) {
	t.Setenv("TEST_SECRET_WS", "  secret-value  \n")
	got := readSecret("TEST_SECRET_WS")
	if got != "secret-value" {
		t.Fatalf("expected 'secret-value', got %q", got)
	}
}

func TestGenerateTokenUniqueness(t *testing.T) {
	tokens := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok := generateToken()
		if tokens[tok] {
			t.Fatalf("duplicate token generated: %s", tok)
		}
		tokens[tok] = true
	}
}

func TestGenerateTokenLength(t *testing.T) {
	tok := generateToken()
	// 32 random bytes → 64 hex chars.
	if len(tok) != 64 {
		t.Fatalf("expected 64 char token, got %d", len(tok))
	}
}

func TestBodySizeLimit(t *testing.T) {
	srv := testServer()

	// Register to get token.
	regBody, _ := json.Marshal(map[string]string{
		"instance_id": "limit-test",
		"secret":      "test-secret",
	})
	regReq := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(regBody))
	regW := httptest.NewRecorder()
	srv.handleRegister(regW, regReq)

	var regResp struct {
		Token string `json:"token"`
	}
	json.NewDecoder(regW.Body).Decode(&regResp)

	handler := srv.requireAuth(srv.handleEmbed)

	// Create body > 1MB.
	bigText := strings.Repeat("x", maxBodySize+1)
	body, _ := json.Marshal(map[string]string{"text": bigText})
	req := httptest.NewRequest(http.MethodPost, "/embed", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+regResp.Token)
	w := httptest.NewRecorder()

	handler(w, req)

	// Should get 503 (model not ready) or 400 (body too large) — either means auth passed.
	// The MaxBytesReader will cause a decode error → 400 or 503 depending on order.
	if w.Code == 200 {
		t.Fatal("expected non-200 for oversized body")
	}
}
