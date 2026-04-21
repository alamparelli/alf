package memory

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// PreferencesFile is the on-disk file name, relative to the context directory,
// where user preference entries are appended.
const PreferencesFile = "preferences.md"

// PreferencesThreshold is the entry count above which curation.ConsolidatePreferences
// replaces the append-only list with a structured LLM summary.
const PreferencesThreshold = 20

var preferencesMu sync.Mutex

// AppendPreference adds a learning to the preferences file.
// If the file exceeds the threshold, callers should schedule consolidation via
// curation.ConsolidatePreferences (memory keeps the data plane, curation
// owns the LLM-driven reorganisation).
func AppendPreference(contextDir string, learning, sentiment, emoji string) {
	preferencesMu.Lock()
	defer preferencesMu.Unlock()

	path := filepath.Join(contextDir, PreferencesFile)

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
	path := filepath.Join(contextDir, PreferencesFile)
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

// LockPreferences / UnlockPreferences expose the file-level mutex to callers
// (curation.ConsolidatePreferences) that must serialise rewrites against
// concurrent AppendPreference calls.
func LockPreferences()   { preferencesMu.Lock() }
func UnlockPreferences() { preferencesMu.Unlock() }
