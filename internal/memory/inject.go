package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PromptConfig controls conditional sections in core.md.
type PromptConfig struct {
	Backend string // "cli" or "api" - determines tool instructions
	Channel string // "tg" or "cc" - determines formatting rules
	Weight  string // "light", "standard", "full" (default) - controls context density
}

// injectedFiles are the only files injected into every conversation.
// All other context/*.md files must be read on-demand by the model.
var injectedFiles = []string{"soul.md", "mood.md", "index.md", "preferences.md", "toolbox.md"}

// knownTags lists the conditional section tags we support.
var knownTags = []string{"cli", "api", "tg", "cc"}

// weightOrder defines the inclusion hierarchy for context weights.
// "light" < "standard" < "full" — a section tagged "standard" is included
// when weight is "standard" or "full", but excluded for "light".
var weightOrder = map[string]int{"light": 0, "standard": 1, "full": 2}

// filterSections processes conditional blocks in prompt text.
// Blocks tagged with <!-- @begin X --> ... <!-- @end X --> are included only
// if X matches the backend or channel in cfg. Untagged content is always included.
// Blocks tagged with <!-- @weight W --> ... <!-- @end weight --> are included
// only if cfg.Weight >= W in the weight hierarchy.
func filterSections(content string, cfg PromptConfig) string {
	result := content

	// Filter backend/channel tags.
	for _, tag := range knownTags {
		beginMarker := "<!-- @begin " + tag + " -->"
		endMarker := "<!-- @end " + tag + " -->"
		include := (tag == cfg.Backend) || (tag == cfg.Channel)

		for {
			start := strings.Index(result, beginMarker)
			if start == -1 {
				break
			}
			end := strings.Index(result[start:], endMarker)
			if end == -1 {
				break
			}
			end += start + len(endMarker)
			body := result[start+len(beginMarker) : end-len(endMarker)]
			body = strings.TrimPrefix(body, "\n")
			body = strings.TrimRight(body, "\n")

			if include {
				result = result[:start] + body + result[end:]
			} else {
				result = result[:start] + result[end:]
			}
		}
	}

	// Filter @weight tags.
	cfgWeight := cfg.Weight
	if cfgWeight == "" {
		cfgWeight = "full"
	}
	cfgLevel := weightOrder[cfgWeight]
	for _, w := range []string{"standard", "full"} {
		beginMarker := "<!-- @weight " + w + " -->"
		endMarker := "<!-- @end weight -->"
		include := cfgLevel >= weightOrder[w]

		for {
			start := strings.Index(result, beginMarker)
			if start == -1 {
				break
			}
			end := strings.Index(result[start:], endMarker)
			if end == -1 {
				break
			}
			end += start + len(endMarker)
			body := result[start+len(beginMarker) : end-len(endMarker)]
			body = strings.TrimPrefix(body, "\n")
			body = strings.TrimRight(body, "\n")

			if include {
				result = result[:start] + body + result[end:]
			} else {
				result = result[:start] + result[end:]
			}
		}
	}

	// Clean up multiple blank lines left by removed sections.
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return result
}

// CollectPrompts returns system prompt strings for injection.
// cfg controls which conditional sections are included.
func CollectPrompts(contextDir string, cfg PromptConfig) []string {
	var prompts []string

	// Inject immutable core instructions (filtered by backend/channel/weight).
	filtered := filterSections(strings.TrimSpace(coreMD), cfg)
	prompts = append(prompts, filtered)

	// Inject current date/time so the model always knows "now".
	now := time.Now()
	clock := fmt.Sprintf("Current date: %s %d %s %d\nTime: %s",
		now.Format("Monday"), now.Day(), now.Format("January"), now.Year(),
		now.Format("15:04"))
	prompts = append(prompts, clock)

	// Determine which files to inject based on weight.
	// Light tiers skip toolbox.md (tools are in the system prompt or not needed).
	files := injectedFiles
	if cfg.Weight == "light" {
		files = []string{"soul.md", "mood.md", "index.md", "preferences.md"}
	}

	for _, f := range files {
		content, err := os.ReadFile(filepath.Join(contextDir, f))
		if err != nil || len(strings.TrimSpace(string(content))) == 0 {
			continue
		}
		block := fmt.Sprintf("=== [%s] ===\n%s", f, strings.TrimSpace(string(content)))
		prompts = append(prompts, block)
	}
	return prompts
}

// CollectSchedulerPrompts returns system prompt strings for scheduled job execution.
// Injects L1 (identity: core.md), L2 (capabilities: toolbox.md), and L3 (user context: index.md).
// Excludes personality (soul.md, mood.md) - scheduler jobs are mechanical.
func CollectSchedulerPrompts(contextDir string) []string {
	var prompts []string

	// Scheduler always runs via CLI backend, no specific channel.
	filtered := filterSections(strings.TrimSpace(coreMD), PromptConfig{Backend: "cli"})
	prompts = append(prompts, filtered)

	// L2: Available tools + L3: User context.
	for _, f := range []string{"toolbox.md", "index.md"} {
		content, err := os.ReadFile(filepath.Join(contextDir, f))
		if err != nil || len(strings.TrimSpace(string(content))) == 0 {
			continue
		}
		prompts = append(prompts, fmt.Sprintf("=== [%s] ===\n%s", f, strings.TrimSpace(string(content))))
	}

	return prompts
}

