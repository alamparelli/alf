package memstore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ExtractorProvider invokes Claude and returns text output.
type ExtractorProvider interface {
	Invoke(ctx context.Context, prompt string, params ExtractorParams) (string, error)
}

// ExtractorParams mirrors the subset of provider params needed for extraction.
type ExtractorParams struct {
	Model    string
	MaxTurns int
	DataDir  string
}

// Extractor periodically extracts facts from event logs and stores them.
type Extractor struct {
	store     *Store
	dataDir   string         // root data dir (for logs, claude working dir)
	stateDir  string         // where to store state file (context dir)
	interval  time.Duration
	timeout   time.Duration  // timeout for Claude extraction call
	statePath string
	stop      chan struct{}
	provider  ExtractorProvider
}

// ExtractorState holds the persisted state of the extractor.
type ExtractorState struct {
	LastRun time.Time `json:"last_run"`
}

// Keep unexported alias for internal use.
type extractorState = ExtractorState

type extractedFact struct {
	Text string `json:"text"`
	Type string `json:"type"` // "fact", "preference", "decision"
}

// NewExtractor creates a new periodic extraction job.
// provider is used to invoke Claude for fact extraction.
// timeout sets the max duration for each Claude extraction call (0 = default 5m).
func NewExtractor(store *Store, dataDir, contextDir string, interval, timeout time.Duration, prov ExtractorProvider) *Extractor {
	if interval <= 0 {
		interval = 3 * time.Hour
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &Extractor{
		store:     store,
		dataDir:   dataDir,
		stateDir:  contextDir,
		interval:  interval,
		timeout:   timeout,
		statePath: filepath.Join(contextDir, "memory_extractor_state.json"),
		stop:      make(chan struct{}),
		provider:  prov,
	}
}

// Start begins the periodic extraction goroutine.
func (e *Extractor) Start() {
	go e.loop()
}

// Stop signals the extraction loop to exit.
func (e *Extractor) Stop() {
	close(e.stop)
}

func (e *Extractor) loop() {
	// Delay first extraction to avoid competing with classifier for memory
	// at boot time. Both spawn Claude CLI processes — running them
	// simultaneously can OOM on constrained hosts.
	select {
	case <-time.After(3 * time.Minute):
	case <-e.stop:
		return
	}

	state := e.loadState()
	if time.Since(state.LastRun) >= e.interval {
		if err := e.RunOnce(state.LastRun); err != nil {
			log.Printf("memstore: extraction failed: %v", err)
		}
	}

	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-e.stop:
			return
		case <-ticker.C:
			state := e.loadState()
			if err := e.RunOnce(state.LastRun); err != nil {
				log.Printf("memstore: extraction failed: %v", err)
			}
		}
	}
}

// RunOnce extracts facts from event logs since the given time.
func (e *Extractor) RunOnce(since time.Time) error {
	log.Printf("memstore: starting extraction (since=%s, timeout=%s)", since.Format(time.RFC3339), e.timeout)

	conversations, err := e.collectConversations(since)
	if err != nil {
		return fmt.Errorf("collect conversations: %w", err)
	}

	if len(conversations) < 3 {
		log.Printf("memstore: skipping extraction — only %d message pairs (will retry next cycle)", len(conversations))
		return nil
	}

	// Build conversation text for Claude.
	var sb strings.Builder
	for _, c := range conversations {
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", c.ts, c.role, c.text))
	}
	promptText := sb.String()
	log.Printf("memstore: extracting from %d messages (%d bytes prompt)", len(conversations), len(promptText))

	start := time.Now()
	facts, err := e.extractFacts(promptText)
	elapsed := time.Since(start)
	if err != nil {
		log.Printf("memstore: extraction failed after %s", elapsed.Round(time.Millisecond))
		return fmt.Errorf("extract facts: %w", err)
	}
	log.Printf("memstore: claude responded in %s", elapsed.Round(time.Millisecond))

	stored := 0
	for _, fact := range facts {
		if fact.Text == "" {
			continue
		}
		memType := fact.Type
		if memType != "fact" && memType != "preference" && memType != "decision" {
			memType = "fact"
		}
		_, err := e.store.Store(fact.Text, memType, "extractor", nil)
		if err != nil {
			if strings.Contains(err.Error(), "duplicate") {
				continue
			}
			log.Printf("memstore: store fact failed: %v", err)
			continue
		}
		stored++
	}

	log.Printf("memstore: extracted %d facts, stored %d (from %d message pairs)", len(facts), stored, len(conversations))
	e.saveState()
	return nil
}

