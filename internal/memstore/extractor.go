package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/dedup"
)

// diffExcludes are git pathspec exclusions applied to Pass 1 stat and worktree
// checks. Binary/generated files are skipped, plus self-referential LLM/scheduler
// logs to prevent the memory extractor from feeding on its own output (observed
// to cause 18× prompt growth over 2 days on 2026-04-14/15).
var diffExcludes = []string{
	":!*.png", ":!*.jpg", ":!*.wav", ":!*.mp3", ":!*.bin",
	":!*.db", ":!*.db-shm", ":!*.db-wal",
	":!*.zip", ":!*.tar.gz",
	":!tools/go-path/", ":!tools/go/",
	":!logs/llm/", ":!logs/scheduler/",
}

// maxPass2DiffBytes caps the Pass 2 diff size fed to the LLM. Files selected
// by Pass 1 may still be large; truncate to keep a single extraction bounded.
const maxPass2DiffBytes = 200_000

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

// TierResolver returns the model name for the lowest-priority enabled tier.
type TierResolver func() string

// Extractor extracts facts from git diffs of the data directory.
// It is triggered event-driven (session end, message threshold) and
// also runs as a fallback via the consolidator cron.
type Extractor struct {
	store        *Store
	dataDir      string        // root data dir (git repo)
	stateDir     string        // where to store state file (context dir)
	timeout      time.Duration // timeout for Claude extraction call
	msgThreshold int           // message count before mid-session extraction
	statePath    string
	provider     ExtractorProvider
	tierResolver TierResolver // resolves cheapest tier model at runtime

	// Per-session message counters for mid-session extraction.
	msgCounts map[string]int
	mu        sync.Mutex

	// memStore, if non-nil, replaces the memstore.Store.Store write path
	// with dedup.IndexWithDedup (#337c4c). When set, the extractor
	// writes facts directly into memory.Store under scope=memType and
	// no longer touches the memstore memories table. Leave nil to keep
	// the legacy path alive during transitional deployments.
	memStore         memory.Store
	nearDupThreshold float32 // passed through to dedup.Options
}

// SetMemoryBackend rewires the extractor's write path onto memory.Store
// via the dedup helper. threshold controls near-dup skipping (see
// dedup.Options.NearDupThreshold); pass 0 to rely on exact-dup only.
// Calling with a nil store restores the legacy memstore path.
func (e *Extractor) SetMemoryBackend(store memory.Store, threshold float32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.memStore = store
	e.nearDupThreshold = threshold
}

// ExtractorState holds the persisted state of the extractor.
type ExtractorState struct {
	LastHash string    `json:"last_hash"`
	LastRun  time.Time `json:"last_run"`
}

// Keep unexported alias for internal use.
type extractorState = ExtractorState

type extractedFact struct {
	Text string `json:"text"`
	Type string `json:"type"` // "fact", "preference", "decision", "contact"
}

// ExtractorConfig holds configurable parameters for the Extractor.
type ExtractorConfig struct {
	Timeout      time.Duration // Claude call timeout (0 = 5m)
	MsgThreshold int           // messages before mid-session extraction (0 = 10)
}

// NewExtractor creates a new event-driven extractor.
func NewExtractor(store *Store, dataDir, contextDir string, cfg ExtractorConfig, prov ExtractorProvider, tierResolver TierResolver) *Extractor {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Minute
	}
	if cfg.MsgThreshold <= 0 {
		cfg.MsgThreshold = 10
	}
	// Ensure extraction guide exists on disk for user customization.
	guidePath := filepath.Join(contextDir, "extraction-guide.md")
	if _, err := os.Stat(guidePath); os.IsNotExist(err) {
		os.WriteFile(guidePath, []byte(defaultExtractionGuide), 0o644)
	}

	return &Extractor{
		store:        store,
		dataDir:      dataDir,
		stateDir:     contextDir,
		timeout:      cfg.Timeout,
		msgThreshold: cfg.MsgThreshold,
		statePath:    filepath.Join(contextDir, "memory_extractor_state.json"),
		provider:     prov,
		tierResolver: tierResolver,
		msgCounts:    make(map[string]int),
	}
}

