package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPIProvider_SSEParsing(t *testing.T) {
	// Mock OpenRouter SSE stream.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		lines := []string{
			`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
			`data: {"choices":[{"delta":{"content":" world"}}]}`,
			`data: {"choices":[{"delta":{"content":"!"}}]}`,
			`data: [DONE]`,
		}
		for _, l := range lines {
			w.Write([]byte(l + "\n\n"))
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := &APIProvider{
		apiKey:  "test-key",
		baseURL: server.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	result, err := p.Invoke(context.Background(), "test", Params{Model: "test-model"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "Hello world!" {
		t.Errorf("expected 'Hello world!', got %q", result.Text)
	}
	if result.Model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", result.Model)
	}
}

func TestAPIProvider_SystemPrompts(t *testing.T) {
	var capturedBody string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 8192)
		n, _ := r.Body.Read(body)
		capturedBody = string(body[:n])
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := &APIProvider{
		apiKey:  "test",
		baseURL: server.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	_, err := p.Invoke(context.Background(), "hello", Params{
		Model:         "test-model",
		SystemPrompts: []string{"You are helpful", "Be concise"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(capturedBody, "You are helpful") {
		t.Error("expected system prompt in request body")
	}
	if !strings.Contains(capturedBody, "Be concise") {
		t.Error("expected second system prompt in request body")
	}
}

func TestAPIProvider_HistoryIntegration(t *testing.T) {
	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"response\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	dir := t.TempDir()
	history := NewHistory(dir, 100, time.Hour)

	p := &APIProvider{
		apiKey:  "test",
		baseURL: server.URL,
		history: history,
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	// First call.
	_, err := p.Invoke(context.Background(), "hello", Params{
		Model:      "test-model",
		SessionKey: "test-session",
	}, nil)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	// Check history was stored.
	msgs := history.Get("test-session")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages in history, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "response" {
		t.Errorf("unexpected second message: %+v", msgs[1])
	}
}

func TestAPIProvider_NoHistoryForStateless(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	dir := t.TempDir()
	history := NewHistory(dir, 100, time.Hour)

	p := &APIProvider{
		apiKey:  "test",
		baseURL: server.URL,
		history: history,
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	// Call without SessionKey (stateless/classify mode).
	_, err := p.Invoke(context.Background(), "classify this", Params{Model: "test"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// History should be empty for stateless calls.
	msgs := history.Get("")
	if msgs != nil {
		t.Errorf("expected no history for stateless call, got %v", msgs)
	}
}

func TestAPIProvider_OnProgress(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := &APIProvider{
		apiKey:  "test",
		baseURL: server.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	var events []StreamEvent
	_, err := p.Invoke(context.Background(), "test", Params{Model: "m"}, func(e StreamEvent) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 progress events, got %d", len(events))
	}
	for _, e := range events {
		if e.Type != "text_delta" {
			t.Errorf("expected 'text_delta' event type, got %q", e.Type)
		}
	}
}

func TestApiMessage_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		msg     apiMessage
		wantKey string // key that must exist
		wantVal string // expected value for wantKey ("null" for JSON null)
	}{
		{
			name:    "assistant with tool_calls emits content null",
			msg:     apiMessage{Role: "assistant", ToolCalls: []apiToolCall{{ID: "1", Type: "function", Function: apiToolCallFn{Name: "bash", Arguments: `{}`}}}},
			wantKey: "content",
			wantVal: "null",
		},
		{
			name:    "assistant with tool_calls and text emits content string",
			msg:     apiMessage{Role: "assistant", Content: "thinking...", ToolCalls: []apiToolCall{{ID: "1", Type: "function", Function: apiToolCallFn{Name: "bash", Arguments: `{}`}}}},
			wantKey: "content",
			wantVal: `"thinking..."`,
		},
		{
			name:    "tool result always emits content",
			msg:     apiMessage{Role: "tool", Content: "output", ToolCallID: "1"},
			wantKey: "content",
			wantVal: `"output"`,
		},
		{
			name:    "tool result emits content even if empty",
			msg:     apiMessage{Role: "tool", Content: "", ToolCallID: "1"},
			wantKey: "content",
			wantVal: `""`,
		},
		{
			name:    "user message omits content when empty",
			msg:     apiMessage{Role: "user"},
			wantKey: "content",
			wantVal: "", // absent
		},
		{
			name:    "user message includes content when set",
			msg:     apiMessage{Role: "user", Content: "hello"},
			wantKey: "content",
			wantVal: `"hello"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			var raw map[string]json.RawMessage
			json.Unmarshal(data, &raw)

			val, exists := raw[tt.wantKey]
			if tt.wantVal == "" {
				if exists {
					t.Errorf("expected %q to be absent, got %s", tt.wantKey, string(val))
				}
				return
			}
			if !exists {
				t.Fatalf("expected %q in JSON, got: %s", tt.wantKey, string(data))
			}
			if string(val) != tt.wantVal {
				t.Errorf("expected %s=%s, got %s", tt.wantKey, tt.wantVal, string(val))
			}
		})
	}
}

