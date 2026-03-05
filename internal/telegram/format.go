package telegram

import (
	"fmt"
	"regexp"
	"strings"
)

// Telegram's maximum message length.
const MaxMessageLen = 4096

// supportedTags are HTML tags that Telegram's parser accepts.
var supportedTags = map[string]bool{
	"b": true, "i": true, "u": true, "s": true,
	"code": true, "pre": true, "a": true,
}

// MarkdownToHTML converts markdown formatting to Telegram-compatible HTML.
// It handles code blocks, inline code, bold, italic, and headers.
// Claude XML artifacts are stripped before conversion.
func MarkdownToHTML(text string) string {
	// Strip Claude XML artifacts.
	text = stripClaudeXML(text)

	placeholders := map[string]string{}
	phIndex := 0

	placeholder := func(html string) string {
		key := fmt.Sprintf("\x00PH%04d\x00", phIndex)
		phIndex++
		placeholders[key] = html
		return key
	}

	// Step 1: code blocks (```...```) → <pre> placeholder
	codeBlockRe := regexp.MustCompile("(?s)```(?:\\w+)?\\n?(.*?)```")
	text = codeBlockRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := codeBlockRe.FindStringSubmatch(m)
		content := strings.TrimSpace(sub[1])
		content = escapeHTMLEntities(content)
		return placeholder("<pre>" + content + "</pre>")
	})

	// Step 1b: markdown tables → <pre> placeholder
	text = convertTables(text, placeholder)

	// Step 2: inline code (`...`) → <code> placeholder
	inlineCodeRe := regexp.MustCompile("`([^`]+?)`")
	text = inlineCodeRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := inlineCodeRe.FindStringSubmatch(m)
		content := escapeHTMLEntities(sub[1])
		return placeholder("<code>" + content + "</code>")
	})

	// Step 3: bold (**text** or __text__)
	boldRe1 := regexp.MustCompile(`(?s)\*\*(.+?)\*\*`)
	text = boldRe1.ReplaceAllString(text, "<b>$1</b>")
	boldRe2 := regexp.MustCompile(`(?s)__(.+?)__`)
	text = boldRe2.ReplaceAllString(text, "<b>$1</b>")

	// Step 4: italic (*text* or _text_)
	italicRe1 := regexp.MustCompile(`\*([^*]+?)\*`)
	text = italicRe1.ReplaceAllString(text, "<i>$1</i>")
	italicRe2 := regexp.MustCompile(`(?:^|[^_\w])_([^_]+?)_(?:[^_\w]|$)`)
	text = italicRe2.ReplaceAllStringFunc(text, func(m string) string {
		sub := italicRe2.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		// Preserve surrounding chars that aren't part of the _..._
		prefix := ""
		suffix := ""
		idx := strings.Index(m, "_")
		if idx > 0 {
			prefix = m[:idx]
		}
		lastIdx := strings.LastIndex(m, "_")
		if lastIdx < len(m)-1 {
			suffix = m[lastIdx+1:]
		}
		return prefix + "<i>" + sub[1] + "</i>" + suffix
	})

	// Step 5: headers (# ## ###) → bold line
	headerRe := regexp.MustCompile(`(?m)^#{1,3}\s+(.+)$`)
	text = headerRe.ReplaceAllString(text, "<b>$1</b>")

	// Step 6: escape remaining HTML entities (preserve our generated tags)
	validTagRe := regexp.MustCompile(`</?(?:b|i|u|s|a(?:\s[^>]*)?)>`)
	var savedTags []string
	text = validTagRe.ReplaceAllStringFunc(text, func(m string) string {
		idx := len(savedTags)
		savedTags = append(savedTags, m)
		return fmt.Sprintf("\x01TAG%d\x01", idx)
	})
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	for idx, tag := range savedTags {
		text = strings.Replace(text, fmt.Sprintf("\x01TAG%d\x01", idx), tag, 1)
	}

	// Step 7: reinsert code placeholders
	for key, html := range placeholders {
		text = strings.Replace(text, key, html, 1)
	}

	// Step 8: close unclosed <b> and <i> tags
	for _, tag := range []string{"b", "i"} {
		opens := strings.Count(text, "<"+tag+">")
		closes := strings.Count(text, "</"+tag+">")
		if opens > closes {
			text += strings.Repeat("</"+tag+">", opens-closes)
		}
	}

	return text
}

