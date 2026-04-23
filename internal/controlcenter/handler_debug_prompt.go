package controlcenter

import (
	"embed"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/platform/mood"
	"github.com/alamparelli/alf/internal/skills"
	"github.com/alamparelli/alf/internal/tooling"
)

//go:embed web/debug-tools.html
var debugToolsHTML embed.FS

// DebugToolsPageHandler serves the tool call tester HTML page.
type DebugToolsPageHandler struct{}

func (h *DebugToolsPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, err := debugToolsHTML.ReadFile("web/debug-tools.html")
	if err != nil {
		respondError(w, http.StatusNotFound, "page not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; "+
			"style-src 'self' 'unsafe-inline'; "+
			"script-src 'self' 'unsafe-inline'; "+
			"connect-src 'self' https://openrouter.ai; "+
			"frame-ancestors 'none'")
	w.Write(data)
}

// DebugPromptHandler returns the exact system prompts and tool schemas
// that would be sent for a given tier. Used for debugging tool calling issues.
type DebugPromptHandler struct {
	ChatService *ChatService
}

type debugPromptResponse struct {
	Tier          string              `json:"tier"`
	Model         string              `json:"model"`
	Backend       string              `json:"backend"`
	SystemPrompts []string            `json:"system_prompts"`
	Tools         []map[string]any    `json:"tools"`
	ToolNames     []string            `json:"tool_names"`
	FullPrompt    string              `json:"full_prompt"`
}

func (h *DebugPromptHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	tierName := r.URL.Query().Get("tier")
	if tierName == "" {
		// Return available tiers.
		tiers := h.ChatService.TierStore.Current()
		var names []string
		for _, t := range tiers.Tiers {
			if t.Enabled {
				names = append(names, t.Name)
			}
		}
		respondJSON(w, http.StatusOK, map[string]any{"tiers": names})
		return
	}

	cs := h.ChatService
	tp := cs.resolveTierParams(tierName)

	// Build system prompts (same as Ask()).
	isAPITier := tp.Backend != "" && tp.Backend != "cli"
	backend := "cli"
	if isAPITier {
		backend = "api"
	}
	sysPromptTexts := memory.CollectPrompts(cs.ContextDir, memory.PromptConfig{Backend: backend, Channel: "cc"})

	if tp.SystemPrompt != "" {
		sysPromptTexts = append([]string{tp.SystemPrompt}, sysPromptTexts...)
	}

	if cs.Recaller != nil {
		if recallBlock := recallMemories(cs.Recaller, "test message"); recallBlock != "" {
			sysPromptTexts = append(sysPromptTexts, recallBlock)
		}
	}

	if cs.SkillStore != nil {
		if catalog := skills.BuildCatalog(cs.SkillStore); catalog != "" {
			sysPromptTexts = append(sysPromptTexts, catalog)
		}
	}

	sysPromptTexts = append(sysPromptTexts, fmt.Sprintf(memory.ReactionMD, mood.AllowedReactionList()))

	if reminder := memory.ToolReminder(cs.ContextDir); reminder != "" {
		sysPromptTexts = append(sysPromptTexts, reminder)
	}

	// Build tool schemas.
	var toolSchemas []map[string]any
	var toolNames []string

	if isAPITier && cs.ToolRegistry != nil && len(tp.Tools) > 0 {
		schemas := cs.ToolRegistry.ForToolsStrict(tp.Tools)
		if len(schemas) > 0 {
			toolSchemas = tooling.ToOpenAI(schemas)
			toolNames = make([]string, len(schemas))
			for i, s := range schemas {
				toolNames[i] = s.Name
			}
			// Add tool instruction (same as Ask()).
			sysPromptTexts = append([]string{memory.ToolInstruction(toolNames)}, sysPromptTexts...)
		}
	}

	// Combine full prompt.
	fullPrompt := strings.Join(sysPromptTexts, "\n\n---\n\n")

	resp := debugPromptResponse{
		Tier:          tierName,
		Model:         tp.Model,
		Backend:       tp.Backend,
		SystemPrompts: sysPromptTexts,
		Tools:         toolSchemas,
		ToolNames:     toolNames,
		FullPrompt:    fullPrompt,
	}

	log.Printf("[debug-prompt] tier=%s model=%s backend=%s tools=%d prompts=%d prompt_len=%d",
		tierName, tp.Model, tp.Backend, len(toolSchemas), len(sysPromptTexts), len(fullPrompt))

	respondJSON(w, http.StatusOK, resp)
}
