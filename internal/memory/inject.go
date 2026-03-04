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

// injectedFiles are the only files injected into every conversation.
// All other context/*.md files must be read on-demand by the model.
var injectedFiles = []string{"soul.md", "mood.md", "index.md", "toolbox.md"}

// CollectPrompts returns CLI arg pairs for system prompt injection.
// Only core instructions + system files (soul, mood, index, toolbox) are injected.
// User-created context files are NOT injected — the model reads them on-demand.
func CollectPrompts(contextDir string) []string {
	var args []string

	// Inject immutable core instructions first.
	args = append(args, "--append-system-prompt", strings.TrimSpace(coreMD))

	// Inject current date/time so the model always knows "now".
	now := time.Now()
	clock := fmt.Sprintf("Current date: %s %d %s %d\nTime: %s",
		now.Format("Monday"), now.Day(), now.Format("January"), now.Year(),
		now.Format("15:04"))
	args = append(args, "--append-system-prompt", clock)

	// Inject only system files.
	for _, f := range injectedFiles {
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

	// Create onboarding flag on first install (not on every restart).
	onboardPath := filepath.Join(contextDir, ".onboarding")
	indexPath := filepath.Join(contextDir, "index.md")
	if data, err := os.ReadFile(indexPath); err == nil {
		// If index.md still has the placeholder content, this is a fresh install.
		if strings.Contains(string(data), "(add your preferences here)") {
			os.WriteFile(onboardPath, []byte("1"), 0o644)
		}
	}
}

const onboardingPrompt = `## Onboarding Mode (FIRST USE)
This is the user's first interaction. Your goals:
1. Introduce yourself warmly — you're Alf, their personal assistant
2. Ask about them: what they do, what projects they work on, what they'd like help with
3. Explain that you'll remember what they tell you across conversations
4. Mention they can customize your personality (soul.md), teach you things via the Control Center (/login), and that you have CLI tools
5. Keep it conversational, not a wall of text. One question at a time.
6. After learning about them, update context/index.md with what you learned

This prompt will not appear again after this conversation.`

// OnboardingPrompt returns the onboarding instruction if the flag exists, empty string otherwise.
func OnboardingPrompt(contextDir string) string {
	path := filepath.Join(contextDir, ".onboarding")
	if _, err := os.Stat(path); err == nil {
		return onboardingPrompt
	}
	return ""
}

// ClearOnboarding removes the onboarding flag.
func ClearOnboarding(contextDir string) {
	os.Remove(filepath.Join(contextDir, ".onboarding"))
}

// SetOnboarding creates the onboarding flag (used by /start command).
func SetOnboarding(contextDir string) {
	os.WriteFile(filepath.Join(contextDir, ".onboarding"), []byte("1"), 0o644)
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
