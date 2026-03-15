package controlcenter

import (
	"regexp"
	"strings"
)

// safeName matches safe resource/file names: alphanumeric, dashes, underscores.
var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// isSafeName validates that a name has no path traversal characters
// and matches the safe name pattern.
func isSafeName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.Contains(s, "/") || strings.Contains(s, "\\") {
		return false
	}
	return safeName.MatchString(s)
}

// stripCodeBlock removes markdown code fences from LLM responses.
// Handles ```json, ```markdown, ```md, ```yaml, and bare ``` wrappers.
func stripCodeBlock(s string) string {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"```json", "```markdown", "```md", "```yaml", "```"} {
		s = strings.TrimPrefix(s, prefix)
	}
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
