package tooling

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type mockLLMService struct {
	result string
	err    error
}

func (m *mockLLMService) Invoke(_ context.Context, opts LLMInvokeOpts) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.result, nil
}

func TestLLMTool_Invoke(t *testing.T) {
	svc := &mockLLMService{result: "The text is about climate change."}
	tool := LLMNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"tier":"haiku","prompt":"Classify this text: ..."}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "climate change") {
		t.Fatalf("expected LLM result, got: %s", out)
	}
}

func TestLLMTool_InvokeWithSystem(t *testing.T) {
	svc := &mockLLMService{result: "Résumé: ..."}
	tool := LLMNativeTool{Service: svc}

	out, err := tool.Run(context.Background(), `{"tier":"sonnet","prompt":"Summarize this","system":"Reply in French"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "Résumé: ..." {
		t.Fatalf("expected French result, got: %s", out)
	}
}

func TestLLMTool_MissingTier(t *testing.T) {
	tool := LLMNativeTool{Service: &mockLLMService{}}

	_, err := tool.Run(context.Background(), `{"prompt":"hello"}`)
	if err == nil || !strings.Contains(err.Error(), "tier is required") {
		t.Fatalf("expected tier required error, got: %v", err)
	}
}

func TestLLMTool_MissingPrompt(t *testing.T) {
	tool := LLMNativeTool{Service: &mockLLMService{}}

	_, err := tool.Run(context.Background(), `{"tier":"haiku"}`)
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("expected prompt required error, got: %v", err)
	}
}

func TestLLMTool_InvokeError(t *testing.T) {
	svc := &mockLLMService{err: fmt.Errorf("tier 'bad' not found")}
	tool := LLMNativeTool{Service: svc}

	_, err := tool.Run(context.Background(), `{"tier":"bad","prompt":"test"}`)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected error, got: %v", err)
	}
}

func TestLLMTool_InvalidJSON(t *testing.T) {
	tool := LLMNativeTool{Service: &mockLLMService{}}

	_, err := tool.Run(context.Background(), `{bad}`)
	if err == nil {
		t.Fatal("expected JSON error")
	}
}

func TestLLMTool_Schema(t *testing.T) {
	s := LLMNativeTool{}.Schema()
	if s.Name != "llm" {
		t.Fatalf("expected 'llm', got %q", s.Name)
	}
	props, ok := s.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties")
	}
	if _, ok := props["tier"]; !ok {
		t.Fatal("expected 'tier' property")
	}
	if _, ok := props["prompt"]; !ok {
		t.Fatal("expected 'prompt' property")
	}
}
