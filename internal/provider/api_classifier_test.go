package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAPIClassifier_Classify(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"instant\"}}]}\n\ndata: [DONE]\n\n"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	history := NewHistory(t.TempDir(), 100, time.Hour)
	api := NewAPIProviderFromConfig(APIProviderConfig{
		Name:    "test",
		BaseURL: server.URL,
		Auth:    "none",
	}, history)

	c := NewAPIClassifier(api, history, "You are a router.")
	result, err := c.Classify(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Tier != "instant" {
		t.Errorf("expected tier 'instant', got %q", result.Tier)
	}
}

func TestAPIClassifier_Restart(t *testing.T) {
	history := NewHistory(t.TempDir(), 100, time.Hour)
	api := NewAPIProviderFromConfig(APIProviderConfig{
		Name: "test",
		Auth: "none",
	}, history)

	c := NewAPIClassifier(api, history, "prompt")

	// Add some history.
	history.Append("classifier", Message{Role: "user", Content: "test"})
	if msgs := history.Get("classifier"); len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	// Restart clears history.
	if err := c.Restart(); err != nil {
		t.Fatalf("restart error: %v", err)
	}
	if msgs := history.Get("classifier"); msgs != nil {
		t.Errorf("expected nil history after restart, got %v", msgs)
	}
}

func TestAPIClassifier_InjectContext(t *testing.T) {
	history := NewHistory(t.TempDir(), 100, time.Hour)
	api := NewAPIProviderFromConfig(APIProviderConfig{Name: "test", Auth: "none"}, history)
	c := NewAPIClassifier(api, history, "prompt")

	err := c.InjectContext("sonnet", "read-write", "helped with code")
	if err != nil {
		t.Fatalf("inject error: %v", err)
	}

	msgs := history.Get("classifier")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "assistant" {
		t.Errorf("expected assistant role, got %q", msgs[0].Role)
	}
}

func TestAPIClassifier_Close(t *testing.T) {
	c := NewAPIClassifier(nil, nil, "")
	if err := c.Close(); err != nil {
		t.Errorf("close should be no-op, got error: %v", err)
	}
}
