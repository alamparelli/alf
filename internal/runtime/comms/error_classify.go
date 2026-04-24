package comms

import (
	"context"
	"strings"

	provider "github.com/alamparelli/alf/internal/ai/provider"
)

// classifyProviderError returns a user-friendly notice for provider errors.
// Covers all backends: CLI (Claude), Codex, API (OpenRouter/OpenAI/Grok/Gemini/Ollama).
func classifyProviderError(errMsg string, ctxErr error) string {
	lower := strings.ToLower(errMsg)

	// Turn/conversation limits (CLI: error_max_turns, Codex: various).
	if strings.Contains(lower, "max_turns") ||
		strings.Contains(lower, "turn limit") ||
		strings.Contains(lower, "conversation limit") ||
		strings.Contains(lower, "max turns") {
		return "Turn limit reached. Use /new to start a fresh conversation, or break your request into smaller steps."
	}

	// Timeout (context deadline, CLI timeout, API timeout).
	if ctxErr == context.DeadlineExceeded ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "timed out") {
		return "Request timed out. The model took too long to respond. Try a simpler prompt or a faster tier."
	}

	// Rate limiting (API 429, Codex rate limit).
	if strings.Contains(lower, "429") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "too many requests") {
		return "Rate limit hit. Wait a moment and try again, or switch to a different tier."
	}

	// Context too long / token limit (API: 400 context_length_exceeded).
	if strings.Contains(lower, "context_length") ||
		strings.Contains(lower, "context length") ||
		strings.Contains(lower, "maximum context") ||
		strings.Contains(lower, "token limit") ||
		strings.Contains(lower, "too many tokens") ||
		strings.Contains(lower, "max tokens") {
		return "Context too long for this model. Use /new to start fresh, or switch to a tier with a larger context window."
	}

	// Authentication / API key issues.
	if strings.Contains(lower, "401") ||
		strings.Contains(lower, "403") ||
		strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "invalid api key") ||
		strings.Contains(lower, "authentication") {
		return "Authentication error. Check your API key in the tier configuration."
	}

	// Model not found / unavailable.
	if strings.Contains(lower, "404") ||
		strings.Contains(lower, "model not found") ||
		strings.Contains(lower, "does not exist") {
		return "Model not available. Check the model name in your tier configuration."
	}

	// Server errors (500, 502, 503).
	if strings.Contains(lower, "500") ||
		strings.Contains(lower, "502") ||
		strings.Contains(lower, "503") ||
		strings.Contains(lower, "internal server error") ||
		strings.Contains(lower, "service unavailable") ||
		strings.Contains(lower, "bad gateway") {
		return "The AI provider is experiencing issues. Try again in a moment, or switch to a different tier."
	}

	// Cancelled by user.
	if ctxErr == context.Canceled ||
		strings.Contains(lower, "context canceled") ||
		strings.Contains(lower, "signal: killed") {
		return "Request was cancelled."
	}

	// Generic fallback.
	return "An error occurred: " + truncate(errMsg, 200) + ". Try again or use /new to start fresh."
}

// detectTurnLimit checks if a successful result indicates a turn limit was hit.
// CLI provider returns text with "Turn limit reached", ToolLoop returns "Tool calling turn limit reached".
func detectTurnLimit(result *provider.Result, text string) string {
	if result == nil {
		return ""
	}

	lower := strings.ToLower(text)
	if strings.Contains(lower, "turn limit reached") ||
		strings.Contains(lower, "tool calling turn limit") {
		return "Turn limit reached for this request. You can continue the conversation — the model will pick up where it left off. Use /new if you want to start fresh."
	}

	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
