package router

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/memory"
)

// Result holds the classification output from the router.
type Result struct {
	Tier     string // tier name (e.g. "haiku", "sonnet", "opus")
	Response string // non-empty only for direct router responses
	Reason   string // classifier reasoning
	React    string // optional emoji reaction suggestion for the user's message
}

// AgentTeamInfo describes an agent team for routing awareness.
type AgentTeamInfo struct {
	Name        string
	Description string
	Agents      []string // agent names within the team
}

// ClassifyInput holds all inputs for the router classifier.
type ClassifyInput struct {
	Message       string
	Tiers         *cc.TiersConfig
	DataDir       string
	ConfigDir     string          // RO config path (for router-prompt.md)
	LastTier      string          // from session store
	MessageCount  int             // from session store
	AgentTeams    []AgentTeamInfo // available agent teams for routing hints
	RecentContext string          // compact summary of recent conversation turns
}

// ResolveModel maps short model names to full Claude model identifiers.
func ResolveModel(short string) string {
	switch strings.ToLower(short) {
	case "haiku":
		return "claude-haiku-4-5"
	case "sonnet":
		return "claude-sonnet-4-6"
	case "opus":
		return "claude-opus-4-6"
	default:
		if strings.HasPrefix(short, "claude-") {
			return short
		}
		return ""
	}
}

// BuildSystemPrompt constructs the one-time system prompt for the persistent
// classifier process. Includes personality, tier catalog, rules, and response format.
// This is sent once at process start; per-message user text goes via stdin.
func BuildSystemPrompt(tiers *cc.TiersConfig, dataDir, configDir string, agentTeams []AgentTeamInfo) string {
	var b strings.Builder

	// 1. Personality (soul.md + mood.md) so direct responses match ALF's voice.
	personality := memory.CollectInline(filepath.Join(dataDir, "context"))
	if personality != "" {
		b.WriteString(personality)
		b.WriteString("\n\n")
	}

	// 2. Router instructions (static part from .md file).
	// Split at first blank line to separate role description from rules.
	routerParts := strings.SplitN(strings.TrimSpace(memory.RouterMD), "\n\n", 2)
	// Role description (first paragraph of router.md).
	b.WriteString(routerParts[0])
	b.WriteString("\n\n")

	// 3. Tier catalog (dynamic).
	b.WriteString("Available tiers:\n")
	for _, t := range tiers.Tiers {
		if !t.Enabled || !t.Routable {
			continue
		}
		access := "read-only"
		if t.WriteCapable {
			access = "read-write"
		}
		desc := t.RouterDescription()
		b.WriteString(fmt.Sprintf("- %s (%s): %s\n", t.Name, access, desc))
	}

	if tiers.RouterDistinctions != "" {
		b.WriteString(fmt.Sprintf("\nKey distinctions: %s\n", tiers.RouterDistinctions))
	}

	// Orchestrator routing hint with available teams.
	if hasOrchestrator(tiers) {
		b.WriteString(buildAgentTeamsHint(agentTeams))
	}

	// 4. Custom router prompt from file.
	routerPromptPath := filepath.Join(configDir, "router-prompt.md")
	if data, err := os.ReadFile(routerPromptPath); err == nil {
		custom := strings.TrimSpace(string(data))
		if custom != "" {
			b.WriteString("\n")
			b.WriteString(custom)
			b.WriteString("\n")
		}
	}

	// 5. Valid tier names.
	b.WriteString("\nValid tier names: ")
	first := true
	for _, t := range tiers.Tiers {
		if t.Enabled && t.Routable {
			if !first {
				b.WriteString(", ")
			}
			b.WriteString("\"" + t.Name + "\"")
			first = false
		}
	}
	b.WriteString("\n")

	// 6. Remaining router instructions (write-intent rules, context tracking, response format).
	if len(routerParts) > 1 {
		b.WriteString("\n")
		b.WriteString(routerParts[1])
	}

	return b.String()
}

// BuildClassifyPrompt constructs the full single-shot classification prompt.
// Used as fallback when the persistent classifier is not available.
func BuildClassifyPrompt(input ClassifyInput) string {
	valid := ValidTierSet(input.Tiers)
	return buildPrompt(input, valid)
}

