package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/memory"
)

// Result holds the classification output from the router.
type Result struct {
	Tier     string // tier name (e.g. "instant", "analyze", "heavy")
	Response string // non-empty only for direct router responses
	Reason   string // classifier reasoning
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

// Classify routes a message to a tier by calling the Claude CLI as a fast classifier.
func Classify(input ClassifyInput) Result {
	valid := validTierSet(input.Tiers)
	prompt := buildPrompt(input, valid)

	model := ResolveModel(input.Tiers.RouterModel)
	if model == "" {
		model = ResolveModel("haiku")
	}
	log.Printf("router: using model %s for classification", model)

	args := []string{
		"-p", prompt,
		"--model", model,
		"--output-format", "text",
		"--max-turns", "2",
		"--allowedTools", "",
		"--dangerously-skip-permissions",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = input.DataDir

	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "HOME=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "HOME="+input.DataDir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	raw := strings.TrimSpace(stdout.String())

	if err != nil || raw == "" {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("router: timeout after 30s, falling back to %s", input.Tiers.DefaultFallback)
		} else {
			log.Printf("router: classify error: %v (stderr: %s)", err, strings.TrimSpace(stderr.String()))
		}
		return fallbackResult(input.Tiers)
	}

	result := parseResponse(raw, valid)

	// Router answered directly (JSON with response field).
	if result.Response != "" && result.Tier == "" {
		log.Printf("router: %s → direct response (%s)", truncate(input.Message, 60), result.Reason)
		return result
	}

	// Router routed to a valid tier.
	if result.Tier != "" {
		log.Printf("router: %s → %s (%s)", truncate(input.Message, 60), result.Tier, result.Reason)
		return result
	}

	// Parse failed but router produced non-empty text — treat as direct response.
	// This handles models that answer directly without JSON wrapping.
	if raw != "" && !strings.HasPrefix(raw, "Error:") {
		log.Printf("router: %s → direct response (plain text)", truncate(input.Message, 60))
		return Result{Response: raw, Reason: "plain-text direct"}
	}

	fb := fallbackResult(input.Tiers)
	log.Printf("router: parse failed (%s), falling back to %s", truncate(raw, 100), fb.Tier)
	return fb
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

// buildPrompt constructs the classification prompt.
func buildPrompt(input ClassifyInput, valid map[string]bool) string {
	var b strings.Builder

	// 1. Personality (soul.md + mood.md) so direct responses match ALF's voice.
	personality := memory.CollectInline(filepath.Join(input.DataDir, "memories"))
	if personality != "" {
		b.WriteString(personality)
		b.WriteString("\n\n")
	}

	// 2. Role description.
	b.WriteString("You are a smart message router AND responder. Your job:\n")
	b.WriteString("1. If you can fully answer the message yourself, respond directly.\n")
	b.WriteString("2. If the message needs tools, file access, code changes, or deep reasoning, route it to the appropriate tier.\n\n")

	// 3. Tier catalog using Description (fallback to RouterLabel).
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

	b.WriteString("\nIMPORTANT: Only route to a write-capable tier if the user explicitly asks to create, modify, or delete files/code.\n")

	// 4. Conversation context.
	if input.MessageCount > 0 && input.LastTier != "" {
		b.WriteString(fmt.Sprintf("\nConversation context: Message #%d in session. Previous message handled by %q.\n", input.MessageCount+1, input.LastTier))
		b.WriteString("Prefer routing to same tier unless user intent clearly changed.\n")
	}

	// 5. Custom router prompt from file.
	routerPromptPath := filepath.Join(input.ConfigDir, "router-prompt.md")
	if data, err := os.ReadFile(routerPromptPath); err == nil {
		custom := strings.TrimSpace(string(data))
		if custom != "" {
			b.WriteString("\n")
			b.WriteString(custom)
			b.WriteString("\n")
		}
	}

	// 6. User message (truncated to 500 chars).
	b.WriteString(fmt.Sprintf("\nUser message: %s\n", truncate(input.Message, 500)))

	// 7. Response format.
	b.WriteString("\nRespond with ONLY a JSON object:")
	b.WriteString("\n- If you can answer: {\"response\": \"<your answer>\", \"reason\": \"direct\"}")
	b.WriteString("\n- If you need to route: {\"tier\": \"<name>\", \"reason\": \"<brief reason>\"}")

	return b.String()
}

// parseResponse extracts a Result from the classifier's raw text output.
func parseResponse(raw string, valid map[string]bool) Result {
	cleaned := stripMarkdownFences(raw)

	var parsed struct {
		Tier     string `json:"tier"`
		Response string `json:"response"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(cleaned), &parsed); err == nil {
		if parsed.Response != "" && parsed.Tier == "" {
			return Result{
				Response: parsed.Response,
				Reason:   parsed.Reason,
			}
		}
		if valid[parsed.Tier] {
			return Result{
				Tier:     parsed.Tier,
				Response: parsed.Response,
				Reason:   parsed.Reason,
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
	set := make(map[string]bool)
	for _, t := range tiers.Tiers {
		if t.Enabled && t.Routable {
			set[t.Name] = true
		}
	}
	return set
}

func fallbackResult(tiers *cc.TiersConfig) Result {
	fb := tiers.DefaultFallback
	if fb != "" {
		for _, t := range tiers.Tiers {
			if t.Name == fb && t.Enabled {
				return Result{Tier: fb, Reason: "fallback"}
			}
		}
	}
	for _, t := range tiers.Tiers {
		if t.Enabled && !t.Instant {
			return Result{Tier: t.Name, Reason: "fallback"}
		}
	}
	for _, t := range tiers.Tiers {
		if t.Enabled {
			return Result{Tier: t.Name, Reason: "fallback"}
		}
	}
	return Result{Tier: "instant", Reason: "fallback (no tiers)"}
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
