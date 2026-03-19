package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alamparelli/alf/pkg/appsdk"
)

type Entry struct {
	ID        string   `json:"id"`
	Content   string   `json:"content"`
	Mood      string   `json:"mood,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"created_at"`
}

func main() {
	app := appsdk.New("journal")

	app.Action("write", actionWrite)
	app.Action("read", actionRead)
	app.Action("search", actionSearch)
	app.Action("reflect", actionReflect)
	app.Action("delete", actionDelete)

	app.Run()
}

// entriesPath returns the path to entries.json inside DataDir.
func entriesPath(dataDir string) string {
	return filepath.Join(dataDir, "entries.json")
}

// loadEntries reads entries from disk. Creates the file with an empty array if missing.
func loadEntries(dataDir string) ([]Entry, error) {
	p := entriesPath(dataDir)

	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
		if err := os.WriteFile(p, []byte("[]"), 0o644); err != nil {
			return nil, fmt.Errorf("init entries file: %w", err)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read entries: %w", err)
	}

	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse entries: %w", err)
	}
	return entries, nil
}

// saveEntries writes entries back to disk.
func saveEntries(dataDir string, entries []Entry) error {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal entries: %w", err)
	}
	return os.WriteFile(entriesPath(dataDir), data, 0o644)
}

// stringSlice extracts a []string from the args map at key.
func stringSlice(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		// Single string value.
		if s, ok := v.(string); ok && s != "" {
			return []string{s}
		}
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func actionWrite(ctx *appsdk.Context) error {
	content := ctx.String("content")
	if content == "" {
		return fmt.Errorf("content is required")
	}

	entries, err := loadEntries(ctx.DataDir)
	if err != nil {
		return err
	}

	entry := Entry{
		ID:        strconv.FormatInt(time.Now().Unix(), 10),
		Content:   content,
		Mood:      ctx.String("mood"),
		Tags:      stringSlice(ctx.Args, "tags"),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	entries = append(entries, entry)
	if err := saveEntries(ctx.DataDir, entries); err != nil {
		return err
	}

	appsdk.Respond(fmt.Sprintf("Entry %s saved.", entry.ID))
	return nil
}

func actionRead(ctx *appsdk.Context) error {
	entries, err := loadEntries(ctx.DataDir)
	if err != nil {
		return err
	}

	// Specific entry by ID.
	if id := ctx.String("id"); id != "" {
		for _, e := range entries {
			if e.ID == id {
				appsdk.Respond(formatEntry(e))
				return nil
			}
		}
		return fmt.Errorf("entry %s not found", id)
	}

	// Last N entries, most recent first.
	limit := ctx.Int("limit", 10)
	if limit <= 0 {
		limit = 10
	}

	// Sort descending by created_at.
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt > sorted[j].CreatedAt
	})

	if limit > len(sorted) {
		limit = len(sorted)
	}
	sorted = sorted[:limit]

	if len(sorted) == 0 {
		appsdk.Respond("No entries yet.")
		return nil
	}

	var b strings.Builder
	for i, e := range sorted {
		if i > 0 {
			b.WriteString("\n---\n")
		}
		b.WriteString(formatEntry(e))
	}
	appsdk.Respond(b.String())
	return nil
}

func actionSearch(ctx *appsdk.Context) error {
	query := ctx.String("query")
	if query == "" {
		return fmt.Errorf("query is required")
	}

	entries, err := loadEntries(ctx.DataDir)
	if err != nil {
		return err
	}

	q := strings.ToLower(query)
	var matches []Entry
	for _, e := range entries {
		if containsQuery(e, q) {
			matches = append(matches, e)
		}
	}

	if len(matches) == 0 {
		appsdk.Respond("No matching entries.")
		return nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d matching entries:\n\n", len(matches)))
	for i, e := range matches {
		if i > 0 {
			b.WriteString("\n---\n")
		}
		b.WriteString(formatEntry(e))
	}
	appsdk.Respond(b.String())
	return nil
}

func actionReflect(ctx *appsdk.Context) error {
	days := ctx.Int("days", 7)
	if days <= 0 {
		days = 7
	}

	entries, err := loadEntries(ctx.DataDir)
	if err != nil {
		return err
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	var recent []Entry
	for _, e := range entries {
		t, err := time.Parse(time.RFC3339, e.CreatedAt)
		if err != nil {
			continue
		}
		if t.After(cutoff) {
			recent = append(recent, e)
		}
	}

	if len(recent) == 0 {
		appsdk.Respond(fmt.Sprintf("No entries in the last %d days.", days))
		return nil
	}

	// Mood distribution.
	moods := make(map[string]int)
	for _, e := range recent {
		mood := e.Mood
		if mood == "" {
			mood = "(none)"
		}
		moods[mood]++
	}

	// Tag frequency.
	tags := make(map[string]int)
	for _, e := range recent {
		for _, t := range e.Tags {
			tags[t]++
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Reflection — last %d days\n\n", days))
	b.WriteString(fmt.Sprintf("Total entries: %d\n\n", len(recent)))

	b.WriteString("Mood distribution:\n")
	for mood, count := range moods {
		b.WriteString(fmt.Sprintf("  %s: %d\n", mood, count))
	}

	if len(tags) > 0 {
		b.WriteString("\nTop tags:\n")
		// Sort tags by frequency descending.
		type tagCount struct {
			tag   string
			count int
		}
		sorted := make([]tagCount, 0, len(tags))
		for t, c := range tags {
			sorted = append(sorted, tagCount{t, c})
		}
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].count > sorted[j].count
		})
		for _, tc := range sorted {
			b.WriteString(fmt.Sprintf("  %s: %d\n", tc.tag, tc.count))
		}
	}

	appsdk.Respond(b.String())
	return nil
}

func actionDelete(ctx *appsdk.Context) error {
	id := ctx.String("id")
	if id == "" {
		return fmt.Errorf("id is required")
	}

	entries, err := loadEntries(ctx.DataDir)
	if err != nil {
		return err
	}

	found := false
	filtered := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, e)
	}

	if !found {
		return fmt.Errorf("entry %s not found", id)
	}

	if err := saveEntries(ctx.DataDir, filtered); err != nil {
		return err
	}

	appsdk.Respond(fmt.Sprintf("Entry %s deleted.", id))
	return nil
}

// containsQuery checks if any of the entry's content, mood, or tags contain the query substring.
func containsQuery(e Entry, q string) bool {
	if strings.Contains(strings.ToLower(e.Content), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Mood), q) {
		return true
	}
	for _, t := range e.Tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
}

// formatEntry returns a human-readable representation of an entry.
func formatEntry(e Entry) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[%s] %s\n", e.ID, e.CreatedAt))
	if e.Mood != "" {
		b.WriteString(fmt.Sprintf("Mood: %s\n", e.Mood))
	}
	if len(e.Tags) > 0 {
		b.WriteString(fmt.Sprintf("Tags: %s\n", strings.Join(e.Tags, ", ")))
	}
	b.WriteString(e.Content)
	b.WriteByte('\n')
	return b.String()
}
