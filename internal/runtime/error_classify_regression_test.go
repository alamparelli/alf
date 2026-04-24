package comms

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Regression tests: exact error messages from each backend.
// If a provider changes its error format, these tests catch the mismatch.

func TestRegression_CLI_ErrorMaxTurns(t *testing.T) {
	// Exact error from cli.go:435 when parsed.IsError && text == "" && subtype == "error_max_turns"
	notice := classifyProviderError("claude: error_max_turns", nil)
	assertContains(t, notice, "Turn limit", "CLI error_max_turns")
	assertContains(t, notice, "/new", "CLI error_max_turns should suggest /new")
}

func TestRegression_CLI_NoConversationFound(t *testing.T) {
	// cli.go:449-450: session expired or invalid
	notice := classifyProviderError("claude: No conversation found with that ID", nil)
	// This is a generic error — should still produce a usable message
	if notice == "" {
		t.Error("expected non-empty notice for no conversation found")
	}
}

func TestRegression_Codex_RateLimit(t *testing.T) {
	// codex.go:303: error event from JSONL stream
	notice := classifyProviderError("codex: rate limit exceeded", nil)
	assertContains(t, notice, "Rate limit", "Codex rate limit")
}

func TestRegression_Codex_GenericError(t *testing.T) {
	// codex.go:295-303: any error event
	notice := classifyProviderError("codex: unknown codex error", nil)
	if notice == "" {
		t.Error("expected non-empty notice for codex generic error")
	}
}

func TestRegression_API_429_OpenRouter(t *testing.T) {
	// api.go:376-378 after 3 retries exhausted
	notice := classifyProviderError(`api[openrouter] error 429: {"error":{"message":"Rate limit exceeded"}}`, nil)
	assertContains(t, notice, "Rate limit", "OpenRouter 429")
}

func TestRegression_API_429_OpenAI(t *testing.T) {
	notice := classifyProviderError(`api[openai] error 429: You exceeded your current quota`, nil)
	assertContains(t, notice, "Rate limit", "OpenAI 429")
}

func TestRegression_API_429_Grok(t *testing.T) {
	notice := classifyProviderError(`api[grok] error 429: too many requests`, nil)
	assertContains(t, notice, "Rate limit", "Grok 429")
}

func TestRegression_API_400_ContextLength(t *testing.T) {
	// Common across OpenAI/OpenRouter/Gemini
	notice := classifyProviderError(`api[openai] error 400: This model's maximum context length is 128000 tokens`, nil)
	assertContains(t, notice, "Context too long", "context_length 400")
}

func TestRegression_API_400_ContextLengthExceeded(t *testing.T) {
	notice := classifyProviderError(`api[openrouter] error 400: context_length_exceeded`, nil)
	assertContains(t, notice, "Context too long", "context_length_exceeded")
}

func TestRegression_API_401_InvalidKey(t *testing.T) {
	notice := classifyProviderError(`api[openai] error 401: Invalid API key`, nil)
	assertContains(t, notice, "Authentication", "401 invalid key")
}

func TestRegression_API_403_Forbidden(t *testing.T) {
	notice := classifyProviderError(`api[gemini] error 403: Permission denied`, nil)
	assertContains(t, notice, "Authentication", "403 forbidden")
}

func TestRegression_API_404_ModelNotFound(t *testing.T) {
	notice := classifyProviderError(`api[openrouter] error 404: model not found`, nil)
	assertContains(t, notice, "Model not available", "404 model not found")
}

func TestRegression_API_500_ServerError(t *testing.T) {
	notice := classifyProviderError(`api[openai] error 500: Internal server error`, nil)
	assertContains(t, notice, "provider", "500 server error")
}

func TestRegression_API_502_BadGateway(t *testing.T) {
	notice := classifyProviderError(`api[openrouter] error 502: Bad Gateway`, nil)
	assertContains(t, notice, "provider", "502 bad gateway")
}

func TestRegression_API_503_ServiceUnavailable(t *testing.T) {
	notice := classifyProviderError(`api[gemini] error 503: Service Unavailable`, nil)
	assertContains(t, notice, "provider", "503 service unavailable")
}

func TestRegression_Ollama_ConnectionRefused(t *testing.T) {
	notice := classifyProviderError(`api[ollama] error 0: connection refused`, nil)
	// Falls through to generic — should still be informative
	if notice == "" {
		t.Error("expected non-empty notice for connection refused")
	}
}

func TestRegression_ContextDeadlineExceeded(t *testing.T) {
	// Any backend can hit this via ctx timeout
	notice := classifyProviderError("context deadline exceeded", context.DeadlineExceeded)
	assertContains(t, notice, "timed out", "context deadline")
}

func TestRegression_ContextCanceled(t *testing.T) {
	// User cancelled via stopCall
	notice := classifyProviderError("context canceled", context.Canceled)
	assertContains(t, notice, "cancelled", "context canceled")
}

func TestRegression_CLI_SignalKilled(t *testing.T) {
	// CLI subprocess killed (e.g. OOM, user stop)
	notice := classifyProviderError("signal: killed", context.Canceled)
	assertContains(t, notice, "cancelled", "signal killed")
}

func TestRegression_ToolLoop_TurnLimit(t *testing.T) {
	// toolloop.go:97: text set to "Tool calling turn limit reached."
	// This comes through as result text, not an error — tested via detectTurnLimit.
	// But if somehow it arrives as an error:
	notice := classifyProviderError("Tool calling turn limit reached", nil)
	assertContains(t, notice, "Turn limit", "toolloop turn limit as error")
}

func TestRegression_TurnLimitHint_NoResume(t *testing.T) {
	// #235: The turn limit hint must NOT reference /resume (it doesn't exist).
	// The hint is a hardcoded string in pipeline.go — grep for it here to catch regressions.
	data, err := os.ReadFile("pipeline.go")
	if err != nil {
		t.Fatal("cannot read pipeline.go:", err)
	}
	content := string(data)
	if strings.Contains(content, "/resume") {
		t.Error("pipeline.go still references /resume — this command does not exist (see #235)")
	}
}

func assertContains(t *testing.T, s, substr, context string) {
	t.Helper()
	if !strings.Contains(strings.ToLower(s), strings.ToLower(substr)) {
		t.Errorf("[%s] expected %q to contain %q", context, s, substr)
	}
}
