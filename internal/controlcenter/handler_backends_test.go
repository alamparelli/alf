package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alamparelli/alf/internal/provider"
)

func TestBackendsModelsHandler_CLIBackend(t *testing.T) {
	h := &BackendsModelsHandler{}
	req := httptest.NewRequest("GET", "/api/backends/cli/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Backend string      `json:"backend"`
		Models  []modelInfo `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Backend != "cli" {
		t.Errorf("expected backend=cli, got %q", resp.Backend)
	}
	if len(resp.Models) != 3 {
		t.Fatalf("expected 3 CLI models, got %d", len(resp.Models))
	}
	ids := map[string]bool{}
	for _, m := range resp.Models {
		ids[m.ID] = true
	}
	for _, want := range []string{"haiku", "sonnet", "opus"} {
		if !ids[want] {
			t.Errorf("missing CLI model: %s", want)
		}
	}
}

func TestBackendsModelsHandler_NotFound(t *testing.T) {
	reg := provider.NewRegistry(nil)
	h := &BackendsModelsHandler{Registry: reg}
	req := httptest.NewRequest("GET", "/api/backends/nonexistent/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestBackendsModelsHandler_BadPath(t *testing.T) {
	h := &BackendsModelsHandler{}
	tests := []string{
		"/api/backends//models",
		"/api/backends/",
		"/api/backends/foo/bar",
	}
	for _, path := range tests {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Errorf("path %q: expected 400, got %d", path, w.Code)
		}
	}
}

func TestBackendsModelsHandler_MethodNotAllowed(t *testing.T) {
	h := &BackendsModelsHandler{}
	req := httptest.NewRequest("POST", "/api/backends/cli/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestBackendsModelsHandler_NilRegistry(t *testing.T) {
	h := &BackendsModelsHandler{Registry: nil}
	req := httptest.NewRequest("GET", "/api/backends/openrouter/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 500 {
		t.Errorf("expected 500 for nil registry, got %d", w.Code)
	}
}

func TestBackendsModelsHandler_FetchFromMockOllama(t *testing.T) {
	// Mock Ollama server returning /api/tags
	ollamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{"name": "llama3.2:latest", "model": "llama3.2:latest"},
					{"name": "qwen2.5:32b", "model": "qwen2.5:32b"},
					{"name": "nomic-embed-text", "model": "nomic-embed-text"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ollamaServer.Close()

	reg := provider.NewRegistry(nil)
	prov := provider.NewAPIProviderFromConfig(provider.APIProviderConfig{
		Name:    "ollama",
		BaseURL: ollamaServer.URL + "/v1", // Ollama uses /v1 as base
		Auth:    "none",
	}, nil)
	reg.Register("ollama", prov)

	h := &BackendsModelsHandler{Registry: reg}
	req := httptest.NewRequest("GET", "/api/backends/ollama/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Backend string      `json:"backend"`
		Models  []modelInfo `json:"models"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Backend != "ollama" {
		t.Errorf("expected backend=ollama, got %q", resp.Backend)
	}
	if len(resp.Models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(resp.Models))
	}
	if resp.Models[0].ID != "llama3.2:latest" {
		t.Errorf("expected first model llama3.2:latest, got %q", resp.Models[0].ID)
	}
}

func TestBackendsModelsHandler_FetchFromMockOpenAI(t *testing.T) {
	// Mock OpenAI-compatible server returning /v1/models
	openaiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "gpt-4o", "object": "model", "owned_by": "openai"},
					{"id": "gpt-4o-mini", "object": "model", "owned_by": "openai"},
					{"id": "gpt-3.5-turbo", "object": "model", "owned_by": "openai"},
				},
			})
			return
		}
		// Ollama /api/tags should 404 (not an Ollama server)
		http.NotFound(w, r)
	}))
	defer openaiServer.Close()

	reg := provider.NewRegistry(nil)
	prov := provider.NewAPIProviderFromConfig(provider.APIProviderConfig{
		Name:    "chatgpt",
		BaseURL: openaiServer.URL + "/v1",
		APIKey:  "sk-test",
		Auth:    "bearer",
	}, nil)
	reg.Register("chatgpt", prov)

	h := &BackendsModelsHandler{Registry: reg}
	req := httptest.NewRequest("GET", "/api/backends/chatgpt/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Backend string      `json:"backend"`
		Models  []modelInfo `json:"models"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if len(resp.Models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(resp.Models))
	}
	if resp.Models[0].ID != "gpt-4o" {
		t.Errorf("expected gpt-4o, got %q", resp.Models[0].ID)
	}
}

func TestBackendsModelsHandler_FetchFromMockOpenRouter(t *testing.T) {
	// Mock OpenRouter server returning /api/v1/models (note: same path as /v1/models under base)
	orServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/models" {
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "anthropic/claude-haiku-4-5", "name": "Claude Haiku 4.5"},
					{"id": "google/gemini-2.0-flash", "name": "Gemini 2.0 Flash"},
					{"id": "openai/gpt-4o", "name": "GPT-4o"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer orServer.Close()

	reg := provider.NewRegistry(nil)
	prov := provider.NewAPIProviderFromConfig(provider.APIProviderConfig{
		Name:    "openrouter",
		BaseURL: orServer.URL + "/api/v1",
		APIKey:  "or-test-key",
		Auth:    "bearer",
	}, nil)
	reg.Register("openrouter", prov)

	h := &BackendsModelsHandler{Registry: reg}
	req := httptest.NewRequest("GET", "/api/backends/openrouter/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Backend string      `json:"backend"`
		Models  []modelInfo `json:"models"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if len(resp.Models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(resp.Models))
	}
	// OpenRouter models have both id and name
	if resp.Models[0].ID != "anthropic/claude-haiku-4-5" {
		t.Errorf("expected anthropic/claude-haiku-4-5, got %q", resp.Models[0].ID)
	}
	if resp.Models[0].Name != "Claude Haiku 4.5" {
		t.Errorf("expected name 'Claude Haiku 4.5', got %q", resp.Models[0].Name)
	}
}

