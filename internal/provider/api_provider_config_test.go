package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewAPIProviderFromConfig_Defaults(t *testing.T) {
	p := NewAPIProviderFromConfig(APIProviderConfig{
		Name:    "test",
		BaseURL: "http://localhost/v1",
	}, nil)
	if p.name != "test" {
		t.Errorf("expected name 'test', got %q", p.name)
	}
	if p.auth != "bearer" {
		t.Errorf("expected auth 'bearer', got %q", p.auth)
	}
	if p.maxTokens != 4096 {
		t.Errorf("expected maxTokens 4096, got %d", p.maxTokens)
	}
}

func TestNewAPIProviderFromConfig_CustomValues(t *testing.T) {
	p := NewAPIProviderFromConfig(APIProviderConfig{
		Name:         "ollama",
		BaseURL:      "http://localhost:11434/v1",
		Auth:         "none",
		DefaultModel: "llama3.2",
		MaxTokens:    2048,
		Headers:      map[string]string{"X-Custom": "value"},
	}, nil)
	if p.auth != "none" {
		t.Errorf("expected auth 'none', got %q", p.auth)
	}
	if p.defaultModel != "llama3.2" {
		t.Errorf("expected defaultModel 'llama3.2', got %q", p.defaultModel)
	}
	if p.maxTokens != 2048 {
		t.Errorf("expected maxTokens 2048, got %d", p.maxTokens)
	}
	if p.headers["X-Custom"] != "value" {
		t.Error("expected custom header")
	}
}

func TestNewAPIProviderFromConfig_NoAuth(t *testing.T) {
	// Simulate Ollama-like backend: no auth header should be set.
	var authHeader string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := NewAPIProviderFromConfig(APIProviderConfig{
		Name:    "ollama",
		BaseURL: server.URL,
		Auth:    "none",
	}, nil)

	_, err := p.Invoke(context.Background(), "test", Params{Model: "llama3.2"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if authHeader != "" {
		t.Errorf("expected no Authorization header for auth=none, got %q", authHeader)
	}
}

func TestNewAPIProviderFromConfig_BearerAuth(t *testing.T) {
	var authHeader string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := NewAPIProviderFromConfig(APIProviderConfig{
		Name:    "openai",
		BaseURL: server.URL,
		APIKey:  "sk-test-key",
		Auth:    "bearer",
	}, nil)

	_, err := p.Invoke(context.Background(), "test", Params{Model: "gpt-4o"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if authHeader != "Bearer sk-test-key" {
		t.Errorf("expected 'Bearer sk-test-key', got %q", authHeader)
	}
}

func TestNewAPIProviderFromConfig_CustomHeaders(t *testing.T) {
	var referer, xTitle string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referer = r.Header.Get("HTTP-Referer")
		xTitle = r.Header.Get("X-Title")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := NewAPIProviderFromConfig(APIProviderConfig{
		Name:    "openrouter",
		BaseURL: server.URL,
		APIKey:  "key",
		Headers: map[string]string{"HTTP-Referer": "https://alf.dev", "X-Title": "ALF"},
	}, nil)

	_, err := p.Invoke(context.Background(), "test", Params{Model: "m"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if referer != "https://alf.dev" {
		t.Errorf("expected HTTP-Referer 'https://alf.dev', got %q", referer)
	}
	if xTitle != "ALF" {
		t.Errorf("expected X-Title 'ALF', got %q", xTitle)
	}
}

func TestNewAPIProviderFromConfig_DefaultModel(t *testing.T) {
	var capturedModel string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		capturedModel = req.Model
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := NewAPIProviderFromConfig(APIProviderConfig{
		Name:         "ollama",
		BaseURL:      server.URL,
		Auth:         "none",
		DefaultModel: "llama3.2",
	}, nil)

	// When no model in params, uses defaultModel.
	_, err := p.Invoke(context.Background(), "test", Params{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedModel != "llama3.2" {
		t.Errorf("expected model 'llama3.2', got %q", capturedModel)
	}
}

func TestNewAPIProviderFromConfig_MaxTokens(t *testing.T) {
	var capturedMaxTokens int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MaxTokens int `json:"max_tokens"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		capturedMaxTokens = req.MaxTokens
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := NewAPIProviderFromConfig(APIProviderConfig{
		Name:      "test",
		BaseURL:   server.URL,
		Auth:      "none",
		MaxTokens: 8192,
	}, nil)

	_, err := p.Invoke(context.Background(), "test", Params{Model: "m"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedMaxTokens != 8192 {
		t.Errorf("expected max_tokens 8192, got %d", capturedMaxTokens)
	}
}

// Regression: NewAPIProvider backward compat creates OpenRouter-compatible provider.
func TestNewAPIProvider_BackwardCompat(t *testing.T) {
	var authHeader, referer string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		referer = r.Header.Get("HTTP-Referer")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	history := NewHistory(t.TempDir(), 10, time.Hour)
	p := NewAPIProvider("sk-or-test", history)
	// Override baseURL for test.
	p.baseURL = server.URL

	_, err := p.Invoke(context.Background(), "test", Params{Model: "anthropic/claude-haiku-4-5"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if authHeader != "Bearer sk-or-test" {
		t.Errorf("expected bearer auth, got %q", authHeader)
	}
	if referer == "" {
		t.Error("expected HTTP-Referer header from OpenRouter compat")
	}
}

func TestAPIProviderName(t *testing.T) {
	p := NewAPIProviderFromConfig(APIProviderConfig{Name: "groq"}, nil)
	if p.Name() != "groq" {
		t.Errorf("expected 'groq', got %q", p.Name())
	}
}

func TestAPIProviderAccessors(t *testing.T) {
	p := NewAPIProviderFromConfig(APIProviderConfig{
		Name:    "openrouter",
		BaseURL: "https://openrouter.ai/api/v1",
		APIKey:  "sk-or-test",
		Auth:    "bearer",
		Headers: map[string]string{"HTTP-Referer": "https://alf.dev", "X-Title": "ALF"},
	}, nil)

	if p.BaseURL() != "https://openrouter.ai/api/v1" {
		t.Errorf("BaseURL: expected 'https://openrouter.ai/api/v1', got %q", p.BaseURL())
	}
	if p.APIKey() != "sk-or-test" {
		t.Errorf("APIKey: expected 'sk-or-test', got %q", p.APIKey())
	}
	if p.Auth() != "bearer" {
		t.Errorf("Auth: expected 'bearer', got %q", p.Auth())
	}
	if p.Headers()["HTTP-Referer"] != "https://alf.dev" {
		t.Errorf("Headers: expected HTTP-Referer 'https://alf.dev', got %q", p.Headers()["HTTP-Referer"])
	}
	if p.Headers()["X-Title"] != "ALF" {
		t.Errorf("Headers: expected X-Title 'ALF', got %q", p.Headers()["X-Title"])
	}
}

func TestAPIProviderAccessors_Ollama(t *testing.T) {
	p := NewAPIProviderFromConfig(APIProviderConfig{
		Name:    "ollama",
		BaseURL: "http://localhost:11434/v1",
		Auth:    "none",
	}, nil)

	if p.Auth() != "none" {
		t.Errorf("Auth: expected 'none', got %q", p.Auth())
	}
	if p.APIKey() != "" {
		t.Errorf("APIKey: expected empty, got %q", p.APIKey())
	}
	if p.Headers() != nil {
		t.Errorf("Headers: expected nil, got %v", p.Headers())
	}
}
