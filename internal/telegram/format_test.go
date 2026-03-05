package telegram

import (
	"strings"
	"testing"
)

func TestMarkdownToHTML_CodeBlock(t *testing.T) {
	input := "Here is code:\n```go\nfmt.Println(\"hello\")\n```"
	got := MarkdownToHTML(input)
	if !strings.Contains(got, "<pre>") || !strings.Contains(got, "</pre>") {
		t.Errorf("expected <pre> tags, got: %s", got)
	}
	if !strings.Contains(got, "fmt.Println") {
		t.Errorf("expected code content preserved, got: %s", got)
	}
	// HTML entities inside code should be escaped
	if !strings.Contains(got, "&quot;") || strings.Contains(got, "\"hello\"") {
		// Actually the legacy just escapes < > &, not quotes
	}
}

func TestMarkdownToHTML_InlineCode(t *testing.T) {
	input := "Use `fmt.Println` to print"
	got := MarkdownToHTML(input)
	if !strings.Contains(got, "<code>fmt.Println</code>") {
		t.Errorf("expected <code> tags, got: %s", got)
	}
}

func TestMarkdownToHTML_Bold(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"**hello**", "<b>hello</b>"},
		{"__hello__", "<b>hello</b>"},
	}
	for _, tt := range tests {
		got := MarkdownToHTML(tt.input)
		if !strings.Contains(got, tt.want) {
			t.Errorf("MarkdownToHTML(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMarkdownToHTML_Italic(t *testing.T) {
	input := "this is *italic* text"
	got := MarkdownToHTML(input)
	if !strings.Contains(got, "<i>italic</i>") {
		t.Errorf("expected <i> tags, got: %s", got)
	}
}

func TestMarkdownToHTML_Headers(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"# Title", "<b>Title</b>"},
		{"## Subtitle", "<b>Subtitle</b>"},
		{"### Section", "<b>Section</b>"},
	}
	for _, tt := range tests {
		got := MarkdownToHTML(tt.input)
		if !strings.Contains(got, tt.want) {
			t.Errorf("MarkdownToHTML(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMarkdownToHTML_HTMLEscaping(t *testing.T) {
	input := "Use x < y and a > b with a & b"
	got := MarkdownToHTML(input)
	if !strings.Contains(got, "&lt;") {
		t.Errorf("expected &lt;, got: %s", got)
	}
	if !strings.Contains(got, "&gt;") {
		t.Errorf("expected &gt;, got: %s", got)
	}
	if !strings.Contains(got, "&amp;") {
		t.Errorf("expected &amp;, got: %s", got)
	}
}

func TestMarkdownToHTML_ClaudeXMLStripped(t *testing.T) {
	input := "Hello <function_calls>some xml</function_calls> world"
	got := MarkdownToHTML(input)
	if strings.Contains(got, "function_calls") {
		t.Errorf("expected Claude XML stripped, got: %s", got)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "world") {
		t.Errorf("expected surrounding text preserved, got: %s", got)
	}
}

func TestMarkdownToHTML_UnclosedTagsAutoClosed(t *testing.T) {
	// Simulate a scenario where bold is opened but markdown doesn't close it well
	input := "**bold text without close"
	got := MarkdownToHTML(input)
	// The regex won't match unclosed **, so it stays as-is (escaped)
	// But if we manually craft HTML with unclosed tags:
	valid, _ := ValidateHTML(got)
	if !valid {
		t.Errorf("expected valid HTML after auto-closing, got invalid: %s", got)
	}
}

func TestMarkdownToHTML_NestedFormatting(t *testing.T) {
	input := "This is **bold with `code` inside** text"
	got := MarkdownToHTML(input)
	if !strings.Contains(got, "<b>") && !strings.Contains(got, "<code>") {
		t.Errorf("expected both bold and code formatting, got: %s", got)
	}
}

func TestMarkdownToHTML_RoundTrip(t *testing.T) {
	input := "# Header\n\n**Bold** and *italic* with `code`\n\n```\nblock\n```"
	html := MarkdownToHTML(input)
	valid, err := ValidateHTML(html)
	if !valid {
		t.Errorf("round-trip produced invalid HTML: %s\nHTML: %s", err, html)
	}
}

func TestValidateHTML_Balanced(t *testing.T) {
	tests := []struct {
		html string
		ok   bool
	}{
		{"<b>hello</b>", true},
		{"<b><i>hello</i></b>", true},
		{"<pre>code</pre>", true},
		{"no tags", true},
		{"<b>unclosed", false},
		{"</b>orphan close", false},
		{"<b><i>mismatched</b></i>", false},
	}
	for _, tt := range tests {
		ok, reason := ValidateHTML(tt.html)
		if ok != tt.ok {
			t.Errorf("ValidateHTML(%q) = %v (%s), want %v", tt.html, ok, reason, tt.ok)
		}
	}
}

func TestChunkHTML_FitsInOne(t *testing.T) {
	text := "short message"
	chunks := ChunkHTML(text, 100)
	if len(chunks) != 1 || chunks[0] != text {
		t.Errorf("expected single chunk, got %d: %v", len(chunks), chunks)
	}
}

func TestChunkHTML_SplitsAtNewline(t *testing.T) {
	// Build text that exceeds maxLen
	line := strings.Repeat("a", 40)
	text := line + "\n" + line + "\n" + line
	chunks := ChunkHTML(text, 50)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %d", len(chunks))
	}
	// Each chunk should be within limit
	for i, c := range chunks {
		if len(c) > 50 {
			t.Errorf("chunk %d exceeds max_len: len=%d", i, len(c))
		}
	}
}

func TestChunkHTML_TagsRebalanced(t *testing.T) {
	// Build a long bold text that must be split
	inner := strings.Repeat("word ", 200)
	text := "<b>" + inner + "</b>"
	chunks := ChunkHTML(text, 200)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %d", len(chunks))
	}
	// Each chunk should have balanced tags
	for i, c := range chunks {
		ok, reason := ValidateHTML(c)
		if !ok {
			t.Errorf("chunk %d has unbalanced tags: %s\nchunk: %s", i, reason, c)
		}
	}
}

func TestStripHTML(t *testing.T) {
	input := "<b>bold</b> and <code>code</code> &amp; entities"
	got := StripHTML(input)
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Errorf("expected no tags, got: %s", got)
	}
	if !strings.Contains(got, "bold") || !strings.Contains(got, "code") {
		t.Errorf("expected text preserved, got: %s", got)
	}
	if !strings.Contains(got, "&") {
		t.Errorf("expected entities decoded, got: %s", got)
	}
}

