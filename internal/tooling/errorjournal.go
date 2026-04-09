package tooling

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxJournalEntries = 200

// ErrorKind distinguishes error sources.
const (
	ErrorKindTool = "tool"
	ErrorKindApp  = "app"
)

// ToolError records a single execution failure with enough context
// for the heartbeat LLM to diagnose and fix the issue.
type ToolError struct {
	Kind       string    `json:"kind,omitempty"`       // "tool" or "app" (empty = "tool" for backwards compat)
	Tool       string    `json:"tool"`                 // tool name or app slug
	Args       string    `json:"args"`
	Error      string    `json:"error"`
	Stack      string    `json:"stack,omitempty"`       // stack trace (apps only)
	SourceHash string    `json:"source_hash,omitempty"` // SHA-256 of tool source at time of error
	Timestamp  time.Time `json:"timestamp"`
	Resolved   bool      `json:"resolved"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// ErrorJournal persists tool errors as JSONL for heartbeat-driven repair.
type ErrorJournal struct {
	path    string
	dataDir string
	mu      sync.Mutex
}

// NewErrorJournal creates a journal that writes to dataDir/logs/error-journal.jsonl.
func NewErrorJournal(dataDir string) *ErrorJournal {
	return &ErrorJournal{
		path:    filepath.Join(dataDir, "logs", "error-journal.jsonl"),
		dataDir: dataDir,
	}
}

// Append logs a tool error with its source hash.
func (j *ErrorJournal) Append(toolName, args, errOutput string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	entry := ToolError{
		Kind:       ErrorKindTool,
		Tool:       toolName,
		Args:       truncate(args, 2000),
		Error:      truncate(errOutput, 2000),
		SourceHash: j.hashToolSource(toolName),
		Timestamp:  time.Now().UTC(),
	}

	j.appendLocked(entry)
}

// AppendAppError logs an app error (from frontend JS or backend crash).
func (j *ErrorJournal) AppendAppError(slug, message, stack string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	entry := ToolError{
		Kind:      ErrorKindApp,
		Tool:      slug,
		Error:     truncate(message, 2000),
		Stack:     truncate(stack, 2000),
		Timestamp: time.Now().UTC(),
	}

	j.appendLocked(entry)
}

func (j *ErrorJournal) appendLocked(entry ToolError) {
	entries := j.loadLocked()
	entries = append(entries, entry)

	// Ring buffer: keep last N.
	if len(entries) > maxJournalEntries {
		entries = entries[len(entries)-maxJournalEntries:]
	}

	j.saveLocked(entries)
}

// ResolveByName marks all unresolved errors for a tool/app as resolved.
// Called when the tool executes successfully or app errors are cleared.
func (j *ErrorJournal) ResolveByName(name string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	entries := j.loadLocked()
	now := time.Now().UTC()
	changed := false
	for i := range entries {
		if entries[i].Tool == name && !entries[i].Resolved {
			entries[i].Resolved = true
			entries[i].ResolvedAt = &now
			changed = true
		}
	}
	if changed {
		j.saveLocked(entries)
	}
}

// Unresolved returns all unresolved errors, grouped by tool.
func (j *ErrorJournal) Unresolved() []ToolError {
	j.mu.Lock()
	defer j.mu.Unlock()

	var result []ToolError
	for _, e := range j.loadLocked() {
		if !e.Resolved {
			result = append(result, e)
		}
	}
	return result
}

// UnresolvedSummary returns a human-readable summary of unresolved errors
// suitable for injection into the heartbeat prompt.
func (j *ErrorJournal) UnresolvedSummary() string {
	unresolved := j.Unresolved()
	if len(unresolved) == 0 {
		return ""
	}

	// Group by kind + name.
	type entryStats struct {
		kind       string
		count      int
		lastError  string
		lastArgs   string
		lastStack  string
		lastHash   string
		currentHash string
	}
	grouped := make(map[string]*entryStats)
	var order []string

	for _, e := range unresolved {
		kind := e.effectiveKind()
		key := kind + ":" + e.Tool
		st, ok := grouped[key]
		if !ok {
			st = &entryStats{kind: kind}
			grouped[key] = st
			order = append(order, key)
		}
		st.count++
		st.lastError = e.Error
		st.lastArgs = e.Args
		st.lastStack = e.Stack
		st.lastHash = e.SourceHash
	}

	// Check current source hash for tools.
	for key, st := range grouped {
		if st.kind == ErrorKindTool {
			name := key[len(ErrorKindTool)+1:]
			st.currentHash = j.hashToolSource(name)
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Errors (%d unresolved)\n\n", len(unresolved)))
	sb.WriteString("The following tools/apps have recent errors. Diagnose and fix each one.\n")
	sb.WriteString("After fixing, test to confirm the fix works.\n\n")

	for _, key := range order {
		st := grouped[key]
		name := key[len(st.kind)+1:]

		label := "Tool"
		if st.kind == ErrorKindApp {
			label = "App"
		}

		modified := ""
		if st.kind == ErrorKindTool && st.lastHash != st.currentHash && st.currentHash != "" {
			modified = " (source modified since error — may be fixed, verify)"
		}

		sb.WriteString(fmt.Sprintf("### %s: %s (%d errors%s)\n", label, name, st.count, modified))
		sb.WriteString(fmt.Sprintf("- Last error: `%s`\n", truncate(st.lastError, 300)))
		if st.lastArgs != "" && st.lastArgs != "{}" {
			sb.WriteString(fmt.Sprintf("- Failing args: `%s`\n", truncate(st.lastArgs, 200)))
		}
		if st.lastStack != "" {
			sb.WriteString(fmt.Sprintf("- Stack: `%s`\n", truncate(st.lastStack, 300)))
		}
		if st.kind == ErrorKindTool {
			sb.WriteString(fmt.Sprintf("- Fix: read tool source at `~/data/tools/%s`, fix the bug, then run with the failing args\n", name))
		} else {
			sb.WriteString(fmt.Sprintf("- Fix: read app source at `~/data/apps/%s/`, check index.html or main.go for the error\n", name))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// effectiveKind returns the kind, defaulting to "tool" for backwards compat.
func (e *ToolError) effectiveKind() string {
	if e.Kind == "" {
		return ErrorKindTool
	}
	return e.Kind
}

// hashToolSource returns SHA-256 of the tool's current source file.
func (j *ErrorJournal) hashToolSource(toolName string) string {
	for _, dir := range []string{
		filepath.Join(j.dataDir, "tools"),
		filepath.Join(j.dataDir, "tools.d"),
	} {
		path := filepath.Join(dir, toolName)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		h := sha256.Sum256(data)
		return hex.EncodeToString(h[:8]) // short hash is enough
	}
	return ""
}

func (j *ErrorJournal) loadLocked() []ToolError {
	data, err := os.ReadFile(j.path)
	if err != nil {
		return nil
	}
	var entries []ToolError
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e ToolError
		if json.Unmarshal([]byte(line), &e) == nil {
			entries = append(entries, e)
		}
	}
	return entries
}

func (j *ErrorJournal) saveLocked(entries []ToolError) {
	os.MkdirAll(filepath.Dir(j.path), 0o755)
	var lines []string
	for _, e := range entries {
		b, _ := json.Marshal(e)
		lines = append(lines, string(b))
	}
	os.WriteFile(j.path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
