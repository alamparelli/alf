package memory

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/provider"
)

const (
	preferencesFile       = "preferences.md"
	consolidateThreshold  = 20 // consolidate after this many entries
)

var preferencesMu sync.Mutex

// AppendPreference adds a learning to the preferences file.
// If the file exceeds the threshold, it triggers consolidation.
func AppendPreference(contextDir string, learning, sentiment, emoji string) {
	preferencesMu.Lock()
	defer preferencesMu.Unlock()

	path := filepath.Join(contextDir, preferencesFile)

	// Create file with header if it doesn't exist.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		header := "# User Preferences\n\nLearned from reactions and feedback.\n\n"
		os.WriteFile(path, []byte(header), 0o644)
	}

	prefix := "+"
	if sentiment == "negative" {
		prefix = "-"
	}
	entry := fmt.Sprintf("- [%s] %s (%s)\n", prefix, learning, emoji)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[preferences] failed to open file: %v", err)
		return
	}
	defer f.Close()
	f.WriteString(entry)

	log.Printf("[preferences] appended: %s %q (%s)", prefix, learning, emoji)
}

// CountEntries returns the number of bullet entries in the preferences file.
func CountEntries(contextDir string) int {
	path := filepath.Join(contextDir, preferencesFile)
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.HasPrefix(strings.TrimSpace(scanner.Text()), "- [") {
			count++
		}
	}
	return count
}

// ConsolidatePreferences uses an LLM to summarize preferences when they exceed the threshold.
// It replaces the file with a structured summary.
func ConsolidatePreferences(contextDir string, prov provider.Provider, model string) {
	preferencesMu.Lock()
	defer preferencesMu.Unlock()

	path := filepath.Join(contextDir, preferencesFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)

	// Count entries.
	lines := strings.Split(content, "\n")
	count := 0
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "- [") {
			count++
		}
	}
	if count < consolidateThreshold {
		return
	}

	log.Printf("[preferences] consolidating %d entries", count)

	prompt := fmt.Sprintf(`Consolidate these user preference entries into a structured summary. Group by category (Communication, Format, Tone, Topics, Dislikes). Merge duplicates, keep specific details, remove redundancy.

Current entries:
%s

Output format (markdown):
# User Preferences

Learned from reactions and feedback.

## Communication
- ...

## Format
- ...

## Tone
- ...

## Topics
- ...

## Dislikes
- ...

Rules:
- Keep only categories that have entries
- Each bullet should be a clear, actionable preference
- Preserve the [+] or [-] prefix to indicate positive/negative
- If two entries contradict, keep the most recent (last in list)
- Output ONLY the markdown, nothing else`, content)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := prov.Invoke(ctx, prompt, provider.Params{
		Model:    model,
		MaxTurns: 1,
		Bare:     true,
	}, nil)
	if err != nil {
		log.Printf("[preferences] consolidation failed: %v", err)
		return
	}

	summary := strings.TrimSpace(result.Text)
	summary = strings.TrimPrefix(summary, "```markdown")
	summary = strings.TrimPrefix(summary, "```")
	summary = strings.TrimSuffix(summary, "```")
	summary = strings.TrimSpace(summary)

	if !strings.Contains(summary, "# User Preferences") {
		log.Printf("[preferences] consolidation returned unexpected format, skipping")
		return
	}

	if err := os.WriteFile(path, []byte(summary+"\n"), 0o644); err != nil {
		log.Printf("[preferences] failed to write consolidated file: %v", err)
		return
	}

	log.Printf("[preferences] consolidated %d entries into structured summary", count)
}
