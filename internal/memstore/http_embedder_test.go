package memstore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// mockEmbedServer creates a test HTTP server that mimics the embed-server protocol.
func mockEmbedServer(t *testing.T, secret string, dims int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	tokens := map[string]bool{}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/register":
			var req struct {
				InstanceID string `json:"instance_id"`
				Secret     string `json:"secret"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.Secret != secret {
				http.Error(w, "bad secret", http.StatusUnauthorized)
				return
			}
			token := "test-token-" + req.InstanceID
			mu.Lock()
			tokens[token] = true
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{
				"token": token,
				"dims":  dims,
			})

		case "/embed", "/embed-query":
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			mu.Lock()
			valid := tokens[token]
			mu.Unlock()
			if !valid {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var req struct {
				Text string `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			vec := make([]float32, dims)
			for i := range vec {
				vec[i] = float32(i) * 0.01
			}
			json.NewEncoder(w).Encode(map[string]any{"embedding": vec})

		case "/embed-batch":
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			mu.Lock()
			valid := tokens[token]
			mu.Unlock()
			if !valid {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var req struct {
				Texts []string `json:"texts"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if len(req.Texts) > 100 {
				http.Error(w, "batch too large", http.StatusBadRequest)
				return
			}
			vecs := make([][]float32, len(req.Texts))
			for i := range vecs {
				vec := make([]float32, dims)
				for j := range vec {
					vec[j] = float32(i+j) * 0.01
				}
				vecs[i] = vec
			}
			json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs})

		case "/health":
			json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"dims":   dims,
				"model":  "test",
			})

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func TestHTTPEmbedderRegisterAndEmbed(t *testing.T) {
	srv := mockEmbedServer(t, "test-secret", 384)
	defer srv.Close()

	emb := NewHTTPEmbedder(srv.URL, "test-instance", "test-secret", 5e9)
	if err := emb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !emb.IsReady() {
		t.Fatal("expected ready after Start")
	}
	if emb.Dims() != 384 {
		t.Fatalf("expected dims=384, got %d", emb.Dims())
	}

	vec, err := emb.Embed("hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 384 {
		t.Fatalf("expected 384 floats, got %d", len(vec))
	}
}

func TestHTTPEmbedderEmbedQuery(t *testing.T) {
	srv := mockEmbedServer(t, "test-secret", 384)
	defer srv.Close()

	emb := NewHTTPEmbedder(srv.URL, "test-instance", "test-secret", 5e9)
	emb.Start()

	vec, err := emb.EmbedQuery("search query")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if len(vec) != 384 {
		t.Fatalf("expected 384, got %d", len(vec))
	}
}

func TestHTTPEmbedderBatch(t *testing.T) {
	srv := mockEmbedServer(t, "test-secret", 384)
	defer srv.Close()

	emb := NewHTTPEmbedder(srv.URL, "test-instance", "test-secret", 5e9)
	emb.Start()

	vecs, err := emb.EmbedBatch([]string{"one", "two", "three"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("expected 3 embeddings, got %d", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 384 {
			t.Fatalf("embedding %d: expected 384 dims, got %d", i, len(v))
		}
	}
}

func TestHTTPEmbedderBatchEmpty(t *testing.T) {
	srv := mockEmbedServer(t, "test-secret", 384)
	defer srv.Close()

	emb := NewHTTPEmbedder(srv.URL, "test-instance", "test-secret", 5e9)
	emb.Start()

	vecs, err := emb.EmbedBatch([]string{})
	if err != nil {
		t.Fatalf("EmbedBatch empty: %v", err)
	}
	if len(vecs) != 0 {
		t.Fatalf("expected 0 embeddings, got %d", len(vecs))
	}
}

func TestHTTPEmbedderBadSecret(t *testing.T) {
	srv := mockEmbedServer(t, "correct-secret", 384)
	defer srv.Close()

	emb := NewHTTPEmbedder(srv.URL, "test-instance", "wrong-secret", 5e9)
	if err := emb.Start(); err == nil {
		t.Fatal("expected error with wrong secret")
	}

	if emb.IsReady() {
		t.Fatal("should not be ready with bad secret")
	}
}

func TestHTTPEmbedderAutoReRegister(t *testing.T) {
	secret := "test-secret"
	dims := 384
	var mu sync.Mutex
	tokens := map[string]bool{}
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/register":
			var req struct {
				InstanceID string `json:"instance_id"`
				Secret     string `json:"secret"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.Secret != secret {
				http.Error(w, "bad", http.StatusUnauthorized)
				return
			}
			mu.Lock()
			callCount++
			// Invalidate old tokens on re-register
			tokens = map[string]bool{}
			token := "token-v" + string(rune('0'+callCount))
			tokens[token] = true
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"token": token, "dims": dims})

		case "/embed":
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			mu.Lock()
			valid := tokens[token]
			mu.Unlock()
			if !valid {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			vec := make([]float32, dims)
			json.NewEncoder(w).Encode(map[string]any{"embedding": vec})
		}
	}))
	defer srv.Close()

	emb := NewHTTPEmbedder(srv.URL, "test", secret, 5e9)
	emb.Start()

	// First call succeeds.
	_, err := emb.Embed("test")
	if err != nil {
		t.Fatalf("first Embed: %v", err)
	}

	// Simulate token invalidation by clearing server tokens.
	mu.Lock()
	tokens = map[string]bool{}
	mu.Unlock()

	// Second call should trigger auto re-register.
	_, err = emb.Embed("test again")
	if err != nil {
		t.Fatalf("Embed after re-register: %v", err)
	}

	mu.Lock()
	if callCount < 2 {
		t.Fatalf("expected at least 2 register calls, got %d", callCount)
	}
	mu.Unlock()
}

func TestHTTPEmbedderNotReady(t *testing.T) {
	emb := NewHTTPEmbedder("http://localhost:1", "test", "secret", 5e9)

	_, err := emb.Embed("test")
	if err == nil {
		t.Fatal("expected error when not ready")
	}

	_, err = emb.EmbedQuery("test")
	if err == nil {
		t.Fatal("expected error when not ready")
	}

	_, err = emb.EmbedBatch([]string{"test"})
	if err == nil {
		t.Fatal("expected error when not ready")
	}
}

func TestHTTPEmbedderServerDown(t *testing.T) {
	// Use a URL that won't connect.
	emb := NewHTTPEmbedder("http://127.0.0.1:1", "test", "secret", 1e9)
	err := emb.Start()
	if err == nil {
		t.Fatal("expected error when server down")
	}
}

func TestHTTPEmbedderStop(t *testing.T) {
	srv := mockEmbedServer(t, "test-secret", 384)
	defer srv.Close()

	emb := NewHTTPEmbedder(srv.URL, "test", "test-secret", 5e9)
	emb.Start()

	if !emb.IsReady() {
		t.Fatal("expected ready")
	}

	emb.Stop()

	if emb.IsReady() {
		t.Fatal("expected not ready after Stop")
	}
}

func TestHTTPEmbedderInterfaceCompliance(t *testing.T) {
	// Compile-time check that HTTPEmbedder satisfies EmbedderI.
	var _ EmbedderI = (*HTTPEmbedder)(nil)
}

func TestHTTPEmbedderIdempotentStart(t *testing.T) {
	srv := mockEmbedServer(t, "test-secret", 384)
	defer srv.Close()

	emb := NewHTTPEmbedder(srv.URL, "test", "test-secret", 5e9)
	if err := emb.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	// Second Start should be no-op.
	if err := emb.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
}
