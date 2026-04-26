package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// mockExecutor implements ToolExecutor for testing.
type mockExecutor struct {
	calls   []ToolCallRequest
	results map[string]ToolCallResult // keyed by tool name
}

func (m *mockExecutor) Execute(_ context.Context, call ToolCallRequest) ToolCallResult {
	m.calls = append(m.calls, call)
	if r, ok := m.results[call.Name]; ok {
		r.ID = call.ID
		return r
	}
	return ToolCallResult{ID: call.ID, Output: "ok"}
}

func TestToolLoop_NoTools_PassesThrough(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	api := &APIProvider{
		baseURL: server.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
	executor := &mockExecutor{results: map[string]ToolCallResult{}}
	tl := NewToolLoop(api, executor, nil, 5)

	result, err := tl.Invoke(context.Background(), "hi", Params{Model: "test"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "Hello" {
		t.Errorf("expected 'Hello', got %q", result.Text)
	}
	if len(executor.calls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(executor.calls))
	}
}

func TestToolLoop_ToolCallLoop(t *testing.T) {
	var callCount atomic.Int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		n := callCount.Add(1)
		if n == 1 {
			// First call: return a tool call.
			w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"recall","arguments":"{\"query\":\"test\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
		} else {
			// Second call: return text.
			w.Write([]byte(`data: {"choices":[{"delta":{"content":"Found it!"},"finish_reason":"stop"}]}` + "\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	api := &APIProvider{
		name:    "test",
		baseURL: server.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
	executor := &mockExecutor{
		results: map[string]ToolCallResult{
			"recall": {Output: "Memory: user likes Go"},
		},
	}

	tools := []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "recall",
				"description": "Search memory",
				"parameters":  map[string]any{"type": "object"},
			},
		},
	}
	tl := NewToolLoop(api, executor, tools, 5)

	var events []StreamEvent
	result, err := tl.Invoke(context.Background(), "what do I like?", Params{Model: "test"}, func(e StreamEvent) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Text != "Found it!" {
		t.Errorf("expected 'Found it!', got %q", result.Text)
	}
	if result.NumTurns != 1 {
		t.Errorf("expected 1 turn, got %d", result.NumTurns)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(executor.calls))
	}
	if executor.calls[0].Name != "recall" {
		t.Errorf("expected tool 'recall', got %q", executor.calls[0].Name)
	}

	// Check stream events.
	hasToolUse := false
	hasToolResult := false
	for _, e := range events {
		if e.Type == "tool_use" {
			hasToolUse = true
		}
		if e.Type == "tool_result" {
			hasToolResult = true
		}
	}
	if !hasToolUse {
		t.Error("expected tool_use event")
	}
	if !hasToolResult {
		t.Error("expected tool_result event")
	}
}

func TestToolLoop_MaxTurns(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// Always return a tool call.
		w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"recall","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	api := &APIProvider{
		name:    "test",
		baseURL: server.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
	executor := &mockExecutor{results: map[string]ToolCallResult{}}

	tl := NewToolLoop(api, executor, nil, 2)

	result, err := tl.Invoke(context.Background(), "loop forever", Params{Model: "test"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NumTurns != 2 {
		t.Errorf("expected 2 turns, got %d", result.NumTurns)
	}
}

func TestAPIProvider_DoRequest_ToolCallsParsing(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify tools were sent in the request.
		var reqBody struct {
			Tools json.RawMessage `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&reqBody)
		if len(reqBody.Tools) == 0 {
			t.Error("expected tools in request body")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		// Simulate streamed tool_calls (name and arguments come in separate chunks).
		lines := []string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"recall"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"quer"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"y\":\"test\"}"}}]}}]}`,
			`data: {"choices":[{"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}
		for _, l := range lines {
			fmt.Fprintln(w, l)
			fmt.Fprintln(w)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	api := &APIProvider{
		name:    "test",
		baseURL: server.URL,
		client:  &http.Client{Timeout: 5 * time.Second},
	}

	tools, _ := json.Marshal([]map[string]any{
		{"type": "function", "function": map[string]any{"name": "recall"}},
	})

	messages := []apiMessage{{Role: "user", Content: "test"}}
	result, err := api.DoRequest(context.Background(), messages, "test-model", tools, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.FinishReason != "tool_calls" {
		t.Errorf("expected finish_reason 'tool_calls', got %q", result.FinishReason)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.ID != "call_abc" {
		t.Errorf("expected ID 'call_abc', got %q", tc.ID)
	}
	if tc.Function.Name != "recall" {
		t.Errorf("expected name 'recall', got %q", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"query":"test"}` {
		t.Errorf("expected arguments '{\"query\":\"test\"}', got %q", tc.Function.Arguments)
	}
}

func TestNestedString_Found(t *testing.T) {
	m := map[string]any{
		"outer": map[string]any{
			"inner": map[string]any{"name": "bob"},
		},
	}
	got, ok := nestedString(m, "outer", "inner", "name")
	if !ok || got != "bob" {
		t.Errorf("expected (bob, true), got (%q, %v)", got, ok)
	}
}

func TestNestedString_MissingKey(t *testing.T) {
	m := map[string]any{"a": map[string]any{"b": "c"}}
	_, ok := nestedString(m, "a", "x")
	if ok {
		t.Error("expected ok=false when leaf key is missing")
	}
}

func TestNestedString_NotAString(t *testing.T) {
	m := map[string]any{"a": map[string]any{"b": 42}}
	_, ok := nestedString(m, "a", "b")
	if ok {
		t.Error("expected ok=false when value is not a string")
	}
}

func TestNestedString_NonMapInPath(t *testing.T) {
	m := map[string]any{"a": "scalar"}
	_, ok := nestedString(m, "a", "b")
	if ok {
		t.Error("expected ok=false when traversing through a scalar")
	}
}

// TestWrapToolOutputForLLM_PinsMarkerShape pins the audit D6 fix:
// tool results sent to the API tool loop are wrapped in
// <tool_output source="..."> matching the kernel prompt's marker
// expectations from internal/runtime/llm.kernel_prompt.txt §3.2.
//
// Inlined here because internal/ai/provider cannot import
// internal/runtime/llm (foundation cross-import). If the kernel
// prompt's tag string ever changes, this test + the helper must be
// updated alongside internal/runtime/llm.TagToolOutput.
func TestWrapToolOutputForLLM_PinsMarkerShape(t *testing.T) {
	if got, want := wrapToolOutputForLLM("native.echo", "hi"), `<tool_output source="native.echo">hi</tool_output>`; got != want {
		t.Errorf("with source: got %q, want %q", got, want)
	}
	if got, want := wrapToolOutputForLLM("", "hi"), `<tool_output>hi</tool_output>`; got != want {
		t.Errorf("empty source: got %q, want %q", got, want)
	}
	// Adversarial source string must not break out of the attribute.
	if got := wrapToolOutputForLLM(`evil"><script>`, "x"); got != `<tool_output source="evil&quot;&gt;&lt;script&gt;">x</tool_output>` {
		t.Errorf("attribute escape failed: %q", got)
	}
}