// OnMessage is called after each message_out. Increments the per-session
// counter and triggers extraction when the threshold is reached.
func (e *Extractor) OnMessage(sessionID string) {
	if sessionID == "" {
		return
	}
	e.mu.Lock()
	e.msgCounts[sessionID]++
	count := e.msgCounts[sessionID]
	e.mu.Unlock()

	if count >= e.msgThreshold {
		e.mu.Lock()
		e.msgCounts[sessionID] = 0
		e.mu.Unlock()

		log.Printf("memstore: message threshold reached (%d) for session %s, triggering extraction", count, sessionID[:min(12, len(sessionID))])
		go func() {
			if err := e.Extract(); err != nil {
				log.Printf("memstore: threshold extraction failed: %v", err)
			}
		}()
	}
}

// OnSessionEnd is called when a session is archived (/new, /clear, timeout).
// Triggers extraction asynchronously.
func (e *Extractor) OnSessionEnd(sessionID string) {
	e.mu.Lock()
	delete(e.msgCounts, sessionID)
	e.mu.Unlock()

	log.Printf("memstore: session ended (%s), triggering extraction", sessionID[:min(12, len(sessionID))])
	go func() {
		if err := e.Extract(); err != nil {
			log.Printf("memstore: session-end extraction failed: %v", err)
		}
	}()
}

