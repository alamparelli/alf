// Package llm carries the daemon-shipped LLM-side artefacts that constrain
// agent behaviour. Today: the kernel prompt that asserts memory policy,
// capability-content non-authority markers, and the admin-boundary refusal
// rules per docs/ARCHITECTURE-SECURITY.md §3.2 (Tier 3.2 agent-mediated).
//
// The kernel prompt is embedded at compile time so a deployment cannot
// silently lose it (no file-not-found surface) and a third party cannot
// substitute it without re-building the daemon binary.
package llm

import _ "embed"

//go:embed kernel_prompt.txt
var kernelPrompt string

// KernelPrompt returns the immutable kernel-prompt text. The string is
// generated from kernel_prompt.txt at compile time. Empty return is
// treated as a programming error by the prepender — daemons must not
// run with an empty kernel prompt because the agent-mediation guarantee
// of §3.2 collapses without it.
func KernelPrompt() string {
	return kernelPrompt
}

// Marker tag names used to wrap capability-provided content before it
// reaches the LLM. The kernel prompt instructs the agent to treat text
// inside any of these tags as non-authoritative data, not as commands.
//
// Constants are exported so callers (skill prompt assemblers, tool
// output formatters, fetcher integrations) reference the same strings —
// a typo in a wrapper site would silently break the kernel prompt's
// match expectations.
const (
	TagCapabilityContent = "capability_content"
	TagToolOutput        = "tool_output"
	TagFetchedContent    = "fetched_content"
)

// WrapCapabilityContent surrounds content with the capability_content
// marker, attributing the source for audit. Use for skill prompts,
// agent instructions, or any other text that originated from a
// capability and is being injected into the LLM context.
func WrapCapabilityContent(source, content string) string {
	return wrapWithSource(TagCapabilityContent, source, content)
}

// WrapToolOutput wraps the result of a tool invocation. Use at the
// site where tool outputs are appended to the LLM message history.
func WrapToolOutput(toolName, content string) string {
	return wrapWithSource(TagToolOutput, toolName, content)
}

// WrapFetchedContent wraps content fetched from an external resource
// (web page, API response). Use at the site where fetched bytes
// become part of the LLM context.
func WrapFetchedContent(url, content string) string {
	return wrapWithSource(TagFetchedContent, url, content)
}

func wrapWithSource(tag, source, content string) string {
	if source == "" {
		return "<" + tag + ">" + content + "</" + tag + ">"
	}
	return "<" + tag + ` source="` + escapeAttr(source) + `">` + content + "</" + tag + ">"
}

// escapeAttr does a minimal HTML-attribute escape for source strings
// that might contain quotes or angle brackets. Not a full HTML escaper
// — capability source strings are short, controlled identifiers.
func escapeAttr(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			out = append(out, '&', 'q', 'u', 'o', 't', ';')
		case '<':
			out = append(out, '&', 'l', 't', ';')
		case '>':
			out = append(out, '&', 'g', 't', ';')
		case '&':
			out = append(out, '&', 'a', 'm', 'p', ';')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