// convertTables detects markdown tables and converts them to <pre> blocks.
// A table is a sequence of lines where each line starts and ends with |.
// The separator row (|---|---|) is stripped and column widths are auto-padded.
func convertTables(text string, placeholder func(string) string) string {
	lines := strings.Split(text, "\n")
	var result []string
	i := 0

	for i < len(lines) {
		// Detect table start: line with | that looks like a header row.
		if isTableRow(lines[i]) && i+1 < len(lines) && isTableSeparator(lines[i+1]) {
			// Collect all contiguous table rows.
			var tableLines []string
			tableLines = append(tableLines, lines[i])
			j := i + 1
			for j < len(lines) && (isTableRow(lines[j]) || isTableSeparator(lines[j])) {
				if !isTableSeparator(lines[j]) {
					tableLines = append(tableLines, lines[j])
				}
				j++
			}
			// Parse cells.
			var rows [][]string
			maxCols := 0
			for _, line := range tableLines {
				cells := parseTableRow(line)
				if len(cells) > maxCols {
					maxCols = len(cells)
				}
				rows = append(rows, cells)
			}

			// Calculate column widths.
			widths := make([]int, maxCols)
			for _, row := range rows {
				for c, cell := range row {
					if len(cell) > widths[c] {
						widths[c] = len(cell)
					}
				}
			}

			// Build formatted table.
			var sb strings.Builder
			for r, row := range rows {
				for c := 0; c < maxCols; c++ {
					cell := ""
					if c < len(row) {
						cell = row[c]
					}
					if c > 0 {
						sb.WriteString(" │ ")
					}
					sb.WriteString(cell)
					// Pad to column width.
					for pad := len(cell); pad < widths[c]; pad++ {
						sb.WriteByte(' ')
					}
				}
				sb.WriteByte('\n')
				// Add separator after header.
				if r == 0 {
					for c := 0; c < maxCols; c++ {
						if c > 0 {
							sb.WriteString("─┼─")
						}
						sb.WriteString(strings.Repeat("─", widths[c]))
					}
					sb.WriteByte('\n')
				}
			}

			pre := "<pre>" + escapeHTMLEntities(strings.TrimRight(sb.String(), "\n")) + "</pre>"
			result = append(result, placeholder(pre))
			i = j
		} else {
			result = append(result, lines[i])
			i++
		}
	}

	return strings.Join(result, "\n")
}

// isTableRow checks if a line looks like a markdown table row (has | separators).
// Supports both |col|col| and col | col formats.
func isTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	// Classic format: |col|col|
	if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") && strings.Count(trimmed, "|") >= 3 {
		return true
	}
	// Unenclosed format: col | col | col (at least 2 pipes with content between)
	if strings.Count(trimmed, "|") >= 2 {
		parts := strings.Split(trimmed, "|")
		// All parts must have non-empty content (not just whitespace).
		for _, p := range parts {
			if strings.TrimSpace(p) == "" {
				return false
			}
		}
		return true
	}
	return false
}

// isTableSeparator checks for |---|---| or ---|--- style separator rows.
func isTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") || !strings.Contains(trimmed, "-") {
		return false
	}
	// Remove pipes, dashes, colons, and spaces — should leave nothing.
	inner := strings.ReplaceAll(trimmed, "|", "")
	inner = strings.ReplaceAll(inner, "-", "")
	inner = strings.ReplaceAll(inner, ":", "")
	inner = strings.TrimSpace(inner)
	return inner == ""
}

// parseTableRow splits a | delimited row into trimmed cells.
// Handles both |col|col| and col | col formats.
func parseTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	// Remove leading and trailing | if present.
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

// ValidateHTML checks that html has balanced Telegram-supported tags.
// Returns (true, "") if valid, or (false, reason) if not.
func ValidateHTML(html string) (bool, string) {
	tagRe := regexp.MustCompile(`<(/?)(\w+)(?:\s[^>]*)?>`)
	var stack []string

	for _, match := range tagRe.FindAllStringSubmatch(html, -1) {
		closing := match[1] == "/"
		tag := strings.ToLower(match[2])
		if !supportedTags[tag] {
			continue
		}
		if closing {
			if len(stack) == 0 {
				return false, fmt.Sprintf("unexpected closing tag </%s> with empty stack", tag)
			}
			if stack[len(stack)-1] != tag {
				return false, fmt.Sprintf("mismatched tag: expected </%s>, got </%s>", stack[len(stack)-1], tag)
			}
			stack = stack[:len(stack)-1]
		} else {
			stack = append(stack, tag)
		}
	}

	if len(stack) > 0 {
		tags := make([]string, len(stack))
		for i, t := range stack {
			tags[i] = "<" + t + ">"
		}
		return false, fmt.Sprintf("unclosed tag(s): %s", strings.Join(tags, ", "))
	}
	return true, ""
}

