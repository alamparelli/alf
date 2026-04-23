package ai

import "strings"

// ResolveModel maps a short tier name to the canonical provider model ID.
//
// This is the single authoritative resolver. Any hardcoded model fallback
// outside this function is reported by TestHardcodedModelFallback in the
// archtest package (see technical/ARCHITECTURE-v0.7.10.md §2.3 rule 1).
//
// Returns the empty ModelID when the input does not resolve — callers
// decide whether that is an error or a trigger for tier fallback.
func ResolveModel(short string) ModelID {
	switch strings.ToLower(short) {
	case "haiku":
		return "claude-haiku-4-5"
	case "sonnet":
		return "claude-sonnet-4-6"
	case "opus":
		return "claude-opus-4-6"
	case "sonnet-max":
		return "claude-sonnet-4-6-max"
	case "opus-max":
		return "claude-opus-4-6-max"
	default:
		if strings.HasPrefix(short, "claude-") {
			return ModelID(short)
		}
		return ""
	}
}