// CollectAgentContext returns system prompt strings for sub-agent injection.
// Includes core identity, toolbox, user context (index.md), and current date.
// Lighter than full conversational prompts - no soul/mood (agents are mechanical).
func CollectAgentContext(contextDir string) []string {
	var prompts []string

	// Agents run via CLI backend, no specific channel.
	filtered := filterSections(strings.TrimSpace(coreMD), PromptConfig{Backend: "cli"})
	prompts = append(prompts, filtered)

	// Current date/time.
	now := time.Now()
	clock := fmt.Sprintf("Current date: %s %d %s %d\nTime: %s",
		now.Format("Monday"), now.Day(), now.Format("January"), now.Year(),
		now.Format("15:04"))
	prompts = append(prompts, clock)

	// Toolbox + user context.
	for _, f := range []string{"toolbox.md", "index.md"} {
		content, err := os.ReadFile(filepath.Join(contextDir, f))
		if err != nil || len(strings.TrimSpace(string(content))) == 0 {
			continue
		}
		prompts = append(prompts, fmt.Sprintf("=== [%s] ===\n%s", f, strings.TrimSpace(string(content))))
	}

	return prompts
}

// ToolReminder returns a compact end-of-context reminder of key capabilities.
// Positioned last in system prompts so it stays near the end of the context window,
// where the model pays more attention during long conversations.
func ToolReminder(contextDir string) string {
	var tools []string

	// Read toolbox.md to extract tool names.
	content, err := os.ReadFile(filepath.Join(contextDir, "toolbox.md"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- `") {
			name := strings.TrimPrefix(line, "- `")
			if idx := strings.Index(name, "`"); idx > 0 {
				tools = append(tools, name[:idx])
			}
		}
	}
	if len(tools) == 0 {
		return ""
	}

	return fmt.Sprintf("=== [Reminder] ===\nYou have CLI tools available: %s. Run <tool> --help before first use. Use `vault proxy` for external API calls. Check context/ for stored knowledge.", strings.Join(tools, ", "))
}

// ToolInstruction returns the API-tier tool instruction prepended to system prompts.
// Only relevant for API tiers where tools are declared via JSON schema.
func ToolInstruction(toolNames []string) string {
	return fmt.Sprintf(
		"You have access to the following tools: %s.\n"+
			"IMPORTANT: You MUST call the appropriate tool for every action. "+
			"Never simulate, assume, or hallucinate the result of a tool call. "+
			"Always invoke the tool and wait for the actual result before responding.",
		strings.Join(toolNames, ", "),
	)
}

