package curation

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/memory"
)

// PrefInvoker is the minimal LLM call shape curation needs to rewrite the
// preferences file. Callers wrap whatever provider they own into this closure
// so curation stays free of ai/provider imports.
type PrefInvoker func(ctx context.Context, prompt string) (string, error)

// ConsolidatePreferences asks the LLM (via invoke) to summarise the preferences
// file when its entry count reaches memory.PreferencesThreshold. If invoke
// returns an error or the output doesn't look like the expected markdown, the
// file is left untouched.
//
// Moved out of the memory/ package in v0.7.9 to enforce the "memory must not
// import ai" rule from technical/ARCHITECTURE-v0.7.10.md §2.2.
func ConsolidatePreferences(contextDir string, invoke PrefInvoker) {
	if invoke == nil {
		return
	}

	memory.LockPreferences()
	defer memory.UnlockPreferences()

	path := filepath.Join(contextDir, memory.PreferencesFile)
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
	if count < memory.PreferencesThreshold {
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

	raw, err := invoke(ctx, prompt)
	if err != nil {
		log.Printf("[preferences] consolidation failed: %v", err)
		return
	}

	summary := strings.TrimSpace(raw)
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
