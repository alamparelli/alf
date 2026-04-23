package provider

import (
	"encoding/json"
	"regexp"
	"strings"
)

// toolArgMaxLen caps the span-tagged args payload. Args are user/LLM-generated
// and can hit megabytes (e.g. write_file bodies); spans go to a JSONL file and
// should stay small.
const toolArgMaxLen = 500

// toolErrMaxLen caps the error message tagged on tool spans.
const toolErrMaxLen = 500

// sensitiveKeyPattern matches argument keys whose values should be redacted
// before persisting to a trace. Case-insensitive, matches substrings so
// "api_token", "accessKey", "auth_header" are all caught.
var sensitiveKeyPattern = regexp.MustCompile(`(?i)(token|secret|password|credential|auth|api[_-]?key|key)`)

// sanitizeToolArgs returns a safe representation of tool arguments suitable
// for persisting in a span tag:
//   - valid JSON objects: sensitive keys have their value replaced with "[REDACTED]"
//   - anything else: used as-is (may be plain text)
// The result is then truncated to toolArgMaxLen.
func sanitizeToolArgs(raw string) string {
	if raw == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		redactMap(obj)
		if b, err := json.Marshal(obj); err == nil {
			return truncateSafe(string(b), toolArgMaxLen)
		}
	}
	return truncateSafe(raw, toolArgMaxLen)
}

func redactMap(m map[string]any) {
	for k, v := range m {
		if sensitiveKeyPattern.MatchString(k) {
			m[k] = "[REDACTED]"
			continue
		}
		switch vv := v.(type) {
		case map[string]any:
			redactMap(vv)
		case []any:
			for _, item := range vv {
				if sub, ok := item.(map[string]any); ok {
					redactMap(sub)
				}
			}
		}
	}
}

// truncateSafe trims to max bytes without splitting a UTF-8 code point.
func truncateSafe(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Trim to rune boundary to avoid splitting multi-byte characters.
	cut := max
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func isRuneStart(b byte) bool {
	// ASCII, or UTF-8 leading byte.
	return b < 0x80 || b&0xC0 == 0xC0
}

// sanitizeToolError trims and truncates an error message for trace tagging.
func sanitizeToolError(msg string) string {
	msg = strings.TrimSpace(msg)
	return truncateSafe(msg, toolErrMaxLen)
}