type conversationLine struct {
	ts   string
	role string
	text string
}

func (e *Extractor) collectConversations(since time.Time) ([]conversationLine, error) {
	eventsDir := filepath.Join(e.dataDir, "logs", "events")

	// Collect relevant day files.
	now := time.Now()
	var lines []conversationLine

	for d := since; !d.After(now); d = d.AddDate(0, 0, 1) {
		dayFile := filepath.Join(eventsDir, d.Format("2006-01-02")+".jsonl")
		dayLines, err := e.readDayEvents(dayFile, since)
		if err != nil {
			continue // file may not exist
		}
		lines = append(lines, dayLines...)
	}

	return lines, nil
}

func (e *Extractor) readDayEvents(path string, since time.Time) ([]conversationLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []conversationLine
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)

	for scanner.Scan() {
		var event struct {
			Event string `json:"event"`
			TS    string `json:"ts"`
			Text  string `json:"text"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}

		ts, err := time.Parse(time.RFC3339, event.TS)
		if err != nil || ts.Before(since) {
			continue
		}

		switch event.Event {
		case "message_in":
			if event.Text != "" {
				lines = append(lines, conversationLine{
					ts:   event.TS,
					role: "user",
					text: event.Text,
				})
			}
		case "message_out":
			if event.Text != "" {
				lines = append(lines, conversationLine{
					ts:   event.TS,
					role: "assistant",
					text: truncateText(event.Text, 500),
				})
			}
		}
	}

	return lines, scanner.Err()
}

const extractionPrompt = `You are a JSON extraction tool. Your ONLY job is to read conversation logs and output a JSON array.
Do NOT reply to the conversations. Do NOT continue the conversation. Do NOT add any commentary.
You MUST respond with ONLY a valid JSON array — nothing else.

Output format (no other text allowed):
[{"text": "specific fact or decision", "type": "fact|preference|decision"}]

Rules:
- Only extract genuinely useful, specific information
- Skip greetings, small talk, and pleasantries
- "preference" = user likes/dislikes, style choices, workflow preferences
- "decision" = architectural choices, tech decisions, agreed approaches
- "fact" = everything else worth remembering (project details, names, context)
- Each fact should be self-contained and understandable without conversation context
- Always write facts in English regardless of conversation language
- Be concise but precise
- If no useful facts exist, return an empty array: []

<conversation_logs>
`

func (e *Extractor) extractFacts(conversationText string) ([]extractedFact, error) {
	prompt := extractionPrompt + conversationText + "\n</conversation_logs>"

	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()

	raw, err := e.provider.Invoke(ctx, prompt, ExtractorParams{
		Model:    "claude-haiku-4-5",
		MaxTurns: 1,
		DataDir:  e.dataDir,
	})
	if err != nil {
		return nil, fmt.Errorf("claude extraction: %w", err)
	}

	// Parse JSON array from response. Claude may wrap it in markdown code blocks
	// or add surrounding text despite instructions.
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var facts []extractedFact
	if err := json.Unmarshal([]byte(raw), &facts); err != nil {
		// Fallback: try to find a JSON array embedded in the response.
		if start := strings.Index(raw, "["); start != -1 {
			if end := strings.LastIndex(raw, "]"); end > start {
				if err2 := json.Unmarshal([]byte(raw[start:end+1]), &facts); err2 == nil {
					return facts, nil
				}
			}
		}
		return nil, fmt.Errorf("parse extraction response: %w (raw: %s)", err, truncateText(raw, 200))
	}

	return facts, nil
}

// LoadState returns the current extractor state (last run time).
func (e *Extractor) LoadState() extractorState {
	return e.loadState()
}

func (e *Extractor) loadState() extractorState {
	data, err := os.ReadFile(e.statePath)
	if err != nil {
		// Default to 3 hours ago so first run covers recent history.
		return extractorState{LastRun: time.Now().Add(-3 * time.Hour)}
	}
	var state extractorState
	if err := json.Unmarshal(data, &state); err != nil {
		return extractorState{LastRun: time.Now().Add(-3 * time.Hour)}
	}
	return state
}

func (e *Extractor) saveState() {
	state := extractorState{LastRun: time.Now()}
	data, _ := json.Marshal(state)
	os.WriteFile(e.statePath, data, 0o644)
}

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
