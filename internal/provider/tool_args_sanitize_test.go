package provider

import (
	"strings"
	"testing"
)

func TestSanitizeToolArgs_Redacts(t *testing.T) {
	in := `{"url":"https://x","api_key":"sk-abc","nested":{"token":"t","safe":"ok"},"auth":"Bearer xyz"}`
	out := sanitizeToolArgs(in)
	for _, leak := range []string{"sk-abc", "\"t\"", "Bearer xyz"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked sensitive value %q in %s", leak, out)
		}
	}
	for _, kept := range []string{"https://x", "safe", "ok"} {
		if !strings.Contains(out, kept) {
			t.Errorf("missing expected %q in %s", kept, out)
		}
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] marker, got %s", out)
	}
}

func TestSanitizeToolArgs_Truncates(t *testing.T) {
	big := `{"msg":"` + strings.Repeat("a", 2000) + `"}`
	out := sanitizeToolArgs(big)
	if len(out) > toolArgMaxLen+4 { // +4 for ellipsis rune
		t.Fatalf("expected truncated output, got len=%d", len(out))
	}
	if !strings.HasSuffix(out, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", out)
	}
}

func TestSanitizeToolArgs_NonJSON(t *testing.T) {
	in := "plain text with api_key=secret inside"
	out := sanitizeToolArgs(in)
	// Non-JSON input is passed through (no key/value structure to redact).
	if !strings.Contains(out, "api_key=secret") {
		t.Fatalf("non-json should be pass-through, got %q", out)
	}
}

func TestSanitizeToolArgs_Empty(t *testing.T) {
	if sanitizeToolArgs("") != "" {
		t.Fatal("empty input should return empty")
	}
}

func TestSanitizeToolError(t *testing.T) {
	if got := sanitizeToolError("  hello  "); got != "hello" {
		t.Errorf("expected trimmed, got %q", got)
	}
	long := strings.Repeat("x", 1000)
	if got := sanitizeToolError(long); !strings.HasSuffix(got, "…") || len(got) > toolErrMaxLen+4 {
		t.Errorf("expected truncation, got len=%d", len(got))
	}
}

func TestSensitiveKeyPattern(t *testing.T) {
	cases := map[string]bool{
		"token":       true,
		"api_token":   true,
		"apiToken":    true,
		"secret":      true,
		"PASSWORD":    true,
		"credentials": true,
		"authHeader":  true,
		"api-key":     true,
		"apiKey":      true,
		"key":         true,
		"username":    false,
		"url":         false,
		"content":     false,
	}
	for k, want := range cases {
		if got := sensitiveKeyPattern.MatchString(k); got != want {
			t.Errorf("%q: want %v, got %v", k, want, got)
		}
	}
}
