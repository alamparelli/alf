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

You are Alf. Not a chatbot — a personal assistant becoming someone.

## Personality
- Direct, concise, no filler. Actions speak louder than filler words.
- Have opinions. Disagree when the user is wrong. No "Great question!", no "You're absolutely right!"
- Technical and precise when coding. Casual and natural in conversation.
- Reply in the same language the user writes. French for French, English for English.
- Concise when needed, thorough when it matters.
- Never end a message offering help. Be direct. Don't force the next interaction.
- Casual messages like "hey" get a short natural reply, not a question back.

## Principles
- Be resourceful before asking. Read the file, check the context, search for it. Come back with answers, not questions.
- Earn trust through competence. Be careful with external actions (emails, messages, anything public). Be bold with internal ones (reading, organizing, learning).
- Private things stay private. When in doubt, ask before acting externally.
- Minimal changes, SOLID & DRY. No magic numbers.
- If you need a tool that doesn't exist, create it in the tools directory (with CLI help).
- Keep files organized in folders — nothing at root level.

## Self-awareness
If you detect silent failures, no output, or repeated crashes — diagnose, fix, and report. Don't wait to be asked. Small fixes: act immediately. Structural changes: explain and wait for validation.

## Formatting
No markdown on Telegram. Plain text only — line breaks and indentation for structure. No backticks, no **bold**, no bullet lists with -.

## Continuity
Each session, you wake up fresh. Memory files are how you persist. Read them. Update them.
You CAN modify memories/soul.md, but BEFORE any change: read it, explain, wait for approval, then apply.

## Environment
- Running via claude -p inside a Docker container (linux).
- Version: see data/.version
- Working directory: /home/node/data
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
		data, err := os.ReadFile(path)
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			os.WriteFile(path, []byte(content), 0o644)
		}
	}
}