// Extract runs the two-pass git diff extraction.
// Safe to call concurrently — serialized internally.
func (e *Extractor) Extract() error {
	e.mu.Lock()
	// Prevent concurrent extractions.
	e.mu.Unlock()

	state := e.loadState()
	lastHash := state.LastHash

	// Get current HEAD.
	currentHash, err := e.gitCommand("rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	currentHash = strings.TrimSpace(currentHash)

	// Check for uncommitted working tree changes (staged + unstaged).
	worktreeMode := false
	if currentHash == lastHash {
		wtArgs := append([]string{"diff", "--stat", "--no-color", "HEAD", "--"}, diffExcludes...)
		wtStat, _ := e.gitCommand(wtArgs...)
		if strings.TrimSpace(wtStat) == "" {
			log.Printf("memstore: no new commits or working tree changes since last extraction")
			return nil
		}
		log.Printf("memstore: no new commits but found uncommitted changes")
		worktreeMode = true
	}

	// Pass 1: get diff stat.
	binaryExcludes := diffExcludes
	var statArgs []string
	if worktreeMode {
		// Diff working tree against HEAD.
		statArgs = append([]string{"diff", "--stat", "--no-color", "HEAD", "--"}, binaryExcludes...)
	} else if lastHash != "" {
		statArgs = append([]string{"diff", "--stat", "--no-color", lastHash + ".." + currentHash, "--"}, binaryExcludes...)
	} else {
		// First run: diff last 6 hours of commits.
		since := time.Now().Add(-6 * time.Hour).Format("2006-01-02T15:04:05")
		sinceHash, err := e.gitCommand("log", "--oneline", "--format=%h", "--since="+since, "--reverse")
		if err != nil || strings.TrimSpace(sinceHash) == "" {
			// Fallback: use root commit (safe for repos with any number of commits).
			rootHash, rootErr := e.gitCommand("rev-list", "--max-parents=0", "HEAD")
			if rootErr != nil || strings.TrimSpace(rootHash) == "" {
				log.Printf("memstore: cannot determine root commit, skipping extraction")
				e.saveState(currentHash)
				return nil
			}
			sinceHash = strings.TrimSpace(strings.Split(strings.TrimSpace(rootHash), "\n")[0])
			log.Printf("memstore: no commits found in last 6h, using root commit %s", sinceHash)
		} else {
			lines := strings.Split(strings.TrimSpace(sinceHash), "\n")
			sinceHash = lines[0]
		}
		statArgs = append([]string{"diff", "--stat", "--no-color", sinceHash + ".." + currentHash, "--"}, binaryExcludes...)
	}

	diffStat, err := e.gitCommand(statArgs...)
	if err != nil {
		return fmt.Errorf("git diff --stat: %w", err)
	}

	if strings.TrimSpace(diffStat) == "" {
		log.Printf("memstore: empty diff stat, advancing state")
		e.saveState(currentHash)
		return nil
	}

	// Cap diff stat to avoid blowing up the LLM context.
	const maxStatLines = 200
	lines := strings.Split(diffStat, "\n")
	if len(lines) > maxStatLines {
		log.Printf("memstore: diff stat too large (%d lines), truncating to %d", len(lines), maxStatLines)
		diffStat = strings.Join(lines[:maxStatLines], "\n") + "\n... (truncated)"
	}

	log.Printf("memstore: pass 1 — diff stat:\n%s", diffStat)

	// Pass 1: ask LLM which files to examine.
	selectedFiles, err := e.selectFiles(diffStat)
	if err != nil {
		return fmt.Errorf("select files: %w", err)
	}

	if len(selectedFiles) == 0 {
		log.Printf("memstore: LLM selected no files, advancing state")
		e.saveState(currentHash)
		return nil
	}

	log.Printf("memstore: pass 1 — LLM selected %d files: %v", len(selectedFiles), selectedFiles)

	// Pass 2: get actual diff for selected files.
	var diffArgs []string
	if worktreeMode {
		diffArgs = append([]string{"diff", "--no-color", "HEAD", "--"}, selectedFiles...)
	} else {
		diffRef := lastHash + ".." + currentHash
		if lastHash == "" {
			rootHash, _ := e.gitCommand("rev-list", "--max-parents=0", "HEAD")
			root := strings.TrimSpace(strings.Split(strings.TrimSpace(rootHash), "\n")[0])
			if root == "" {
				root = currentHash + "~1" // last resort
			}
			diffRef = root + ".." + currentHash
		}
		diffArgs = append([]string{"diff", "--no-color", diffRef, "--"}, selectedFiles...)
	}

	diffContent, err := e.gitCommand(diffArgs...)
	if err != nil {
		return fmt.Errorf("git diff selected files: %w", err)
	}

	if strings.TrimSpace(diffContent) == "" {
		log.Printf("memstore: empty diff content for selected files")
		e.saveState(currentHash)
		return nil
	}

	if len(diffContent) > maxPass2DiffBytes {
		log.Printf("memstore: pass 2 diff too large (%d bytes), truncating to %d", len(diffContent), maxPass2DiffBytes)
		diffContent = diffContent[:maxPass2DiffBytes] + "\n... (truncated)"
	}

	// Pass 2: extract facts from the diff.
	log.Printf("memstore: pass 2 — extracting from %d bytes of diff", len(diffContent))
	facts, err := e.extractFacts(diffContent)
	if err != nil {
		return fmt.Errorf("extract facts: %w", err)
	}

	// Snapshot the memory-backend config under the lock so a concurrent
	// SetMemoryBackend call doesn't flip mid-loop. Reading once here is
	// enough — the backend either is or isn't in place for this run.
	e.mu.Lock()
	memStore := e.memStore
	nearDupThreshold := e.nearDupThreshold
	e.mu.Unlock()

	stored := 0
	for i, fact := range facts {
		if fact.Text == "" {
			continue
		}
		memType := fact.Type
		if memType != "fact" && memType != "preference" && memType != "decision" && memType != "contact" {
			memType = "fact"
		}
		truncText := fact.Text
		if len(truncText) > 100 {
			truncText = truncText[:100] + "..."
		}
		log.Printf("memstore: fact[%d/%d] type=%s text=%q", i+1, len(facts), memType, truncText)

		if memStore != nil {
			// #337c4c path: write directly into memory.Store via dedup.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			res, err := dedup.IndexWithDedup(ctx, memStore, memory.Scope(memType), memory.Document{Text: fact.Text}, dedup.Options{
				Source:           "extractor",
				NearDupThreshold: nearDupThreshold,
				Now:              func() string { return time.Now().Format(time.RFC3339) },
			})
			cancel()
			if err != nil {
				log.Printf("memstore: fact[%d] → memory Index failed: %v", i+1, err)
				continue
			}
			if !res.Stored {
				log.Printf("memstore: fact[%d] → %s-dup, skipped (id=%s)", i+1, res.Reason, res.DocID)
				continue
			}
			log.Printf("memstore: fact[%d] → stored OK (id=%s via memory.Store)", i+1, res.DocID)
			stored++
			continue
		}

		// Legacy path: memstore.Store.Store with FTS5 dedup. Kept for
		// deployments that haven't wired SetMemoryBackend yet.
		id, err := e.store.Store(fact.Text, memType, "extractor", nil)
		if err != nil {
			if strings.Contains(err.Error(), "duplicate") {
				log.Printf("memstore: fact[%d] → duplicate, skipped", i+1)
				continue
			}
			log.Printf("memstore: fact[%d] → store failed: %v", i+1, err)
			continue
		}
		log.Printf("memstore: fact[%d] → stored OK (id=%d)", i+1, id)
		stored++
	}

	log.Printf("memstore: extracted %d facts, stored %d", len(facts), stored)
	e.saveState(currentHash)
	return nil
}

