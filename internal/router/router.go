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
	Response string // non-empty only for instant tier
	Reason   string // classifier reasoning
}

// Classify routes a message to a tier by calling the Claude CLI as a fast classifier.
// Uses the router model (default haiku) with a 15s timeout, no tools, max 1 turn.
func Classify(message string, tiers *cc.TiersConfig, dataDir string) Result {
	valid := validTierSet(tiers)
	prompt := buildPrompt(message, tiers, valid, dataDir)

	model := ResolveModel(tiers.RouterModel)
	if model == "" {
		model = ResolveModel("haiku")
	}
	log.Printf("router: using model %s for classification", model)

	args := []string{
		"-p", prompt,
		"--model", model,
		"--output-format", "text",
		"--max-turns", "1",
		"--dangerously-skip-permissions",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = dataDir

	env := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "HOME=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "HOME="+dataDir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	raw := strings.TrimSpace(stdout.String())

	if err != nil || raw == "" {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("router: timeout after 30s, falling back to %s", tiers.DefaultFallback)
		} else {
			log.Printf("router: classify error: %v (stderr: %s)", err, strings.TrimSpace(stderr.String()))
		}
		return fallbackResult(tiers)
	}

	result := parseResponse(raw, valid)

	// Router answered directly.
	if result.Response != "" && result.Tier == "" {
		log.Printf("router: %s → direct response (%s)", truncate(message, 60), result.Reason)
		return result
	}

	// Router routed to a tier.
	if result.Tier != "" {
		log.Printf("router: %s → %s (%s)", truncate(message, 60), result.Tier, result.Reason)
		return result
	}

	log.Printf("router: no valid tier or response, falling back to %s", tiers.DefaultFallback)
	return fallbackResult(tiers)
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
		// Allow full model names to pass through.
		if strings.HasPrefix(short, "claude-") {
			return short
		}
		return ""
	}
}

// buildPrompt constructs the classification prompt listing routable tiers.
// The router tries to answer directly when possible, only routing to another
// tier when the task requires tools, file access, or deeper reasoning.
func buildPrompt(message string, tiers *cc.TiersConfig, valid map[string]bool, dataDir string) string {
	var b strings.Builder

	// Inject personality (soul.md + mood.md) so direct responses match ALF's voice.
	personality := memory.CollectInline(filepath.Join(dataDir, "memories"))
	if personality != "" {
		b.WriteString(personality)
		b.WriteString("\n\n")
	}

	b.WriteString("You are a smart message router AND responder. Your job:\n")
	b.WriteString("1. If you can fully answer the message yourself, respond directly.\n")
	b.WriteString("2. If the message needs tools, file access, code changes, or deep reasoning, route it to the appropriate tier.\n\n")

	b.WriteString("Available tiers (only route when YOU cannot handle it):\n")
	for _, t := range tiers.Tiers {
		if !t.Enabled || !t.Routable {
			continue
		}
		access := "read-only"
		if t.WriteCapable {
			access = "read-write"
		}
		b.WriteString(fmt.Sprintf("- %s (%s): %s\n", t.Name, access, t.RouterLabel))
	}

	if tiers.RouterDistinctions != "" {
		b.WriteString(fmt.Sprintf("\nKey distinctions: %s\n", tiers.RouterDistinctions))
	}

	b.WriteString("\nIMPORTANT: Only route to a write-capable tier if the user explicitly asks to create, modify, or delete files/code.\n")

	b.WriteString(fmt.Sprintf("\nUser message: %s\n", truncate(message, 300)))

	b.WriteString("\nRespond with ONLY a JSON object:")
	b.WriteString("\n- If you can answer: {\"response\": \"<your answer>\", \"reason\": \"direct\"}")
	b.WriteString("\n- If you need to route: {\"tier\": \"<name>\", \"reason\": \"<brief reason>\"}")

	return b.String()
}

// parseResponse extracts a Result from the classifier's raw text output.
func parseResponse(raw string, valid map[string]bool) Result {
	// Try JSON parse (with optional markdown fence stripping).
	cleaned := stripMarkdownFences(raw)

	var parsed struct {
		Tier     string `json:"tier"`
		Response string `json:"response"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(cleaned), &parsed); err == nil {
		// Router answered directly — no tier needed.
		if parsed.Response != "" && parsed.Tier == "" {
			return Result{
				Response: parsed.Response,
				Reason:   parsed.Reason,
			}
		}
		// Router routed to a specific tier.
		if valid[parsed.Tier] {
			return Result{
				Tier:     parsed.Tier,
				Response: parsed.Response,
				Reason:   parsed.Reason,
			}
		}
	}

	// Fallback: scan raw text for any valid tier name.
	lower := strings.ToLower(raw)
	for name := range valid {
		if strings.Contains(lower, name) {
			return Result{Tier: name, Reason: "text-scan fallback"}
		}
	}

	return Result{}
}

// validTierSet returns the set of tier names the router can classify into.
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
	// Verify fallback tier actually exists; if not, pick first enabled non-instant tier.
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
	// Last resort: first enabled tier of any kind.
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
		// Remove opening fence (```json or ```)
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
		// Remove closing fence
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
