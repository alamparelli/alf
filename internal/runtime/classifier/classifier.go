// Package classifier routes a user message to a tier and, when the router
// decides to answer directly, produces a ready-made response. It lives
// inside internal/runtime/ because it imports memory + controlcenter —
// imports that would violate §4 if placed under internal/ai/. Moved here
// from internal/router/ in #340 R2a.
package classifier

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
		weight := t.EffectiveContextWeight()
		desc := t.RouterDescription()
		if weight == "light" {
			b.WriteString(fmt.Sprintf("- %s (%s, light model — simple tasks only): %s\n", t.Name, access, desc))
		} else {
			b.WriteString(fmt.Sprintf("- %s (%s): %s\n", t.Name, access, desc))
		}
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
// Includes programmatic guardrails:
// 1. Read-only → write-capable upgrade if message has write intent.
// 2. Light tier → standard upgrade if message shows complexity markers.
func InterpretRaw(raw string, tiers *cc.TiersConfig, message string) Result {
	valid := ValidTierSet(tiers)
	result := parseResponse(raw, valid)

	// Router routed to a valid tier.
	if result.Tier != "" {
		// Guardrail 1: upgrade read-only → write-capable if write intent detected.
		if !tierIsWriteCapable(result.Tier, tiers) && HasWriteIntent(message) {
			if wt := lowestWriteTier(tiers); wt != "" {
				log.Printf("router: (%d chars) → %s upgraded to %s (write intent detected)", len(message), result.Tier, wt)
				result.Tier = wt
				result.Reason += " [upgraded: write intent]"
				return result
			}
		}
		// Guardrail 2: downgrade non-light → light if reason says greeting/trivial.
		if tierContextWeight(result.Tier, tiers) != "light" && isGreetingReason(result.Reason) && !hasComplexityMarkers(message) {
			if lt := lowestLightTier(tiers); lt != "" {
				log.Printf("router: (%d chars) → %s downgraded to %s (greeting detected)", len(message), result.Tier, lt)
				result.Tier = lt
				result.Reason += " [downgraded: greeting]"
				return result
			}
		}
		// Guardrail 3: upgrade light → next tier if message is too complex.
		if tierContextWeight(result.Tier, tiers) == "light" && hasComplexityMarkers(message) {
			if nt := nextTierAbove(result.Tier, tiers); nt != "" {
				log.Printf("router: (%d chars) → %s upgraded to %s (complexity markers)", len(message), result.Tier, nt)
				result.Tier = nt
				result.Reason += " [upgraded: complexity]"
				return result
			}
		}
		log.Printf("router: (%d chars) → %s (%s)", len(message), result.Tier, result.Reason)
		return result
	}

	// Router returned a direct response or unparseable text - fallback to default tier.
	fb := FallbackResult(tiers)
	if result.Response != "" {
		log.Printf("router: (%d chars) → direct response ignored, falling back to %s", len(message), fb.Tier)
	} else {
		// Log a truncated raw sample so parse failures are diagnosable (#194).
		preview := raw
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		log.Printf("router: parse failed (%d chars), falling back to %s. raw=%q", len(raw), fb.Tier, preview)
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
		weight := t.EffectiveContextWeight()
		desc := t.RouterDescription()
		if weight == "light" {
			b.WriteString(fmt.Sprintf("- %s (%s, light model — simple tasks only): %s\n", t.Name, access, desc))
		} else {
			b.WriteString(fmt.Sprintf("- %s (%s): %s\n", t.Name, access, desc))
		}
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
// Conservative: only route to agent when explicitly requested or clearly parallel work.
func buildAgentTeamsHint(teams []AgentTeamInfo) string {
	var b strings.Builder
	b.WriteString("The \"agent\" tier coordinates parallel workstreams. Route to it when:\n")
	b.WriteString("  1. The user explicitly mentions a team by name (see list below)\n")
	b.WriteString("  2. The user asks to use agents or teams (\"lance les agents\", \"use agents\", \"use team X\")\n")
	b.WriteString("  3. The task explicitly requires PARALLEL independent workstreams\n")

	if len(teams) > 0 {
		b.WriteString("\nAvailable agent teams:\n")
		for _, t := range teams {
			b.WriteString(fmt.Sprintf("  - \"%s\": %s", t.Name, t.Description))
			if len(t.Agents) > 0 {
				b.WriteString(fmt.Sprintf(" [agents: %s]", strings.Join(t.Agents, ", ")))
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\nDo NOT route to \"agent\" for:\n")
	b.WriteString("  - Conversational messages, questions, status checks, simple follow-ups\n")
	b.WriteString("  - Tasks that don't mention a team and don't need parallel work\n")
	b.WriteString("When in doubt and no team is mentioned, route to a standard conversational tier.\n")
	return b.String()
}

// hasOrchestrator returns true if an enabled+routable orchestrator tier exists.
func hasOrchestrator(tiers *cc.TiersConfig) bool {
	for _, t := range tiers.Tiers {
		if t.IsOrchestrator() && t.Enabled && t.Routable {
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

// greetingReasonMarkers are substrings in classifier reasons that indicate trivial messages.
var greetingReasonMarkers = []string{
	"greeting", "greet", "small talk", "farewell", "acknowledge",
	"non-actionable", "casual", "salutation", "chitchat", "pleasantr",
}

// isGreetingReason checks if the classifier's reason text indicates a greeting/trivial message.
func isGreetingReason(reason string) bool {
	lower := strings.ToLower(reason)
	for _, marker := range greetingReasonMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// lowestLightTier returns the lowest-priority enabled+routable tier with context_weight=light.
func lowestLightTier(tiers *cc.TiersConfig) string {
	best := ""
	bestPriority := int(^uint(0) >> 1)
	for _, t := range tiers.Tiers {
		if t.Enabled && t.Routable && t.EffectiveContextWeight() == "light" && t.Priority < bestPriority {
			best = t.Name
			bestPriority = t.Priority
		}
	}
	return best
}

// tierContextWeight returns the effective context weight for a tier name.
func tierContextWeight(name string, tiers *cc.TiersConfig) string {
	for _, t := range tiers.Tiers {
		if t.Name == name {
			return t.EffectiveContextWeight()
		}
	}
	return "full"
}

// complexityMarkers are words/patterns that signal a message needs more than a light model.
var complexityMarkers = []string{
	"pourquoi", "comment", "explique", "explain", "why", "how",
	"compare", "analyse", "analyze", "résume", "summarize",
	"difference", "trade-off", "avantage", "inconvénient",
	"debug", "error", "bug", "stack trace", "exception",
}

// hasComplexityMarkers returns true if the message shows signs of needing
// reasoning capability beyond what a light model provides.
func hasComplexityMarkers(message string) bool {
	if len(message) > 150 {
		return true // long messages generally need more reasoning
	}
	lower := strings.ToLower(message)
	if strings.Count(lower, "?") >= 2 {
		return true // multiple questions
	}
	for _, marker := range complexityMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// nextTierAbove returns the next enabled+routable tier above the given one by priority.
func nextTierAbove(name string, tiers *cc.TiersConfig) string {
	var currentPriority int
	for _, t := range tiers.Tiers {
		if t.Name == name {
			currentPriority = t.Priority
			break
		}
	}
	best := ""
	bestPriority := int(^uint(0) >> 1)
	for _, t := range tiers.Tiers {
		if t.Enabled && t.Routable && t.Priority > currentPriority && t.Priority < bestPriority {
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