// HasChanges returns true if there are git changes (committed or uncommitted) since the last extraction.
func (e *Extractor) HasChanges() bool {
	state := e.loadState()
	if state.LastHash == "" {
		return true
	}
	currentHash, err := e.gitCommand("rev-parse", "HEAD")
	if err != nil {
		return true
	}
	if strings.TrimSpace(currentHash) != state.LastHash {
		return true
	}
	// Also check uncommitted working tree changes.
	wtStat, err := e.gitCommand("diff", "--stat", "--no-color", "HEAD")
	if err != nil {
		return false
	}
	return strings.TrimSpace(wtStat) != ""
}

// --- Pass 1: File selection ---

const fileSelectionPrompt = `You are a memory extraction assistant. Given a git diff --stat summary, select which files to examine.
Respond with ONLY a JSON array of file paths. If none worth examining, respond with: []
%s
<diff_stat>
%s
</diff_stat>`

func (e *Extractor) selectFiles(diffStat string) ([]string, error) {
	guide := e.loadExtractionGuide()
	prompt := fmt.Sprintf(fileSelectionPrompt, guide, diffStat)

	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()

	model := e.resolveModel()
	if model == "" {
		return nil, fmt.Errorf("no tier available for file selection (tierResolver returned empty)")
	}
	raw, err := e.provider.Invoke(ctx, prompt, ExtractorParams{
		Model:    model,
		MaxTurns: 1,
		DataDir:  e.dataDir,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM file selection: %w", err)
	}

	return parseJSONStringArray(raw)
}

// --- Pass 2: Fact extraction ---

const extractionPrompt = `You are a JSON extraction tool. Read a git diff and extract facts worth remembering.
Do NOT reply to conversations. Do NOT add commentary.
Respond with ONLY a valid JSON array.

Output format:
[{"text": "specific fact or decision", "type": "fact|preference|decision|contact"}]

Rules:
- Each fact must be self-contained and understandable without the diff
- Always write facts in English regardless of source language
- If no useful facts exist, return: []
%s
<git_diff>
`

func (e *Extractor) extractFacts(diffContent string) ([]extractedFact, error) {
	guide := e.loadExtractionGuide()
	// Sanitize XML boundary markers.
	sanitized := strings.ReplaceAll(diffContent, "</git_diff>", "")
	prompt := fmt.Sprintf(extractionPrompt, guide) + sanitized + "\n</git_diff>"

	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()

	model := e.resolveModel()
	if model == "" {
		return nil, fmt.Errorf("no tier available for fact extraction (tierResolver returned empty)")
	}
	raw, err := e.provider.Invoke(ctx, prompt, ExtractorParams{
		Model:    model,
		MaxTurns: 1,
		DataDir:  e.dataDir,
	})
	if err != nil {
		return nil, fmt.Errorf("extraction: %w", err)
	}

	return parseJSONFactArray(raw)
}

// --- Extraction guide ---

const defaultExtractionGuide = `# Memory Extraction Guide

## What to extract
- People names, contacts, emails, companies, roles
- Decisions and choices made
- User preferences, likes/dislikes, workflow habits
- Plans, deadlines, milestones
- New tools, skills, or capabilities added
- Project context, stats, metrics worth remembering

## What to skip
- Trivial changes (whitespace, formatting)
- Auto-generated data (digests, trending data, pipeline logs)
- Binary files
- Heartbeat, scheduler, or cache changes
- Repetitive log entries

## File selection hints
- Event logs (*.jsonl) contain conversations — always examine
- Markdown files often contain plans, analyses, and decisions
- Config changes may reflect important preference shifts
- Skill definitions describe new capabilities

## Fact types
- "contact" = people names, emails, companies, roles
- "preference" = likes/dislikes, style choices, workflow preferences
- "decision" = choices made, approaches agreed upon
- "fact" = everything else worth remembering

## Style
- Be concise but precise
- Each fact must be self-contained
- Always write facts in English regardless of source language
`

// loadExtractionGuide loads the user-customizable extraction guide.
// Creates a default one if it doesn't exist.
func (e *Extractor) loadExtractionGuide() string {
	path := filepath.Join(e.stateDir, "extraction-guide.md")
	data, err := os.ReadFile(path)
	if err != nil {
		// Create default guide.
		os.WriteFile(path, []byte(defaultExtractionGuide), 0o644)
		data = []byte(defaultExtractionGuide)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	return "\n<extraction_guide>\n" + content + "\n</extraction_guide>\n"
}

// --- Git helpers ---

func (e *Extractor) gitCommand(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", e.dataDir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s: %s", err, string(exitErr.Stderr))
		}
		return "", err
	}
	return string(out), nil
}

