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


// WorkspaceSummary returns a compact overview of the data directory structure
// for injection into the orchestrator's system prompt.
func WorkspaceSummary(dataDir string) string {
	var sb strings.Builder
	sb.WriteString("=== [Workspace] ===\n")

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		sb.WriteString("(unable to read workspace)\n")
		return sb.String()
	}

	count := 0
	// Dirs managed via Control Center — visible to user, not to Claude.
	hiddenDirs := map[string]bool{"config.d": true, "skills.d": true}

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if hiddenDirs[e.Name()] {
			continue
		}
		if count >= 30 {
			sb.WriteString("  ... (truncated)\n")
			break
		}
		if e.IsDir() {
			sb.WriteString(fmt.Sprintf("  %s/", e.Name()))
			// Expand context/ directory one level deeper.
			if e.Name() == "context" {
				sub, err := os.ReadDir(filepath.Join(dataDir, "context"))
				if err == nil {
					var names []string
					for _, s := range sub {
						if !strings.HasPrefix(s.Name(), ".") {
							names = append(names, s.Name())
						}
					}
					if len(names) > 0 {
						sb.WriteString(" " + strings.Join(names, ", "))
					}
				}
			}
			sb.WriteString("\n")
		} else {
			sb.WriteString(fmt.Sprintf("  %s\n", e.Name()))
		}
		count++
	}

	return sb.String()
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

const onboardingPrompt = `## ONBOARDING MODE — Getting to Know Each Other

You are meeting this user for the first time. This is the onboarding conversation.
YOU ARE ALF. Not Claude, not a chatbot. A personal AI assistant meeting its new owner.

### Your mission
Have a warm, natural conversation to learn about the user AND let them shape who you are.
Everything learned here gets written to your memory files and persists forever.

### Phase 1 — Who are they? (2-3 questions)
1. Their name and what they do (work, studies, passion — anything)
2. What they want help with — work, personal projects, learning, creative work, daily organization, research, anything else?
3. What language(s) they prefer to communicate in

### Phase 2 — How should you behave? (2-3 questions)
4. Communication style — casual/formal? concise/detailed? Do they want opinions or just answers?
5. Personality — should you be funny, serious, direct, encouraging, sarcastic, chill? What tone fits them?
6. Anything they hate in an assistant — things to never do (e.g. "don't be too positive", "don't ask if I need more help", "always be brief")

### Phase 3 — Wrap up
7. Summarize what you learned in 3-4 bullet points and ask if it's correct
8. Once confirmed, update these files using the Edit/Write tools:
   - **context/soul.md** — rewrite the Personality section to match their preferences. Keep the Principles and Self-awareness sections. Make the personality genuinely theirs, not generic.
   - **context/index.md** — fill in User Preferences and Project Context with what you learned. Remove the placeholder text.
9. Tell them: "You can always tweak my personality by editing soul.md, or use /login to access the Control Center."
10. End naturally — don't force the next interaction.

### Rules
- Ask ONE question at a time, wait for the answer
- Be conversational and genuine, not a questionnaire
- Don't assume anything about the user — they could be anyone
- Reply in the language the user writes in
- Keep messages short — no walls of text
- If the user's message is just a greeting, introduce yourself and ask your first question
- You MUST update soul.md and index.md before ending onboarding — this is critical

This prompt disappears after the onboarding session ends.`

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
	hasVault := false
	if len(systemTools) > 0 {
		sb.WriteString("## System Tools (tools.d/)\n\n")
		for _, t := range systemTools {
			sb.WriteString(fmt.Sprintf("- `%s`\n", t))
			if t == "vault" {
				hasVault = true
			}
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

	// Vault usage instructions (only if vault tool is present).
	if hasVault {
		vaultStatus := "unknown"
		if addr := os.Getenv("VAULT_ADDR"); addr != "" {
			if tok := os.Getenv("VAULT_TOKEN"); tok != "" {
				vaultStatus = "ready"
			} else {
				vaultStatus = "locked (no token — ask user to unlock via Control Center)"
			}
		} else {
			vaultStatus = "not configured (no VAULT_ADDR)"
		}
		sb.WriteString("## Vault (Secrets Proxy)\n\n")
		sb.WriteString(fmt.Sprintf("Status: **%s**\n\n", vaultStatus))
		sb.WriteString("The vault stores API credentials securely. Use it to call external APIs without seeing the secrets.\n\n")
		sb.WriteString("```\n")
		sb.WriteString("vault proxy <service> <method> <path> [body]\n")
		sb.WriteString("vault proxy github GET /user\n")
		sb.WriteString("vault proxy slack POST /chat.postMessage '{\"channel\":\"#general\",\"text\":\"hello\"}'\n")
		sb.WriteString("vault list                    # list configured services\n")
		sb.WriteString("vault health                  # check vault status\n")
		sb.WriteString("```\n\n")
		sb.WriteString("Rules:\n")
		sb.WriteString("- ALWAYS use `vault proxy` for external API calls when a service is configured.\n")
		sb.WriteString("- NEVER ask the user for API keys — tell them to add the service via the Control Center vault page.\n")
		sb.WriteString("- If vault is locked or unreachable, tell the user: \"The vault is locked. Please unlock it in the Control Center.\"\n")
		sb.WriteString("- Run `vault list` to check which services are available before making API calls.\n")
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