// ParseResponse extracts a Result from the classifier's raw text output.
func ParseResponse(raw string, tiers *cc.TiersConfig) Result {
	valid := ValidTierSet(tiers)
	return parseResponse(raw, valid)
}

// InterpretRaw applies the full interpretation logic to raw classifier output:
// parse JSON, handle direct responses, fallback on text scan, fallback tier.
// Includes a programmatic guardrail: if the selected tier is read-only but
// the message has write intent, it upgrades to the lowest write-capable tier.
func InterpretRaw(raw string, tiers *cc.TiersConfig, message string) Result {
	valid := ValidTierSet(tiers)
	result := parseResponse(raw, valid)

	// Router routed to a valid tier.
	if result.Tier != "" {
		// Guardrail: upgrade read-only → write-capable if write intent detected.
		if !tierIsWriteCapable(result.Tier, tiers) && HasWriteIntent(message) {
			if wt := lowestWriteTier(tiers); wt != "" {
				log.Printf("router: %s → %s upgraded to %s (write intent detected)", truncate(message, 60), result.Tier, wt)
				result.Tier = wt
				result.Reason += " [upgraded: write intent]"
				return result
			}
		}
		log.Printf("router: %s → %s (%s)", truncate(message, 60), result.Tier, result.Reason)
		return result
	}

	// Router returned a direct response or unparseable text - fallback to default tier.
	fb := FallbackResult(tiers)
	if result.Response != "" {
		log.Printf("router: %s → direct response ignored, falling back to %s", truncate(message, 60), fb.Tier)
	} else {
		log.Printf("router: parse failed (%s), falling back to %s", truncate(raw, 100), fb.Tier)
	}
	return fb
}

// ValidTierSet returns the set of enabled, routable tier names.
func ValidTierSet(tiers *cc.TiersConfig) map[string]bool {
	set := make(map[string]bool)
	for _, t := range tiers.Tiers {
		if t.Enabled && t.Routable {
			set[t.Name] = true
		}
	}
	return set
}

// FallbackResult returns a fallback tier when classification fails.
// Uses default_fallback if set and the tier is enabled, otherwise picks
// the lowest-priority enabled+routable tier.
func FallbackResult(tiers *cc.TiersConfig) Result {
	// Honor explicit default_fallback if the tier exists and is enabled.
	if tiers.DefaultFallback != "" {
		for _, t := range tiers.Tiers {
			if t.Name == tiers.DefaultFallback && t.Enabled {
				return Result{Tier: t.Name, Reason: "fallback (default)"}
			}
		}
	}

	// Pick the lowest-priority enabled+routable tier.
	best := ""
	bestPriority := int(^uint(0) >> 1)
	for _, t := range tiers.Tiers {
		if t.Enabled && t.Routable && t.Priority < bestPriority {
			best = t.Name
			bestPriority = t.Priority
		}
	}
	if best != "" {
		return Result{Tier: best, Reason: "fallback"}
	}
	// Any enabled tier.
	for _, t := range tiers.Tiers {
		if t.Enabled {
			return Result{Tier: t.Name, Reason: "fallback"}
		}
	}
	// All tiers disabled - use first tier regardless.
	if len(tiers.Tiers) > 0 {
		return Result{Tier: tiers.Tiers[0].Name, Reason: "fallback (all disabled)"}
	}
	return Result{Reason: "fallback (no tiers)"}
}

// TierAccess returns "read-write" or "read-only" for a tier name.
func TierAccess(tierName string, tiers *cc.TiersConfig) string {
	for _, t := range tiers.Tiers {
		if t.Name == tierName && t.WriteCapable {
			return "read-write"
		}
	}
	return "read-only"
}

// --- internal helpers (keep unexported for tests) ---

