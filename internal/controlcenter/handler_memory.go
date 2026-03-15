package controlcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/provider"
)

// validFileName uses the shared safeName pattern from validation.go.
var validFileName = safeName

// protectedContextFiles cannot be overwritten via Teach.
var protectedContextFiles = map[string]bool{
	"soul": true, "mood": true, "index": true, "toolbox": true,
}

const maxIngestContentBytes = 50 * 1024 // 50KB

// allowedInstructions restricts extraction instructions to known presets.
// Custom instructions are passed through but capped to prevent prompt abuse.
var allowedInstructions = map[string]bool{
	"extract key facts":   true,
	"extract preferences": true,
	"extract decisions":   true,
	"store-as-is":         true,
	"summarize":           true,
}

const maxCustomInstructionLen = 200

// MemoryStorer is the subset of memstore.Store used by the ingest handler.
type MemoryStorer interface {
	Store(text, memType, source string, meta map[string]any) (int64, error)
}

// MemoryIngestHandler handles POST /api/memory/ingest.
type MemoryIngestHandler struct {
	Store        MemoryStorer
	Provider     provider.Provider
	TierStore    TierStore
	ContextStore ResourceStore // for destination=context
}

type ingestRequest struct {
	Content     string `json:"content"`
	Instruction string `json:"instruction"`
	Tier        string `json:"tier"`         // tier name to use for extraction (optional)
	Destination string `json:"destination"`  // "memory" (default) or "context"
	FileName    string `json:"file_name"`    // required when destination=context
}

type ingestResponse struct {
	Imported int            `json:"imported"`
	Skipped  int            `json:"skipped"`
	Memories []ingestMemory `json:"memories"`
}

type ingestMemory struct {
	Text string `json:"text"`
	Type string `json:"type"`
}

func (h *MemoryIngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Limit request body to prevent memory exhaustion (content limit + JSON overhead).
	r.Body = http.MaxBytesReader(w, r.Body, maxIngestContentBytes+4096)

	var req ingestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "request too large or invalid JSON"})
		return
	}

	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}

	contentSize := len(req.Content)
	if contentSize > maxIngestContentBytes {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("Content exceeds 50KB limit (current: %dKB)", contentSize/1024),
		})
		return
	}

	// Validate instruction: allow known presets or cap custom instructions.
	if !allowedInstructions[strings.ToLower(req.Instruction)] && len(req.Instruction) > maxCustomInstructionLen {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("custom instruction too long (max %d chars)", maxCustomInstructionLen),
		})
		return
	}

	// Route by destination.
	if req.Destination == "context" {
		h.handleContextDestination(w, req)
		return
	}

	// Store-as-is: split by newlines, no Claude call.
	if strings.EqualFold(req.Instruction, "store-as-is") {
		resp := h.storeAsIs(req.Content)
		respondJSON(w, http.StatusOK, resp)
		return
	}

	// Claude extraction.
	resp, err := h.extractAndStore(req.Content, req.Instruction, req.Tier)
	if err != nil {
		log.Printf("[CC] memory ingest error: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "extraction failed - check server logs"})
		return
	}

	respondJSON(w, http.StatusOK, resp)
}

func (h *MemoryIngestHandler) storeAsIs(content string) *ingestResponse {
	lines := strings.Split(content, "\n")
	resp := &ingestResponse{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		_, err := h.Store.Store(line, "fact", "user-import", nil)
		if err != nil {
			if strings.Contains(err.Error(), "duplicate") {
				resp.Skipped++
				continue
			}
			log.Printf("[CC] memory store error: %v", err)
			resp.Skipped++
			continue
		}
		resp.Imported++
		resp.Memories = append(resp.Memories, ingestMemory{Text: line, Type: "fact"})
	}

	return resp
}

// resolveTier finds the requested tier, or defaults to the first enabled tier with tools.
func (h *MemoryIngestHandler) resolveTier(name string) *Tier {
	if h.TierStore == nil {
		return nil
	}
	tc := h.TierStore.Current()
	if tc == nil {
		return nil
	}

	// Explicit selection.
	if name != "" {
		for i := range tc.Tiers {
			if tc.Tiers[i].Name == name && tc.Tiers[i].Enabled {
				return &tc.Tiers[i]
			}
		}
	}

	// Default: first enabled tier with tool access (bash/tools = capable of extraction).
	for i := range tc.Tiers {
		t := &tc.Tiers[i]
		if t.Enabled && len(t.Tools) > 0 {
			return t
		}
	}

	// Fallback: any enabled tier.
	for i := range tc.Tiers {
		if tc.Tiers[i].Enabled {
			return &tc.Tiers[i]
		}
	}
	return nil
}

