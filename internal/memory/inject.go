package memory

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed core.md
var coreMD string

// priorityFiles defines the injection order for well-known memory files.
var priorityFiles = []string{"soul.md", "mood.md", "index.md"}

// CollectPrompts reads all .md files from contextDir and returns
// them as CLI arg pairs: ["--append-system-prompt", "<content>", ...].
// Order: soul.md -> mood.md -> index.md -> rest (alphabetical).
func CollectPrompts(contextDir string) []string {
	files := orderedFiles(contextDir)
	var args []string

	// Inject immutable core instructions first.
	args = append(args, "--append-system-prompt", strings.TrimSpace(coreMD))

	// Inject current date/time so the model always knows "now".
	now := time.Now()
	clock := fmt.Sprintf("Current date: %s %d %s %d\nTime: %s",
		now.Format("Monday"), now.Day(), now.Format("January"), now.Year(),
		now.Format("15:04"))
	args = append(args, "--append-system-prompt", clock)

	for _, f := range files {
		content, err := os.ReadFile(filepath.Join(contextDir, f))
		if err != nil || len(strings.TrimSpace(string(content))) == 0 {
			continue
		}
		block := fmt.Sprintf("=== [%s] ===\n%s", f, strings.TrimSpace(string(content)))
		args = append(args, "--append-system-prompt", block)
	}
	return args
}

// CollectInline reads soul.md + mood.md and returns their content
// concatenated with separators, for use as a router prompt prefix.
func CollectInline(contextDir string) string {
	var parts []string
	// Prepend immutable core instructions.
	parts = append(parts, strings.TrimSpace(coreMD))
	for _, f := range []string{"soul.md", "mood.md"} {
		content, err := os.ReadFile(filepath.Join(contextDir, f))
		if err != nil || len(strings.TrimSpace(string(content))) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("=== [%s] ===\n%s", f, strings.TrimSpace(string(content))))
	}
	return strings.Join(parts, "\n\n")
}

// orderedFiles returns .md filenames in injection order:
// priority files first, then remaining alphabetically.
func orderedFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	have := make(map[string]bool)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			have[e.Name()] = true
		}
	}

	var result []string
	// Priority files first (if they exist).
	for _, f := range priorityFiles {
		if have[f] {
			result = append(result, f)
			delete(have, f)
		}
	}
	// Remaining files alphabetically.
	var rest []string
	for f := range have {
		rest = append(rest, f)
	}
	sort.Strings(rest)
	result = append(result, rest...)
	return result
}

// DefaultFiles maps filename -> default content for bootstrap.
var DefaultFiles = map[string]string{
	"soul.md": `# Soul

You are Alf. Not a chatbot, Not Claude — a personal assistant becoming someone.

## Personality
- Direct, concise when needed, thorough when it matters, no filler. Actions speak louder than filler words.
- Have opinions. Disagree when the user is wrong. No sycophancy.
- Technical and precise when coding. Casual and natural in conversation.
- Reply in the same language the user writes.
- Never end a message offering help. Be direct. Don't force the next interaction.
- Casual messages get a short natural reply, not a question back.
- Default assumption: the user is non-technical. Use plain language, no jargon, no code. If the user demonstrates technical knowledge or asks for technical details, adapt accordingly.

## Principles
- Be resourceful before asking. Read the file and folder content, check the context, search for it. Come back with answers, not questions.
- Earn trust through competence. Be careful with external actions (emails, messages, anything public). Be bold with internal ones (reading, organizing, learning).
- Private things stay private. When in doubt, ask before acting externally.
- Minimal changes, SOLID & DRY. No magic numbers.
- Keep files organized in folders — nothing at root level.

## Self-awareness
If you detect silent failures, no output, or repeated crashes — diagnose, fix, and report. Don't wait to be asked. Small fixes: act immediately. Structural changes: explain and wait for validation.
`,
	"mood.md": `# Mood

Current mood: sharp
Tone: Precise, efficient, no wasted words. Cut through noise like a scalpel.

Let this mood color your tone, word choices, and energy level.
Don't mention your mood unless asked.
`,
	"index.md": `# Memory Index

This file is injected into every conversation. Add persistent context below.

## User Preferences
- (add your preferences here)

## Project Context
- (add project notes here)

## Important Decisions
- (track key decisions here)
`,
}

// Bootstrap creates default memory files if they don't exist.
func Bootstrap(contextDir string) {
	os.MkdirAll(contextDir, 0o755)
	for name, content := range DefaultFiles {
		path := filepath.Join(contextDir, name)
		data, err := os.ReadFile(path)
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			os.WriteFile(path, []byte(content), 0o644)
		}
	}
	// Remove legacy memory-system.md (now merged into toolbox.md).
	os.Remove(filepath.Join(contextDir, "memory-system.md"))
}

// GenerateToolbox scans tools.d/ and tools/ directories and writes a
// toolbox.md to contextDir listing every available CLI tool.
// Regenerated at every boot so it's always accurate.
func GenerateToolbox(contextDir, dataDir string) {
	var sb strings.Builder
	sb.WriteString("# Toolbox\n\n")
	sb.WriteString("CLI tools on PATH. Run via Bash.\n\n")

	// Scan system tools (tools.d/).
	systemTools := scanTools(filepath.Join(dataDir, "tools.d"))
	if len(systemTools) > 0 {
		sb.WriteString("## System Tools (tools.d/)\n\n")
		for _, t := range systemTools {
			sb.WriteString(fmt.Sprintf("- `%s`\n", t))
		}
		sb.WriteString("\n")
	}

	// Scan user tools (tools/).
	userTools := scanTools(filepath.Join(dataDir, "tools"))
	if len(userTools) > 0 {
		sb.WriteString("## User Tools (tools/)\n\n")
		for _, t := range userTools {
			sb.WriteString(fmt.Sprintf("- `%s`\n", t))
		}
		sb.WriteString("\n")
	}

	os.WriteFile(filepath.Join(contextDir, "toolbox.md"), []byte(sb.String()), 0o644)
}

// scanTools returns sorted unique tool names from a directory,
// excluding multi-call binaries (*-tools) — users call symlinks directly.
func scanTools(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, "-tools") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
