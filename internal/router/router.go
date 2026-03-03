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
	Tier     string // tier name (e.g. "instant", "analyze", "heavy")
	Response string // non-empty only for direct router responses
	Reason   string // classifier reasoning
	React    string // optional emoji reaction suggestion for the user's message
}

// ClassifyInput holds all inputs for the router classifier.
type ClassifyInput struct {
	Message      string
	Tiers        *cc.TiersConfig
	DataDir      string
	ConfigDir    string // RO config path (for router-prompt.md)
	LastTier     string // from session store
	MessageCount int    // from session store
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
func BuildSystemPrompt(tiers *cc.TiersConfig, dataDir, configDir string) string {
	var b strings.Builder

	// 1. Personality (soul.md + mood.md) so direct responses match ALF's voice.
	personality := memory.CollectInline(filepath.Join(dataDir, "context"))
	if personality != "" {
		b.WriteString(personality)
		b.WriteString("\n\n")
	}

	// 2. Role description.
	b.WriteString("You are a smart message router AND responder. Your job:\n")
	b.WriteString("1. If you can fully answer the message yourself, respond directly.\n")
	b.WriteString("2. If the message needs tools, file access, code changes, or deep reasoning, route it to the appropriate tier.\n\n")

	// 3. Tier catalog.
	b.WriteString("Available tiers (only route when YOU cannot handle it):\n")
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

	b.WriteString("\nIMPORTANT: Route to a write-capable (_rw) tier when the user asks to create, modify, delete, update, set, mark, change, or edit ANYTHING (files, tasks, settings, status, etc.).\n")
	b.WriteString("IMPORTANT: If the message references conversation history (\"what did we talk about\", \"earlier\", \"before\", \"you said\", \"continue\"), you MUST route to a tier — never respond directly, because you have no conversation memory.\n")

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

	// 6. Conversation context instruction.
	b.WriteString("\nYou maintain conversation context across messages. After each tier response, you'll receive a summary like:\n")
	b.WriteString("[tierName (access) responded: brief summary]\n")
	b.WriteString("Use this to track what happened and make better routing decisions for follow-up messages.\n")

	// 7. Response format.
	b.WriteString("\nRespond with ONLY a JSON object:")
	b.WriteString("\n- If you can answer: {\"response\": \"<your answer>\", \"reason\": \"direct\", \"react\": \"EMOJI_or_empty\"}")
	b.WriteString("\n- If you need to route: {\"tier\": \"<EXACT tier name from list above>\", \"reason\": \"<brief reason>\"}")
	b.WriteString("\nThe \"tier\" value MUST be one of the valid tier names listed above. Do NOT invent tier names.")
	b.WriteString("\nThe optional \"react\" field suggests a single emoji reaction for the user's message (shows you understood it). Omit or leave empty if no reaction fits. Pick contextually relevant emojis, not generic thumbs up.")

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
func InterpretRaw(raw string, tiers *cc.TiersConfig, message string) Result {
	valid := ValidTierSet(tiers)
	result := parseResponse(raw, valid)

	// Router answered directly.
	if result.Response != "" && result.Tier == "" {
		log.Printf("router: %s → direct response (%s)", truncate(message, 60), result.Reason)
		return result
	}

	// Router routed to a valid tier.
	if result.Tier != "" {
		log.Printf("router: %s → %s (%s)", truncate(message, 60), result.Tier, result.Reason)
		return result
	}

	// Parse failed but non-empty text — use as direct response.
	if raw != "" && !strings.HasPrefix(raw, "Error:") && !strings.EqualFold(raw, "Execution error") {
		log.Printf("router: %s → direct response (plain text)", truncate(message, 60))
		return Result{Response: raw, Reason: "plain-text direct"}
	}

	fb := FallbackResult(tiers)
	log.Printf("router: parse failed (%s), falling back to %s", truncate(raw, 100), fb.Tier)
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
func FallbackResult(tiers *cc.TiersConfig) Result {
	// Pick the lowest-priority enabled non-instant tier.
	best := ""
	bestPriority := int(^uint(0) >> 1)
	for _, t := range tiers.Tiers {
		if t.Enabled && !t.Instant && t.Priority < bestPriority {
			best = t.Name
			bestPriority = t.Priority
		}
	}
	if best != "" {
		return Result{Tier: best, Reason: "fallback"}
	}
	for _, t := range tiers.Tiers {
		if t.Enabled {
			return Result{Tier: t.Name, Reason: "fallback"}
		}
	}
	return Result{Tier: "instant", Reason: "fallback (no tiers)"}
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

	b.WriteString("You are a smart message router AND responder. Your job:\n")
	b.WriteString("1. If you can fully answer the message yourself, respond directly.\n")
	b.WriteString("2. If the message needs tools, file access, code changes, or deep reasoning, route it to the appropriate tier.\n\n")

	b.WriteString("Available tiers (only route when YOU cannot handle it):\n")
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

	b.WriteString("\nIMPORTANT: Route to a write-capable (_rw) tier when the user asks to create, modify, delete, update, set, mark, change, or edit ANYTHING (files, tasks, settings, status, etc.).\n")
	b.WriteString("IMPORTANT: If the message references conversation history (\"what did we talk about\", \"earlier\", \"before\", \"you said\", \"continue\"), you MUST route to a tier — never respond directly, because you have no conversation memory.\n")

	if input.MessageCount > 0 && input.LastTier != "" {
		b.WriteString(fmt.Sprintf("\nConversation context: Message #%d in session. Previous message handled by %q.\n", input.MessageCount+1, input.LastTier))
		b.WriteString("If the message is a short reply (\"oui\", \"non\", \"ok\", \"yes\", \"no\", \"continue\") that clearly answers or continues the previous exchange, route to the same tier (\"" + input.LastTier + "\"). But if it's a new greeting or new topic, route normally.\n")
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

	b.WriteString("\nRespond with ONLY a JSON object:")
	b.WriteString("\n- If you can answer: {\"response\": \"<your answer>\", \"reason\": \"direct\", \"react\": \"EMOJI_or_empty\"}")
	b.WriteString("\n- If you need to route: {\"tier\": \"<EXACT tier name from list above>\", \"reason\": \"<brief reason>\"}")
	b.WriteString("\nThe \"tier\" value MUST be one of the valid tier names listed above. Do NOT invent tier names.")
	b.WriteString("\nThe optional \"react\" field suggests a single emoji reaction for the user's message (shows you understood it). Omit or leave empty if no reaction fits. Pick contextually relevant emojis, not generic thumbs up.")

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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