func (h *MemoryIngestHandler) extractAndStore(content, instruction, tierName string) (*ingestResponse, error) {
	if instruction == "" {
		instruction = "Extract key facts"
	}

	prompt := fmt.Sprintf(`Extract structured memories from this content.
Instruction: %s

Content:
---
%s
---

Return ONLY a JSON array: [{"text": "...", "type": "fact|preference|decision"}]
Rules: self-contained items, concise, skip trivial info.`, instruction, content)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Build params from tier config.
	params := provider.Params{
		Model:    "claude-haiku-4-5",
		MaxTurns: 3,
		Tools:    []string{""}, // no tools by default
	}
	if tier := h.resolveTier(tierName); tier != nil {
		if tier.Model != "" {
			params.Model = tier.Model
		}
		if tier.MaxTurns > 0 {
			params.MaxTurns = tier.MaxTurns
		}
		if len(tier.Tools) > 0 {
			params.Tools = tier.Tools
		}
		if tier.Effort != "" {
			params.Effort = tier.Effort
		}
	}

	result, err := h.Provider.Invoke(ctx, prompt, params, nil)
	if err != nil {
		return nil, fmt.Errorf("claude extraction failed: %w", err)
	}

	// Parse JSON array from response - Claude may wrap it in prose or code blocks.
	raw := stripCodeBlock(result.Text)

	// Extract JSON array even if surrounded by prose text.
	if start := strings.Index(raw, "["); start >= 0 {
		if end := strings.LastIndex(raw, "]"); end > start {
			raw = raw[start : end+1]
		}
	}

	var facts []ingestMemory
	if err := json.Unmarshal([]byte(raw), &facts); err != nil {
		return nil, fmt.Errorf("failed to parse extraction result: %w (raw: %.200s)", err, result.Text)
	}

	resp := &ingestResponse{}
	for _, fact := range facts {
		text := strings.TrimSpace(fact.Text)
		if text == "" {
			continue
		}
		memType := fact.Type
		if memType != "fact" && memType != "preference" && memType != "decision" {
			memType = "fact"
		}
		_, err := h.Store.Store(text, memType, "user-import", nil)
		if err != nil {
			if strings.Contains(err.Error(), "duplicate") {
				resp.Skipped++
				continue
			}
			log.Printf("[CC] memory store error: %v", err)
			resp.Skipped++
			continue
		}
		resp.Imported++
		resp.Memories = append(resp.Memories, ingestMemory{Text: text, Type: memType})
	}

	return resp, nil
}

func (h *MemoryIngestHandler) handleContextDestination(w http.ResponseWriter, req ingestRequest) {
	if h.ContextStore == nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "context store not available"})
		return
	}

	name := strings.TrimSpace(req.FileName)
	if name == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "file_name is required for context destination"})
		return
	}
	if !validFileName.MatchString(name) {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "file_name must contain only letters, numbers, dashes, and underscores"})
		return
	}
	if protectedContextFiles[strings.ToLower(name)] {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("%q is a protected system file and cannot be overwritten", name),
		})
		return
	}

	resp, err := h.saveToContext(req.Content, req.Instruction, name, req.Tier)
	if err != nil {
		log.Printf("[CC] context save error: %v", err)
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed - check server logs"})
		return
	}
	respondJSON(w, http.StatusOK, resp)
}

func (h *MemoryIngestHandler) saveToContext(content, instruction, fileName, tierName string) (map[string]any, error) {
	var body string

	if strings.EqualFold(instruction, "store-as-is") {
		body = content
	} else {
		// Summarize: Claude distills content into clean markdown.
		prompt := fmt.Sprintf(`Summarize the following content into concise, well-structured markdown.
Use bullet points for key items. Keep only important information. No preamble or explanation - output ONLY the markdown summary.

Content:
---
%s
---`, content)

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		params := provider.Params{
			Model:    "claude-haiku-4-5",
			MaxTurns: 1,
			Tools:    []string{""},
		}
		if tier := h.resolveTier(tierName); tier != nil {
			if tier.Model != "" {
				params.Model = tier.Model
			}
			if tier.Effort != "" {
				params.Effort = tier.Effort
			}
		}

		result, err := h.Provider.Invoke(ctx, prompt, params, nil)
		if err != nil {
			return nil, fmt.Errorf("summarization failed: %w", err)
		}

		body = strings.TrimSpace(result.Text) + "\n"
	}

	if err := h.ContextStore.Put(fileName, []byte(body)); err != nil {
		return nil, fmt.Errorf("failed to write context file: %w", err)
	}

	return map[string]any{
		"file_name": fileName + ".md",
		"imported":  1,
	}, nil
}

// MemoryTiersHandler returns the list of enabled tiers for the Teach UI dropdown.
type MemoryTiersHandler struct {
	TierStore TierStore
}

func (h *MemoryTiersHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	type tierInfo struct {
		Name         string `json:"name"`
		Model        string `json:"model"`
		Tools        bool   `json:"tools"`
		ForceCommand bool   `json:"force_command"`
	}

	var tiers []tierInfo
	if h.TierStore != nil {
		if tc := h.TierStore.Current(); tc != nil {
			for _, t := range tc.Tiers {
				if !t.Enabled {
					continue
				}
				tiers = append(tiers, tierInfo{
					Name:         t.Name,
					Model:        t.Model,
					Tools:        len(t.Tools) > 0,
					ForceCommand: t.ForceCommand,
				})
			}
		}
	}
	if tiers == nil {
		tiers = []tierInfo{}
	}
	respondJSON(w, http.StatusOK, tiers)
}

func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