// ChunkHTML splits text into chunks of at most maxLen characters while
// keeping Telegram HTML tags balanced across chunk boundaries.
func ChunkHTML(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	tagRe := regexp.MustCompile(`(?i)<(/?)(\w+)(?:[^>]*)?>`)

	// trackedTagsAt returns the stack of open tags up to pos.
	trackedTagsAt := func(pos int) []string {
		var stack []string
		for _, m := range tagRe.FindAllStringSubmatchIndex(text[:pos], -1) {
			full := text[m[0]:m[1]]
			sub := tagRe.FindStringSubmatch(full)
			tag := strings.ToLower(sub[2])
			if !supportedTags[tag] {
				continue
			}
			if sub[1] == "/" {
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i] == tag {
						stack = append(stack[:i], stack[i+1:]...)
						break
					}
				}
			} else {
				stack = append(stack, tag)
			}
		}
		return stack
	}

	suffixFor := func(tags []string) string {
		var b strings.Builder
		for i := len(tags) - 1; i >= 0; i-- {
			b.WriteString("</" + tags[i] + ">")
		}
		return b.String()
	}

	prefixFor := func(tags []string) string {
		var b strings.Builder
		for _, t := range tags {
			b.WriteString("<" + t + ">")
		}
		return b.String()
	}

	findSafeEnd := func(start, end int) int {
		if end > len(text) {
			end = len(text)
		}
		chunk := text[start:end]
		lastOpen := strings.LastIndex(chunk, "<")
		if lastOpen != -1 && !strings.Contains(chunk[lastOpen:], ">") {
			end = start + lastOpen
		}
		if end <= start {
			return start + 1
		}
		return end
	}

	findSplitPos := func(start, budget int) int {
		end := findSafeEnd(start, start+budget)
		if end <= start {
			return start + 1
		}
		region := text[start:end]
		if nl := strings.LastIndex(region, "\n"); nl > 0 {
			return start + nl + 1
		}
		if ws := strings.LastIndex(region, " "); ws > 0 {
			return start + ws + 1
		}
		return end
	}

	var chunks []string
	pos := 0
	n := len(text)

	for pos < n {
		openTags := trackedTagsAt(pos)
		prefix := prefixFor(openTags)

		remaining := n - pos
		if len(prefix)+remaining <= maxLen {
			if len(openTags) > 0 {
				chunks = append(chunks, prefix+text[pos:])
			} else {
				chunks = append(chunks, text[pos:])
			}
			break
		}

		// Two-pass: find split, check if suffix fits, shrink if needed.
		var chunkText string
		var splitPos int
		for attempt := 0; attempt < 2; attempt++ {
			budget := maxLen - len(prefix)
			if attempt == 1 {
				scanEnd := pos + budget
				if scanEnd > n {
					scanEnd = n
				}
				tags := trackedTagsAt(scanEnd)
				worst := suffixFor(tags)
				budget = maxLen - len(prefix) - len(worst)
				if budget < 1 {
					budget = 1
				}
			}

			splitPos = findSplitPos(pos, budget)
			if splitPos <= pos {
				splitPos = pos + 1
			}
			if splitPos > n {
				splitPos = n
			}

			raw := text[pos:splitPos]
			openAtEnd := trackedTagsAt(splitPos)
			suffix := suffixFor(openAtEnd)
			chunkText = prefix + raw + suffix

			if len(chunkText) <= maxLen || attempt == 1 {
				break
			}
		}

		chunks = append(chunks, chunkText)
		pos = splitPos
	}

	if len(chunks) == 0 {
		return []string{text}
	}
	return chunks
}

// StripHTML removes all HTML/XML tags and decodes entities for plain-text fallback.
func StripHTML(text string) string {
	// Strip Claude XML blocks first.
	text = stripClaudeXML(text)
	// Remove all remaining tags.
	tagRe := regexp.MustCompile(`<[^>]+>`)
	text = tagRe.ReplaceAllString(text, "")
	// Decode entities.
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	return text
}

// EscapeHTML escapes &, <, > for safe inclusion in HTML.
func EscapeHTML(text string) string {
	return escapeHTMLEntities(text)
}

// stripClaudeXML removes Claude-specific XML artifacts from text.
func stripClaudeXML(text string) string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?s)<function_calls>.*?</function_calls>`),
		regexp.MustCompile(`(?s)<invoke>.*?</invoke>`),
		regexp.MustCompile(`(?s)<tool_use>.*?</tool_use>`),
		regexp.MustCompile(`</?[a-z_]+(?:\s[^>]*)?\s*/?>`),
	}
	for _, re := range patterns {
		text = re.ReplaceAllString(text, "")
	}
	return text
}

// escapeHTMLEntities escapes &, <, > characters.
func escapeHTMLEntities(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}