func TestBackendsModelsHandler_AuthHeaderSent(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "test-model"}},
		})
	}))
	defer server.Close()

	reg := provider.NewRegistry(nil)
	prov := provider.NewAPIProviderFromConfig(provider.APIProviderConfig{
		Name:    "test",
		BaseURL: server.URL,
		APIKey:  "sk-secret-123",
		Auth:    "bearer",
	}, nil)
	reg.Register("test", prov)

	h := &BackendsModelsHandler{Registry: reg}
	req := httptest.NewRequest("GET", "/api/backends/test/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if gotAuth != "Bearer sk-secret-123" {
		t.Errorf("expected auth header 'Bearer sk-secret-123', got %q", gotAuth)
	}
}

func TestBackendsModelsHandler_NoAuthForOllama(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		// Return ollama format
		if r.URL.Path == "/api/tags" {
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{{"name": "test"}},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	reg := provider.NewRegistry(nil)
	prov := provider.NewAPIProviderFromConfig(provider.APIProviderConfig{
		Name:    "ollama",
		BaseURL: server.URL + "/v1",
		Auth:    "none",
	}, nil)
	reg.Register("ollama", prov)

	h := &BackendsModelsHandler{Registry: reg}
	req := httptest.NewRequest("GET", "/api/backends/ollama/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if gotAuth != "" {
		t.Errorf("expected no auth header for ollama, got %q", gotAuth)
	}
}

func TestBackendsModelsHandler_BackendDown(t *testing.T) {
	reg := provider.NewRegistry(nil)
	prov := provider.NewAPIProviderFromConfig(provider.APIProviderConfig{
		Name:    "dead",
		BaseURL: "http://127.0.0.1:1", // will fail to connect
		Auth:    "none",
	}, nil)
	reg.Register("dead", prov)

	h := &BackendsModelsHandler{Registry: reg}
	req := httptest.NewRequest("GET", "/api/backends/dead/models", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 502 {
		t.Errorf("expected 502 for dead backend, got %d", w.Code)
	}
}

// --- Unit tests for model parsers ---

func TestParseOllamaModels(t *testing.T) {
	data := []byte(`{"models":[{"name":"llama3.2:latest","model":"llama3.2:latest"},{"name":"qwen2.5:32b"}]}`)
	models := parseOllamaModels(data)
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "llama3.2:latest" {
		t.Errorf("expected llama3.2:latest, got %q", models[0].ID)
	}
	if models[1].ID != "qwen2.5:32b" {
		t.Errorf("expected qwen2.5:32b, got %q", models[1].ID)
	}
}

func TestParseOllamaModels_Empty(t *testing.T) {
	models := parseOllamaModels([]byte(`{"models":[]}`))
	if models != nil {
		t.Errorf("expected nil for empty models, got %v", models)
	}
}

func TestParseOllamaModels_InvalidJSON(t *testing.T) {
	models := parseOllamaModels([]byte(`not json`))
	if models != nil {
		t.Errorf("expected nil for invalid JSON, got %v", models)
	}
}

func TestParseOpenAIModels(t *testing.T) {
	data := []byte(`{"object":"list","data":[{"id":"gpt-4o","object":"model"},{"id":"gpt-4o-mini","object":"model","name":"GPT-4o Mini"}]}`)
	models := parseOpenAIModels(data)
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "gpt-4o" {
		t.Errorf("expected gpt-4o, got %q", models[0].ID)
	}
	if models[0].Name != "" {
		t.Errorf("expected empty name for OpenAI, got %q", models[0].Name)
	}
	if models[1].Name != "GPT-4o Mini" {
		t.Errorf("expected name 'GPT-4o Mini', got %q", models[1].Name)
	}
}

func TestParseOpenAIModels_Empty(t *testing.T) {
	models := parseOpenAIModels([]byte(`{"data":[]}`))
	if models != nil {
		t.Errorf("expected nil for empty data, got %v", models)
	}
}

func TestParseOpenAIModels_SkipsEmptyID(t *testing.T) {
	data := []byte(`{"data":[{"id":""},{"id":"valid"}]}`)
	models := parseOpenAIModels(data)
	if len(models) != 1 {
		t.Fatalf("expected 1 model (skip empty), got %d", len(models))
	}
	if models[0].ID != "valid" {
		t.Errorf("expected 'valid', got %q", models[0].ID)
	}
}

func TestParseOllamaModels_FallbackToModelField(t *testing.T) {
	// When "name" is empty, use "model" field
	data := []byte(`{"models":[{"name":"","model":"fallback-model"}]}`)
	models := parseOllamaModels(data)
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "fallback-model" {
		t.Errorf("expected fallback-model, got %q", models[0].ID)
	}
}