// buildPrompt constructs the full single-shot classification prompt.
func buildPrompt(input ClassifyInput, valid map[string]bool) string {
	var b strings.Builder

	personality := memory.CollectInline(filepath.Join(input.DataDir, "context"))
	if personality != "" {
		b.WriteString(personality)
		b.WriteString("\n\n")
	}

	// Role description (first paragraph of router.md).
	routerParts := strings.SplitN(strings.TrimSpace(memory.RouterMD), "\n\n", 2)
	b.WriteString(routerParts[0])
	b.WriteString("\n\n")

	b.WriteString("Available tiers:\n")
	for _, t := range input.Tiers.Tiers {
		if !t.Enabled || !t.Routable {
			continue
		}
		access := "read-only"
		if t.WriteCapable {
			access = "read-write"
		}
		desc := t.RouterDescription()
		b.WriteString(fmt.Sprintf("- %s (%s): %s\n", t.Name, access, desc))
	}

	if input.Tiers.RouterDistinctions != "" {
		b.WriteString(fmt.Sprintf("\nKey distinctions: %s\n", input.Tiers.RouterDistinctions))
	}

	if hasOrchestrator(input.Tiers) {
		b.WriteString(buildAgentTeamsHint(input.AgentTeams))
	}

	if input.RecentContext != "" {
		b.WriteString("\nRecent conversation (for context — use this to understand whether the new message continues the conversation or starts a new topic):\n")
		b.WriteString(input.RecentContext)
		b.WriteString("\n")
	}

	if input.MessageCount > 0 && input.LastTier != "" {
		b.WriteString(fmt.Sprintf("\nConversation context: Message #%d in session. Previous message handled by %q.\n", input.MessageCount+1, input.LastTier))
		b.WriteString("If the new message is a continuation of the conversation above (short reply, follow-up comment, answer to a question, reference to previous topic), route to the same tier (\"" + input.LastTier + "\"). But if the message requests any action (fix, apply, create, modify, etc.) or is clearly a new topic, route based on intent - do NOT stick to the previous tier if it lacks write capability.\n")
	}

	routerPromptPath := filepath.Join(input.ConfigDir, "router-prompt.md")
	if data, err := os.ReadFile(routerPromptPath); err == nil {
		custom := strings.TrimSpace(string(data))
		if custom != "" {
			b.WriteString("\n")
			b.WriteString(custom)
			b.WriteString("\n")
		}
	}

	b.WriteString(fmt.Sprintf("\nUser message: %s\n", truncate(input.Message, 500)))

	b.WriteString("\nValid tier names: ")
	first := true
	for _, t := range input.Tiers.Tiers {
		if t.Enabled && t.Routable {
			if !first {
				b.WriteString(", ")
			}
			b.WriteString("\"" + t.Name + "\"")
			first = false
		}
	}
	b.WriteString("\n")

	// Remaining router instructions (write-intent rules, context tracking, response format).
	if len(routerParts) > 1 {
		b.WriteString("\n")
		b.WriteString(routerParts[1])
	}

	return b.String()
}

func parseResponse(raw string, valid map[string]bool) Result {
	cleaned := stripMarkdownFences(raw)

	var parsed struct {
		Tier     string `json:"tier"`
		Response string `json:"response"`
		Reason   string `json:"reason"`
		React    string `json:"react"`
	}
	if err := json.Unmarshal([]byte(cleaned), &parsed); err == nil {
		if parsed.Response != "" && parsed.Tier == "" {
			return Result{
				Response: parsed.Response,
				Reason:   parsed.Reason,
				React:    parsed.React,
			}
		}
		if valid[parsed.Tier] {
			return Result{
				Tier:     parsed.Tier,
				Response: parsed.Response,
				Reason:   parsed.Reason,
				React:    parsed.React,
			}
		}
	}

	lower := strings.ToLower(raw)
	for name := range valid {
		if strings.Contains(lower, name) {
			return Result{Tier: name, Reason: "text-scan fallback"}
		}
	}

	return Result{}
}

func validTierSet(tiers *cc.TiersConfig) map[string]bool {
	return ValidTierSet(tiers)
}

func fallbackResult(tiers *cc.TiersConfig) Result {
	return FallbackResult(tiers)
}

func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
		if idx := strings.LastIndex(s, "```"); idx > 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// buildAgentTeamsHint generates a routing hint paragraph describing available agent teams.