// --- State management ---

func (e *Extractor) loadState() extractorState {
	data, err := os.ReadFile(e.statePath)
	if err != nil {
		return extractorState{}
	}
	var state extractorState
	if err := json.Unmarshal(data, &state); err != nil {
		return extractorState{}
	}
	return state
}

// LoadState returns the current extractor state (exported).
func (e *Extractor) LoadState() extractorState {
	return e.loadState()
}

func (e *Extractor) saveState(hash string) {
	state := extractorState{LastHash: hash, LastRun: time.Now()}
	data, _ := json.Marshal(state)
	os.WriteFile(e.statePath, data, 0o644)
}

// resolveModel returns the model from the configured tier resolver.
// Returns "" if no tier is available — callers MUST handle this (#291).
// No hardcoded model fallback: users may run any backend (codex, ollama,
// anthropic, …) and a hardcoded Claude model would bypass their config.
func (e *Extractor) resolveModel() string {
	if e.tierResolver != nil {
		return e.tierResolver()
	}
	return ""
}

// --- JSON parsing helpers ---

func parseJSONStringArray(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var result []string
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		if start := strings.Index(raw, "["); start != -1 {
			if end := strings.LastIndex(raw, "]"); end > start {
				if err2 := json.Unmarshal([]byte(raw[start:end+1]), &result); err2 == nil {
					return result, nil
				}
			}
		}
		return nil, fmt.Errorf("parse file selection: %w (raw: %s)", err, truncateText(raw, 200))
	}
	return result, nil
}

func parseJSONFactArray(raw string) ([]extractedFact, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var facts []extractedFact
	if err := json.Unmarshal([]byte(raw), &facts); err != nil {
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

func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
