package comms

import (
	"context"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/provider"
)

func TestClassifyProviderError_TurnLimit(t *testing.T) {
	cases := []string{
		"claude: error_max_turns",
		"Turn limit reached",
		"conversation limit exceeded",
		"max turns reached",
	}
	for _, errMsg := range cases {
		notice := classifyProviderError(errMsg, nil)
		if !strings.Contains(notice, "/new") {
			t.Errorf("turn limit error %q: expected /new suggestion, got: %s", errMsg, notice)
		}
		if !strings.Contains(strings.ToLower(notice), "turn limit") {
			t.Errorf("turn limit error %q: expected 'turn limit' in notice, got: %s", errMsg, notice)
		}
	}
}

func TestClassifyProviderError_Timeout(t *testing.T) {
	// Context deadline.
	notice := classifyProviderError("context deadline exceeded", context.DeadlineExceeded)
	if !strings.Contains(strings.ToLower(notice), "timed out") && !strings.Contains(strings.ToLower(notice), "timeout") {
		t.Errorf("timeout: expected timeout notice, got: %s", notice)
	}

	// String-based timeout.
	notice = classifyProviderError("request timeout after 60s", nil)
	if !strings.Contains(strings.ToLower(notice), "timed out") && !strings.Contains(strings.ToLower(notice), "timeout") {
		t.Errorf("string timeout: expected timeout notice, got: %s", notice)
	}
}

func TestClassifyProviderError_RateLimit(t *testing.T) {
	cases := []string{
		"api[openrouter] error 429: rate limited",
		"codex: rate limit exceeded",
		"too many requests",
	}
	for _, errMsg := range cases {
		notice := classifyProviderError(errMsg, nil)
		if !strings.Contains(strings.ToLower(notice), "rate limit") {
			t.Errorf("rate limit error %q: expected rate limit notice, got: %s", errMsg, notice)
		}
	}
}

func TestClassifyProviderError_ContextLength(t *testing.T) {
	cases := []string{
		"api[openai] error 400: context_length_exceeded",
		"maximum context length exceeded",
		"too many tokens for this model",
	}
	for _, errMsg := range cases {
		notice := classifyProviderError(errMsg, nil)
		if !strings.Contains(strings.ToLower(notice), "context too long") {
			t.Errorf("context length error %q: expected context notice, got: %s", errMsg, notice)
		}
	}
}

func TestClassifyProviderError_Auth(t *testing.T) {
	notice := classifyProviderError("api[grok] error 401: unauthorized", nil)
	if !strings.Contains(strings.ToLower(notice), "authentication") {
		t.Errorf("auth error: expected auth notice, got: %s", notice)
	}
}

func TestClassifyProviderError_ServerError(t *testing.T) {
	notice := classifyProviderError("api[gemini] error 503: service unavailable", nil)
	if !strings.Contains(strings.ToLower(notice), "provider") {
		t.Errorf("server error: expected provider notice, got: %s", notice)
	}
}

func TestClassifyProviderError_Cancelled(t *testing.T) {
	notice := classifyProviderError("context canceled", context.Canceled)
	if !strings.Contains(strings.ToLower(notice), "cancelled") {
		t.Errorf("cancel: expected cancelled notice, got: %s", notice)
	}
}

func TestClassifyProviderError_Fallback(t *testing.T) {
	notice := classifyProviderError("some unknown error", nil)
	if !strings.Contains(notice, "some unknown error") {
		t.Errorf("fallback: expected original error in notice, got: %s", notice)
	}
}

func TestDetectTurnLimit_FromResult(t *testing.T) {
	// CLI turn limit.
	notice := detectTurnLimit(&provider.Result{NumTurns: 5}, "Turn limit reached - try breaking this into smaller steps.")
	if notice == "" {
		t.Error("expected turn limit notice for CLI turn limit text")
	}

	// ToolLoop turn limit.
	notice = detectTurnLimit(&provider.Result{NumTurns: 10}, "Tool calling turn limit reached.")
	if notice == "" {
		t.Error("expected turn limit notice for toolloop turn limit text")
	}

	// Normal result.
	notice = detectTurnLimit(&provider.Result{NumTurns: 3}, "Here is your answer.")
	if notice != "" {
		t.Errorf("expected no notice for normal result, got: %s", notice)
	}

	// Nil result.
	notice = detectTurnLimit(nil, "")
	if notice != "" {
		t.Errorf("expected no notice for nil result, got: %s", notice)
	}
}