func TestApiMessage_RoundTrip(t *testing.T) {
	original := apiMessage{
		Role:      "assistant",
		ToolCalls: []apiToolCall{{ID: "call_1", Type: "function", Function: apiToolCallFn{Name: "grep", Arguments: `{"pattern":"foo"}`}}},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded apiMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Role != "assistant" {
		t.Errorf("role: got %q", decoded.Role)
	}
	if len(decoded.ToolCalls) != 1 || decoded.ToolCalls[0].Function.Name != "grep" {
		t.Errorf("tool_calls not preserved: %+v", decoded.ToolCalls)
	}
}

func TestApiRequest_CacheControl_AnthropicModel(t *testing.T) {
	// When model starts with "anthropic/", cache_control should be set.
	var capturedBody json.RawMessage
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 16384)
		n, _ := r.Body.Read(body)
		capturedBody = body[:n]
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := &APIProvider{
		apiKey:    "test",
		baseURL:   server.URL,
		maxTokens: 4096,
		client:    &http.Client{Timeout: 5 * time.Second},
	}

	_, err := p.Invoke(context.Background(), "test", Params{Model: "anthropic/claude-haiku-4-5"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req struct {
		CacheControl *apiCacheControl `json:"cache_control"`
	}
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.CacheControl == nil {
		t.Fatal("expected cache_control to be set for anthropic/ model")
	}
	if req.CacheControl.Type != "ephemeral" {
		t.Errorf("expected type 'ephemeral', got %q", req.CacheControl.Type)
	}
}

func TestApiRequest_CacheControl_NonAnthropicModel(t *testing.T) {
	// When model does NOT start with "anthropic/", cache_control should be omitted.
	var capturedBody json.RawMessage
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 16384)
		n, _ := r.Body.Read(body)
		capturedBody = body[:n]
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := &APIProvider{
		apiKey:    "test",
		baseURL:   server.URL,
		maxTokens: 4096,
		client:    &http.Client{Timeout: 5 * time.Second},
	}

	_, err := p.Invoke(context.Background(), "test", Params{Model: "openai/gpt-4o"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(capturedBody, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, exists := raw["cache_control"]; exists {
		t.Error("expected cache_control to be omitted for non-anthropic model")
	}
}

func TestApiCacheControl_JSON(t *testing.T) {
	tests := []struct {
		name string
		cc   apiCacheControl
		want string
	}{
		{
			name: "ephemeral without TTL",
			cc:   apiCacheControl{Type: "ephemeral"},
			want: `{"type":"ephemeral"}`,
		},
		{
			name: "ephemeral with TTL",
			cc:   apiCacheControl{Type: "ephemeral", TTL: "1h"},
			want: `{"type":"ephemeral","ttl":"1h"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.cc)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			if string(data) != tt.want {
				t.Errorf("got %s, want %s", string(data), tt.want)
			}
		})
	}
}

func TestApiStreamResult_CachedTokens(t *testing.T) {
	// Verify cached_tokens is parsed from SSE usage chunk.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		w.Write([]byte(`data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":80}}}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := &APIProvider{
		apiKey:    "test",
		baseURL:   server.URL,
		maxTokens: 4096,
		client:    &http.Client{Timeout: 5 * time.Second},
	}

	msgs := []apiMessage{{Role: "user", Content: "test"}}
	result, err := p.DoRequest(context.Background(), msgs, "anthropic/claude-haiku-4-5", nil, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CachedTokens != 80 {
		t.Errorf("expected CachedTokens=80, got %d", result.CachedTokens)
	}
	if result.InputTokens != 100 {
		t.Errorf("expected InputTokens=100, got %d", result.InputTokens)
	}
}

func TestAPIProvider_DoRequest_CacheControl_AnthropicModel(t *testing.T) {
	// DoRequest should also set cache_control for anthropic/ models.
	var capturedBody json.RawMessage
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 16384)
		n, _ := r.Body.Read(body)
		capturedBody = body[:n]
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := &APIProvider{
		apiKey:    "test",
		baseURL:   server.URL,
		maxTokens: 4096,
		client:    &http.Client{Timeout: 5 * time.Second},
	}

	msgs := []apiMessage{{Role: "user", Content: "test"}}
	_, err := p.DoRequest(context.Background(), msgs, "anthropic/claude-sonnet-4-5", nil, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req struct {
		CacheControl *apiCacheControl `json:"cache_control"`
	}
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.CacheControl == nil {
		t.Fatal("expected cache_control in DoRequest for anthropic/ model")
	}
	if req.CacheControl.Type != "ephemeral" {
		t.Errorf("expected type 'ephemeral', got %q", req.CacheControl.Type)
	}
}

func TestAPIProvider_ErrorStatus(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error": "internal server error"}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	p := &APIProvider{
		apiKey:  "test",
		baseURL: server.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	_, err := p.Invoke(context.Background(), "test", Params{Model: "m"}, nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to mention status code, got: %v", err)
	}
}
