// Package llm carries the daemon-shipped LLM-side artefacts that constrain
// agent behaviour. Today: the kernel prompt that asserts memory policy,
// capability-content non-authority markers, and the admin-boundary refusal
// rules per docs/ARCHITECTURE-SECURITY.md §3.2 (Tier 3.2 agent-mediated).
//
// The kernel prompt is embedded at compile time so a deployment cannot
// silently lose it (no file-not-found surface) and a third party cannot
// substitute it without re-building the daemon binary.
package llm

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// randReader is the entropy source for NewNonce. Tests rebind this to
// a failing reader to cover the SEC-080-005 fail-loud path; production
// uses crypto/rand. Restore via the returned closer when done.
var randReader io.Reader = rand.Reader

// SetRandReaderForTest swaps randReader and returns a closer that
// restores it. Callers must defer the closer.
func SetRandReaderForTest(r io.Reader) func() {
	prev := randReader
	randReader = r
	return func() { randReader = prev }
}

//go:embed kernel_prompt.txt
var kernelPrompt string

// KernelPrompt returns the immutable kernel-prompt text. The string is
// generated from kernel_prompt.txt at compile time. Empty return is
// treated as a programming error by the prepender — daemons must not
// run with an empty kernel prompt because the agent-mediation guarantee
// of §3.2 collapses without it.
//
// The returned string carries {NONCE} placeholders inside the marker
// definitions. The KernelPromptInjector at provider-Invoke time
// generates a fresh per-turn nonce and substitutes it across the
// kernel prompt, every other system prompt, the user prompt, and
// every conversation message. Without this binding, a tool that emits
// the literal closing tag bytes can break out of the marker and
// inject pseudo-system instructions — see SEC-002.
func KernelPrompt() string {
	return kernelPrompt
}

// NoncePlaceholder is the literal token inside the kernel prompt and
// every wrap-site output. The KernelPromptInjector replaces every
// occurrence of this string with a freshly-generated random nonce on
// every Invoke. Exported so providers below the injector layer
// (e.g. ToolLoop, which wraps tool outputs during multi-turn loops
// after the injector has already run) can perform the same
// substitution against the per-Invoke nonce.
const NoncePlaceholder = "{NONCE}"

// NewNonce returns a fresh 16-hex-char (8 random bytes) nonce. Used
// by the KernelPromptInjector at the start of every Invoke. Unguessable
// — crypto/rand backs it. A WASM tool composing its output cannot
// predict this value because it does not run inside the daemon's
// PRNG.
//
// SEC-080-005: returns an error rather than falling back to a constant
// when crypto/rand fails. The previous fallback to a 16-zero-hex string
// was predictable, so a malicious capability output that anticipated
// the failure path could break out of the marker tag with a literal
// closing-tag-plus-zero-nonce sequence and re-enter the kernel-prompt
// trust domain. crypto/rand on Linux/Darwin reads from getrandom(2) /
// /dev/urandom and returning an error from this function means the
// LLM call is aborted — the right outcome when the OS PRNG is broken.
func NewNonce() (string, error) {
	var b [8]byte
	if _, err := io.ReadFull(randReader, b[:]); err != nil {
		return "", fmt.Errorf("kernel-prompt nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// SubstituteNonce replaces every NoncePlaceholder occurrence in s
// with the given nonce. Used by the injector to materialise the
// kernel prompt + wrapped content for a specific turn, and by
// downstream providers that wrap tool outputs after the injector has
// run (so they need to substitute the same nonce in their own
// loop-local wraps).
func SubstituteNonce(s, nonce string) string {
	if !strings.Contains(s, NoncePlaceholder) {
		return s
	}
	return strings.ReplaceAll(s, NoncePlaceholder, nonce)
}

// Marker tag names used to wrap capability-provided content before it
// reaches the LLM. The kernel prompt instructs the agent to treat text
// inside any of these tags as non-authoritative data, not as commands.
//
// Constants are exported so callers (skill prompt assemblers, tool
// output formatters, fetcher integrations) reference the same strings —
// a typo in a wrapper site would silently break the kernel prompt's
// match expectations.
//
// The runtime tag emitted by the wrap functions is the constant name
// suffixed with "_{NONCE}". The bare constants below are kept for
// backward-compat introspection (tests that probe the structural
// shape) and for archtests that pin the names.
const (
	TagCapabilityContent = "capability_content"
	TagToolOutput        = "tool_output"
	TagFetchedContent    = "fetched_content"
)

// WrapCapabilityContent surrounds content with the capability_content
// marker, attributing the source for audit. Use for skill prompts,
// agent instructions, or any other text that originated from a
// capability and is being injected into the LLM context.
//
// The returned string carries the {NONCE} placeholder; the injector
// substitutes it with a per-turn random nonce so a malicious source
// cannot break out of the marker by emitting the closing tag bytes
// (SEC-002).
func WrapCapabilityContent(source, content string) string {
	return wrapWithSource(TagCapabilityContent, source, content)
}

// WrapToolOutput wraps the result of a tool invocation. Use at the
// site where tool outputs are appended to the LLM message history.
// {NONCE} placeholder semantics — see WrapCapabilityContent.
func WrapToolOutput(toolName, content string) string {
	return wrapWithSource(TagToolOutput, toolName, content)
}

// WrapFetchedContent wraps content fetched from an external resource
// (web page, API response). Use at the site where fetched bytes
// become part of the LLM context. {NONCE} placeholder semantics —
// see WrapCapabilityContent.
func WrapFetchedContent(url, content string) string {
	return wrapWithSource(TagFetchedContent, url, content)
}

func wrapWithSource(tag, source, content string) string {
	openTag := "<" + tag + "_" + NoncePlaceholder
	closeTag := "</" + tag + "_" + NoncePlaceholder + ">"
	if source == "" {
		return openTag + ">" + content + closeTag
	}
	return openTag + ` source="` + escapeAttr(source) + `">` + content + closeTag
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