// If no teams are configured, falls back to a generic orchestrator hint.
func buildAgentTeamsHint(teams []AgentTeamInfo) string {
	var b strings.Builder
	b.WriteString("IMPORTANT: Route to \"agent\" tier when:\n")
	b.WriteString("  1. The task matches an available agent team's specialty (see list below)\n")
	b.WriteString("  2. The user explicitly asks to use agents/teams (\"lance une équipe\", \"use agents\")\n")
	b.WriteString("  3. The task requires research+writing, or multiple steps that benefit from parallel work\n")

	if len(teams) > 0 {
		b.WriteString("\nAvailable agent teams:\n")
		for _, t := range teams {
			b.WriteString(fmt.Sprintf("  - \"%s\": %s", t.Name, t.Description))
			if len(t.Agents) > 0 {
				b.WriteString(fmt.Sprintf(" [agents: %s]", strings.Join(t.Agents, ", ")))
			}
			b.WriteString("\n")
		}
		b.WriteString("\nExamples that MUST route to \"agent\":\n")
		b.WriteString("  - \"rédige un article sur X\" → agent (writer team)\n")
		b.WriteString("  - \"write a blog post about Y\" → agent (research + writing)\n")
		b.WriteString("  - \"research Z and write a report\" → agent (multi-step)\n")
		b.WriteString("  - \"review this code and fix issues\" → agent (review + fix)\n")
	}

	b.WriteString("\nDo NOT route to sonnet/opus for tasks that match a team - only the \"agent\" tier can coordinate agents.\n")
	b.WriteString("Do NOT route to \"agent\" for:\n")
	b.WriteString("  - Conversational messages (\"ok\", \"merci\", \"on va focus sur X\", \"j'ai fini\", small talk)\n")
	b.WriteString("  - Simple questions or status checks\n")
	b.WriteString("  - Topic changes or expressions of intent without an actionable instruction\n")
	return b.String()
}

// hasOrchestrator returns true if an enabled+routable agent tier exists.
func hasOrchestrator(tiers *cc.TiersConfig) bool {
	for _, t := range tiers.Tiers {
		if t.Name == "agent" && t.Enabled && t.Routable {
			return true
		}
	}
	return false
}

// writeIntentVerbs are words that signal the user wants something modified.
// Checked with word-boundary detection to avoid false positives.
var writeIntentVerbs = []string{
	"fix", "polish", "apply", "correct", "repair", "patch", "improve",
	"refactor", "clean up", "cleanup", "rewrite", "rework",
	"create", "modify", "delete", "remove", "update", "set", "mark",
	"change", "edit", "enable", "disable", "mute", "silence",
	"configure", "schedule", "install", "build", "deploy", "run",
	"write", "add", "rename", "move", "replace", "merge", "split",
	"implement", "execute", "generate", "transform", "convert",
}

// HasWriteIntent returns true if the message contains words signaling
// the user wants something created, modified, or fixed.
func HasWriteIntent(message string) bool {
	lower := strings.ToLower(message)
	for _, verb := range writeIntentVerbs {
		idx := strings.Index(lower, verb)
		if idx == -1 {
			continue
		}
		// Word boundary check: character before must be start-of-string or non-letter.
		if idx > 0 {
			prev := rune(lower[idx-1])
			if prev >= 'a' && prev <= 'z' {
				continue
			}
		}
		// Character after must be end-of-string or non-letter.
		end := idx + len(verb)
		if end < len(lower) {
			next := rune(lower[end])
			if next >= 'a' && next <= 'z' {
				continue
			}
		}
		return true
	}
	return false
}

// tierIsWriteCapable returns true if the named tier has WriteCapable set.
func tierIsWriteCapable(name string, tiers *cc.TiersConfig) bool {
	for _, t := range tiers.Tiers {
		if t.Name == name {
			return t.WriteCapable
		}
	}
	return false
}

// lowestWriteTier returns the name of the lowest-priority enabled, routable,
// write-capable tier. Returns "" if none found.
func lowestWriteTier(tiers *cc.TiersConfig) string {
	best := ""
	bestPriority := int(^uint(0) >> 1)
	for _, t := range tiers.Tiers {
		if t.Enabled && t.Routable && t.WriteCapable && t.Priority < bestPriority {
			best = t.Name
			bestPriority = t.Priority
		}
	}
	return best
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
