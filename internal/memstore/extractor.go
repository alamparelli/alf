package memstore

import (
	"bufio"
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
)

// Extractor periodically extracts facts from event logs and stores them.
type Extractor struct {
	store    *Store
	dataDir  string
	interval time.Duration
	statePath string
	stop     chan struct{}
}

type extractorState struct {
	LastRun time.Time `json:"last_run"`
}

type extractedFact struct {
	Text string `json:"text"`
	Type string `json:"type"` // "fact", "preference", "decision"
}

// NewExtractor creates a new periodic extraction job.
func NewExtractor(store *Store, dataDir string, interval time.Duration) *Extractor {
	if interval <= 0 {
		interval = 3 * time.Hour
	}
	return &Extractor{
		store:     store,
		dataDir:   dataDir,
		interval:  interval,
		statePath: filepath.Join(dataDir, "memory_extractor_state.json"),
		stop:      make(chan struct{}),
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
	// Run immediately if overdue.
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
	log.Printf("memstore: starting extraction (since=%s)", since.Format(time.RFC3339))

	conversations, err := e.collectConversations(since)
	if err != nil {
		return fmt.Errorf("collect conversations: %w", err)
	}

	if len(conversations) < 3 {
		log.Printf("memstore: skipping extraction — only %d message pairs", len(conversations))
		e.saveState()
		return nil
	}

	// Build conversation text for Claude.
	var sb strings.Builder
	for _, c := range conversations {
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", c.ts, c.role, c.text))
	}

	facts, err := e.extractFacts(sb.String())
	if err != nil {
		return fmt.Errorf("extract facts: %w", err)
	}

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

func (e *Extractor) extractFacts(conversationText string) ([]extractedFact, error) {
	prompt := `Extract important information from these conversations.
Return ONLY a JSON array, no other text:
[{"text": "specific fact or decision", "type": "fact|preference|decision"}]

Rules:
- Only extract genuinely useful, specific information
- Skip greetings, small talk, and pleasantries
- "preference" = user likes/dislikes, style choices, workflow preferences
- "decision" = architectural choices, tech decisions, agreed approaches
- "fact" = everything else worth remembering (project details, names, context)
- Each fact should be self-contained and understandable without conversation context
- Be concise but precise

Conversations:
` + conversationText

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "-p", prompt,
		"--model", "claude-haiku-4-5",
		"--output-format", "text",
		"--dangerously-skip-permissions",
	)
	cmd.Dir = e.dataDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("claude extraction: %w (stderr: %s)", err, stderr.String())
	}

	// Parse JSON array from response. Claude may wrap it in markdown code blocks.
	raw := strings.TrimSpace(stdout.String())
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var facts []extractedFact
	if err := json.Unmarshal([]byte(raw), &facts); err != nil {
		return nil, fmt.Errorf("parse extraction response: %w (raw: %s)", err, truncateText(raw, 200))
	}

	return facts, nil
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