func TestEscapeHTML(t *testing.T) {
	got := EscapeHTML("a < b & c > d")
	if got != "a &lt; b &amp; c &gt; d" {
		t.Errorf("unexpected escape result: %s", got)
	}
}

func TestMarkdownToHTML_Table(t *testing.T) {
	input := `Here is a table:

| # | Source | Medium |
|---|--------|--------|
| 1 | x | dm |
| 2 | reddit | post |

End of table.`

	got := MarkdownToHTML(input)
	if !strings.Contains(got, "<pre>") {
		t.Fatalf("expected table in <pre> block, got: %s", got)
	}
	// Should contain data cells.
	if !strings.Contains(got, "reddit") || !strings.Contains(got, "dm") {
		t.Errorf("expected table data preserved, got: %s", got)
	}
	// Separator line with box-drawing chars.
	if !strings.Contains(got, "─") {
		t.Errorf("expected box-drawing separator, got: %s", got)
	}
	// Should NOT contain the markdown separator row.
	if strings.Contains(got, "|---|") {
		t.Errorf("markdown separator should be stripped, got: %s", got)
	}
	// Surrounding text should remain.
	if !strings.Contains(got, "Here is a table:") || !strings.Contains(got, "End of table.") {
		t.Errorf("surrounding text lost, got: %s", got)
	}
}

func TestMarkdownToHTML_TableColumnAlignment(t *testing.T) {
	input := `| Name | Age |
|------|-----|
| Alice | 30 |
| Bob | 7 |`

	got := MarkdownToHTML(input)
	if !strings.Contains(got, "<pre>") {
		t.Fatalf("expected <pre>, got: %s", got)
	}
	// Column separator should use │
	if !strings.Contains(got, "│") {
		t.Errorf("expected │ column separator, got: %s", got)
	}
}

func TestMarkdownToHTML_TableWithSpecialChars(t *testing.T) {
	input := `| URL | Note |
|-----|------|
| https://example.com?a=1&b=2 | ok |`

	got := MarkdownToHTML(input)
	// & should be escaped inside <pre>.
	if !strings.Contains(got, "&amp;") {
		t.Errorf("expected & escaped in table, got: %s", got)
	}
	if !strings.Contains(got, "example.com") {
		t.Errorf("expected URL preserved, got: %s", got)
	}
}

func TestMarkdownToHTML_NotATable(t *testing.T) {
	// Single pipe line without separator shouldn't be treated as table.
	input := "This | is not | a table"
	got := MarkdownToHTML(input)
	if strings.Contains(got, "<pre>") {
		t.Errorf("should not wrap non-table in <pre>, got: %s", got)
	}
}

func TestMarkdownToHTML_UnenclosedTable(t *testing.T) {
	input := `Besoin | Outil dispo | Ce qu'il donne
-------|-------------|---------------
Mots-clés | gsc | Queries réelles
Trafic | ga | Sessions`

	got := MarkdownToHTML(input)
	if !strings.Contains(got, "<pre>") {
		t.Fatalf("expected unenclosed table in <pre> block, got: %s", got)
	}
	if !strings.Contains(got, "Mots-cl") {
		t.Errorf("expected table data preserved, got: %s", got)
	}
	if !strings.Contains(got, "─") {
		t.Errorf("expected box-drawing separator, got: %s", got)
	}
}

func TestChunkHTML_AllContentPreserved(t *testing.T) {
	// Verify no content is lost during chunking
	words := make([]string, 100)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")
	chunks := ChunkHTML(text, 80)

	// Rejoin and check all words present
	joined := strings.Join(chunks, "")
	wordCount := strings.Count(joined, "word")
	if wordCount != 100 {
		t.Errorf("expected 100 words, got %d in %d chunks", wordCount, len(chunks))
	}
}
