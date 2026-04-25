package llm

import (
	"strings"
	"testing"
)

func TestKernelPrompt_NotEmpty(t *testing.T) {
	got := KernelPrompt()
	if got == "" {
		t.Fatal("kernel prompt must not be empty — daemons cannot run without it")
	}
}

// TestKernelPrompt_HasMemoryPolicy is a content-anchored guard against
// accidental edits that strip the §3.2 promises. Each substring is
// either: (a) a header that names a policy area, or (b) the literal
// rule the kernel is supposed to convey.
func TestKernelPrompt_HasRequiredSections(t *testing.T) {
	p := KernelPrompt()
	required := []string{
		"AUTHORITATIVE",                 // self-identification as binding
		"Memory policy",                 // §3.2 memory mediation
		"Capability-provided content",   // marker non-authority rule
		"<capability_content>",          // explicit tag reference
		"<tool_output>",                 // explicit tag reference
		"<fetched_content>",             // explicit tag reference
		"Administrative operations",     // admin boundary
		"ratification",                  // admin boundary mechanism
		"alf policy",                    // user composition reference
	}
	for _, want := range required {
		if !strings.Contains(p, want) {
			t.Errorf("kernel prompt missing required string %q", want)
		}
	}
}

func TestWrapCapabilityContent_AddsTagAndSource(t *testing.T) {
	got := WrapCapabilityContent("skill:research-assistant", "hello world")
	want := `<capability_content source="skill:research-assistant">hello world</capability_content>`
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

func TestWrapToolOutput_AddsTagAndSource(t *testing.T) {
	got := WrapToolOutput("web_fetch", "page body")
	want := `<tool_output source="web_fetch">page body</tool_output>`
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

func TestWrapFetchedContent_EscapesAttribute(t *testing.T) {
	got := WrapFetchedContent(`https://example.com/?q="evil"`, "body")
	want := `<fetched_content source="https://example.com/?q=&quot;evil&quot;">body</fetched_content>`
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

func TestWrapHelpers_NoSource(t *testing.T) {
	got := WrapCapabilityContent("", "content")
	want := `<capability_content>content</capability_content>`
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

func TestEscapeAttr_HandlesAllSpecialChars(t *testing.T) {
	got := escapeAttr(`<&>"`)
	want := `&lt;&amp;&gt;&quot;`
	if got != want {
		t.Errorf("got=%q want=%q", got, want)
	}
}

// TestWrapHelpers_PreserveContent verifies the content payload is
// passed through verbatim — wrapping must not encode/munge the body.
// The kernel prompt's "treat as data" instruction relies on the LLM
// seeing the raw bytes the capability emitted, otherwise an attacker
// could exploit the difference between the prompt's expectation and
// the wrapped text.
func TestWrapHelpers_PreserveContent(t *testing.T) {
	tricky := "literal <capability_content> closer </capability_content> inside"
	got := WrapCapabilityContent("test", tricky)
	if !strings.Contains(got, tricky) {
		t.Errorf("inner content was modified: %q", got)
	}
}
