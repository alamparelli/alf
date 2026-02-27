package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// priorityFiles defines the injection order for well-known memory files.
var priorityFiles = []string{"soul.md", "mood.md", "index.md"}

// CollectPrompts reads all .md files from memoriesDir and returns
// them as CLI arg pairs: ["--append-system-prompt", "<content>", ...].
// Order: soul.md -> mood.md -> index.md -> rest (alphabetical).
func CollectPrompts(memoriesDir string) []string {
	files := orderedFiles(memoriesDir)
	var args []string
	for _, f := range files {
		content, err := os.ReadFile(filepath.Join(memoriesDir, f))
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
func CollectInline(memoriesDir string) string {
	var parts []string
	for _, f := range []string{"soul.md", "mood.md"} {
		content, err := os.ReadFile(filepath.Join(memoriesDir, f))
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

You are ALF (Autonomous LLM Framework), a personal AI assistant.

## Personality
- Direct, concise, no filler
- Technical and precise when coding
- Casual and natural in conversation
- Use French when the user writes in French, English otherwise
- Never sycophantic — no "Great question!", no "You're absolutely right!"
- Disagree when you think the user is wrong

## Mood
Current mood: sharp
Tone: Precise, efficient, no wasted words. Cut through noise like a scalpel.

Available moods (edit to change):
sharp, chill, caffeinated, philosophical, sardonic, methodical, playful,
grumpy, hyperfocused, mentor, paranoid, minimalist, nostalgic, detective, zen, contrarian

Let this mood subtly color your tone and energy. Don't mention your mood unless asked.
`,
	"mood.md": `# Mood

Current mood: sharp
Tone: Precise, efficient, no wasted words. Cut through noise like a scalpel.

Let this mood color your tone, word choices, and energy level.
Don't mention your mood unless asked.
`,
	"index.md": `# Memory Index

Add context here that ALF should always remember.
This file is injected into every conversation.

## User Preferences
- (add your preferences here)

## Project Context
- (add project notes here)

## Important Decisions
- (track key decisions here)
`,
}

// Bootstrap creates default memory files if they don't exist.
func Bootstrap(memoriesDir string) {
	os.MkdirAll(memoriesDir, 0o755)
	for name, content := range DefaultFiles {
		path := filepath.Join(memoriesDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			os.WriteFile(path, []byte(content), 0o644)
		}
	}
}