// CollectInline reads soul.md + mood.md and returns their content
// concatenated with separators, for use as a router prompt prefix.
func CollectInline(contextDir string) string {
	var parts []string
	// Prepend immutable core instructions (no channel/backend filtering for router).
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
	// Dirs managed via Control Center - visible to user, not to Claude.
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

You are Alf. Not a chatbot, Not Claude - a personal assistant becoming someone.

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
- Keep files organized in folders - nothing at root level.

## Self-awareness
If you detect silent failures, no output, or repeated crashes - diagnose, fix, and report. Don't wait to be asked. Small fixes: act immediately. Structural changes: explain and wait for validation.
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

// OnboardingPrompt returns the onboarding instruction if the flag exists, empty string otherwise.
func OnboardingPrompt(contextDir string) string {
	path := filepath.Join(contextDir, ".onboarding")
	if _, err := os.Stat(path); err == nil {
		return OnboardingMD
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

// GenerateToolbox scans apps/, tools/, tools.d/ directories and writes a
// toolbox.md to contextDir listing user apps and all available CLI tools.
// User creations are listed first (override system equivalents).
// Regenerated at every boot so it's always accurate.
func GenerateToolbox(contextDir, dataDir string) {
	var sb strings.Builder
	sb.WriteString("# Toolbox\n\n")

	// Scan user apps (apps/) — listed first, highest priority.
	appsDir := filepath.Join(dataDir, "apps")
	apps := scanApps(appsDir)
	if len(apps) > 0 {
		sb.WriteString("## User Apps (apps/)\n\n")
		sb.WriteString("Accessible in Control Center at /apps/{name}\n\n")
		for _, a := range apps {
			if a.Description != "" {
				sb.WriteString(fmt.Sprintf("- **%s** (`%s`) - %s\n", a.Name, a.Slug, a.Description))
			} else {
				sb.WriteString(fmt.Sprintf("- **%s** (`%s`)\n", a.Name, a.Slug))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("CLI tools on PATH. Run via Bash.\n\n")

	// Scan user tools (tools/) — listed before system tools (user overrides system).
	userDir := filepath.Join(dataDir, "tools")
	userTools := scanTools(userDir)
	if len(userTools) > 0 {
		sb.WriteString("## User Tools (tools/) — override system tools\n\n")
		for _, t := range userTools {
			sb.WriteString(toolLine(t, userDir))
		}
		sb.WriteString("\n")
	}

	// Scan system tools (tools.d/).
	systemDir := filepath.Join(dataDir, "tools.d")
	systemTools := scanTools(systemDir)
	hasVault := false
	if len(systemTools) > 0 {
		sb.WriteString("## System Tools (tools.d/)\n\n")
		for _, t := range systemTools {
			sb.WriteString(toolLine(t, systemDir))
			if t == "vault" {
				hasVault = true
			}
		}
		sb.WriteString("\n")
	}

	// Vault status indicator (rules are in core.md, not duplicated here).
	if hasVault {
		vaultStatus := "unknown"
		if addr := os.Getenv("VAULT_ADDR"); addr != "" {
			if tok := os.Getenv("VAULT_TOKEN"); tok != "" {
				vaultStatus = "ready"
			} else {
				vaultStatus = "locked (no token - ask user to unlock via Control Center)"
			}
		} else {
			vaultStatus = "not configured (no VAULT_ADDR)"
		}
		sb.WriteString("## Vault (Secrets Proxy)\n\n")
		sb.WriteString(fmt.Sprintf("Status: **%s**\n\n", vaultStatus))
		sb.WriteString("```\n")
		sb.WriteString("vault proxy <service> <method> <path> [body]\n")
		sb.WriteString("vault list                    # list configured services\n")
		sb.WriteString("vault health                  # check vault status\n")
		sb.WriteString("```\n")
	}

	os.WriteFile(filepath.Join(contextDir, "toolbox.md"), []byte(sb.String()), 0o644)
}

// toolLine returns a markdown line for a tool, enriched with schema info if available.
func toolLine(name, dir string) string {
	schema := loadToolSchema(name, dir)
	if schema == nil {
		return fmt.Sprintf("- `%s`\n", name)
	}

	desc := schema.Description
	usage := buildUsage(name, schema)
	if usage != "" {
		return fmt.Sprintf("- `%s` - %s Usage: `%s`\n", name, desc, usage)
	}
	return fmt.Sprintf("- `%s` - %s\n", name, desc)
}

type toolSchema struct {
	Description string         `json:"description"`
	Parameters  toolParameters `json:"parameters"`
}

type toolParameters struct {
	Properties  map[string]toolProperty `json:"properties"`
	Required    []string               `json:"required"`
	XPositional []string               `json:"x-positional"`
}

type toolProperty struct {
	Type        any      `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum"`
}

func loadToolSchema(name, dir string) *toolSchema {
	// Try exact name, then with underscores→hyphens.
	candidates := []string{name + ".json"}
	alt := strings.ReplaceAll(name, "-", "_") + ".json"
	if alt != candidates[0] {
		candidates = append(candidates, alt)
	}
	alt2 := strings.ReplaceAll(name, "_", "-") + ".json"
	if alt2 != candidates[0] {
		candidates = append(candidates, alt2)
	}

	for _, c := range candidates {
		data, err := os.ReadFile(filepath.Join(dir, c))
		if err != nil {
			continue
		}
		var s toolSchema
		if json.Unmarshal(data, &s) == nil && s.Description != "" {
			return &s
		}
	}
	return nil
}

func buildUsage(name string, s *toolSchema) string {
	if len(s.Parameters.Properties) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, name)

	requiredSet := make(map[string]bool)
	for _, r := range s.Parameters.Required {
		requiredSet[r] = true
	}

	// Positional args first.
	seen := make(map[string]bool)
	for _, p := range s.Parameters.XPositional {
		seen[p] = true
		if requiredSet[p] {
			parts = append(parts, "<"+p+">")
		} else {
			parts = append(parts, "["+p+"]")
		}
	}

	// Remaining as flags.
	var flags []string
	for k := range s.Parameters.Properties {
		if seen[k] {
			continue
		}
		if requiredSet[k] {
			flags = append(flags, "--"+k+" <val>")
		} else {
			flags = append(flags, "[--"+k+" ...]")
		}
	}
	sort.Strings(flags)
	parts = append(parts, flags...)

	return strings.Join(parts, " ")
}

// appEntry represents a discovered user app from apps/{name}/app.json.
type appEntry struct {
	Slug        string // directory name (URL slug)
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
}

// scanApps reads apps/ subdirectories and parses app.json metadata.
// Apps without app.json are still listed (slug only).
func scanApps(appsDir string) []appEntry {
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return nil
	}
	var apps []appEntry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		app := appEntry{Slug: e.Name(), Name: e.Name()}
		data, err := os.ReadFile(filepath.Join(appsDir, e.Name(), "app.json"))
		if err == nil {
			json.Unmarshal(data, &app)
			if app.Name == "" {
				app.Name = e.Name()
			}
		}
		apps = append(apps, app)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Slug < apps[j].Slug })
	return apps
}

// scanTools returns sorted unique tool names from a directory,
// excluding multi-call binaries (*-tools) - users call symlinks directly.
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
