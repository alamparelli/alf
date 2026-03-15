package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockProvider records calls and returns canned responses.
type mockProvider struct {
	response string
	err      error
	calls    []mockCall
}

type mockCall struct {
	Prompt string
	Params ExtractorParams
}

func (m *mockProvider) Invoke(_ context.Context, prompt string, params ExtractorParams) (string, error) {
	m.calls = append(m.calls, mockCall{Prompt: prompt, Params: params})
	return m.response, m.err
}

func TestCollectConversations(t *testing.T) {
	dir := t.TempDir()
	eventsDir := filepath.Join(dir, "logs", "events")
	os.MkdirAll(eventsDir, 0o755)

	// Write a day file with mixed events.
	today := time.Now().Format("2006-01-02")
	ts1 := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	ts2 := time.Now().Add(-30 * time.Minute).Format(time.RFC3339)
	tsOld := time.Now().Add(-25 * time.Hour).Format(time.RFC3339) // should be filtered by since

	lines := fmt.Sprintf(
		`{"event":"message_in","ts":"%s","text":"hello"}
{"event":"message_out","ts":"%s","text":"hi there"}
{"event":"message_in","ts":"%s","text":"old message"}
{"event":"router_classify","ts":"%s","text":"ignored"}
{"event":"message_in","ts":"%s","text":""}
`,
		ts1, ts2, tsOld, ts1, ts1)

	os.WriteFile(filepath.Join(eventsDir, today+".jsonl"), []byte(lines), 0o644)

	e := &Extractor{dataDir: dir}
	since := time.Now().Add(-2 * time.Hour)

	convos, err := e.collectConversations(since)
	if err != nil {
		t.Fatalf("collectConversations: %v", err)
	}

	if len(convos) != 2 {
		t.Fatalf("expected 2 conversations, got %d", len(convos))
	}
	if convos[0].role != "user" || convos[0].text != "hello" {
		t.Errorf("unexpected first line: %+v", convos[0])
	}
	if convos[1].role != "assistant" || convos[1].text != "hi there" {
		t.Errorf("unexpected second line: %+v", convos[1])
	}
}

func TestCollectConversations_MultiDay(t *testing.T) {
	dir := t.TempDir()
	eventsDir := filepath.Join(dir, "logs", "events")
	os.MkdirAll(eventsDir, 0o755)

	yesterday := time.Now().AddDate(0, 0, -1)
	today := time.Now()

	tsYesterday := yesterday.Format(time.RFC3339)
	tsToday := today.Add(-1 * time.Hour).Format(time.RFC3339)

	os.WriteFile(
		filepath.Join(eventsDir, yesterday.Format("2006-01-02")+".jsonl"),
		[]byte(fmt.Sprintf(`{"event":"message_in","ts":"%s","text":"yesterday msg"}`+"\n", tsYesterday)),
		0o644,
	)
	os.WriteFile(
		filepath.Join(eventsDir, today.Format("2006-01-02")+".jsonl"),
		[]byte(fmt.Sprintf(`{"event":"message_out","ts":"%s","text":"today reply"}`+"\n", tsToday)),
		0o644,
	)

	e := &Extractor{dataDir: dir}
	since := yesterday.Add(-1 * time.Hour)

	convos, err := e.collectConversations(since)
	if err != nil {
		t.Fatalf("collectConversations: %v", err)
	}
	if len(convos) != 2 {
		t.Fatalf("expected 2 conversations across days, got %d", len(convos))
	}
}

func TestCollectConversations_NoEventsDir(t *testing.T) {
	dir := t.TempDir() // no logs/events subdir

	e := &Extractor{dataDir: dir}
	convos, err := e.collectConversations(time.Now().Add(-1 * time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(convos) != 0 {
		t.Fatalf("expected 0 conversations, got %d", len(convos))
	}
}

func TestCollectConversations_TruncatesLongOutput(t *testing.T) {
	dir := t.TempDir()
	eventsDir := filepath.Join(dir, "logs", "events")
	os.MkdirAll(eventsDir, 0o755)

	today := time.Now().Format("2006-01-02")
	ts := time.Now().Add(-10 * time.Minute).Format(time.RFC3339)
	longText := ""
	for i := 0; i < 200; i++ {
		longText += "word "
	}

	line := fmt.Sprintf(`{"event":"message_out","ts":"%s","text":"%s"}`+"\n", ts, longText)
	os.WriteFile(filepath.Join(eventsDir, today+".jsonl"), []byte(line), 0o644)

	e := &Extractor{dataDir: dir}
	convos, err := e.collectConversations(time.Now().Add(-1 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(convos) != 1 {
		t.Fatalf("expected 1, got %d", len(convos))
	}
	if len(convos[0].text) > 504 { // 500 + "..."
		t.Errorf("text not truncated: len=%d", len(convos[0].text))
	}
}

func TestExtractFacts_CleanJSON(t *testing.T) {
	prov := &mockProvider{
		response: `[{"text":"user prefers Go","type":"preference"},{"text":"project uses Docker","type":"fact"}]`,
	}
	e := &Extractor{dataDir: "/tmp", timeout: time.Minute, provider: prov}

	facts, err := e.extractFacts("some conversation text")
	if err != nil {
		t.Fatalf("extractFacts: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(facts))
	}
	if facts[0].Text != "user prefers Go" || facts[0].Type != "preference" {
		t.Errorf("unexpected fact[0]: %+v", facts[0])
	}
	if facts[1].Text != "project uses Docker" || facts[1].Type != "fact" {
		t.Errorf("unexpected fact[1]: %+v", facts[1])
	}

	// Verify provider was called with correct params.
	if len(prov.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(prov.calls))
	}
	if prov.calls[0].Params.Model != "claude-haiku-4-5" {
		t.Errorf("expected haiku model, got %s", prov.calls[0].Params.Model)
	}
	if prov.calls[0].Params.MaxTurns != 1 {
		t.Errorf("expected max_turns=1, got %d", prov.calls[0].Params.MaxTurns)
	}
}

func TestExtractFacts_MarkdownWrapped(t *testing.T) {
	prov := &mockProvider{
		response: "```json\n[{\"text\":\"wrapped fact\",\"type\":\"fact\"}]\n```",
	}
	e := &Extractor{dataDir: "/tmp", timeout: time.Minute, provider: prov}

	facts, err := e.extractFacts("conversation")
	if err != nil {
		t.Fatalf("extractFacts: %v", err)
	}
	if len(facts) != 1 || facts[0].Text != "wrapped fact" {
		t.Errorf("failed to parse markdown-wrapped JSON: %+v", facts)
	}
}

func TestExtractFacts_ProviderError(t *testing.T) {
	prov := &mockProvider{err: fmt.Errorf("connection refused")}
	e := &Extractor{dataDir: "/tmp", timeout: time.Minute, provider: prov}

	_, err := e.extractFacts("conversation")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "claude extraction: connection refused" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestExtractFacts_InvalidJSON(t *testing.T) {
	prov := &mockProvider{response: "I couldn't extract any facts from this conversation."}
	e := &Extractor{dataDir: "/tmp", timeout: time.Minute, provider: prov}

	_, err := e.extractFacts("conversation")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if got := err.Error(); !contains(got, "parse extraction response") {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestExtractFacts_EmptyArray(t *testing.T) {
	prov := &mockProvider{response: "[]"}
	e := &Extractor{dataDir: "/tmp", timeout: time.Minute, provider: prov}

	facts, err := e.extractFacts("conversation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("expected 0 facts, got %d", len(facts))
	}
}

func TestStatePersistence(t *testing.T) {
	dir := t.TempDir()

	e := &Extractor{
		statePath: filepath.Join(dir, "state.json"),
	}

	// Default state when no file exists.
	state := e.loadState()
	if time.Since(state.LastRun) > 4*time.Hour {
		t.Errorf("default state should be ~3h ago, got %s ago", time.Since(state.LastRun))
	}

	// Save and reload.
	e.saveState()
	state = e.loadState()
	if time.Since(state.LastRun) > 5*time.Second {
		t.Errorf("saved state should be recent, got %s ago", time.Since(state.LastRun))
	}

	// Verify file content.
	data, _ := os.ReadFile(e.statePath)
	var saved ExtractorState
	json.Unmarshal(data, &saved)
	if time.Since(saved.LastRun) > 5*time.Second {
		t.Errorf("persisted state should be recent")
	}
}

func TestStatePersistence_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	os.WriteFile(statePath, []byte("not json"), 0o644)

	e := &Extractor{statePath: statePath}
	state := e.loadState()
	if time.Since(state.LastRun) > 4*time.Hour {
		t.Errorf("corrupt state should fallback to ~3h ago")
	}
}

func TestNewExtractor_Defaults(t *testing.T) {
	prov := &mockProvider{}
	e := NewExtractor(nil, "/data", "/ctx", ExtractorConfig{}, prov, nil)

	if e.interval != 3*time.Hour {
		t.Errorf("expected 3h interval, got %s", e.interval)
	}
	if e.timeout != 5*time.Minute {
		t.Errorf("expected 5m timeout, got %s", e.timeout)
	}
	if e.provider != prov {
		t.Error("provider not set")
	}
}

func TestNewExtractor_CustomValues(t *testing.T) {
	prov := &mockProvider{}
	e := NewExtractor(nil, "/data", "/ctx", ExtractorConfig{
		Interval:    1 * time.Hour,
		Timeout:     2 * time.Minute,
		BootDelay:   30 * time.Second,
		MinMessages: 5,
	}, prov, nil)

	if e.interval != 1*time.Hour {
		t.Errorf("expected 1h interval, got %s", e.interval)
	}
	if e.timeout != 2*time.Minute {
		t.Errorf("expected 2m timeout, got %s", e.timeout)
	}
	if e.bootDelay != 30*time.Second {
		t.Errorf("expected 30s boot delay, got %s", e.bootDelay)
	}
	if e.minMessages != 5 {
		t.Errorf("expected 5 min messages, got %d", e.minMessages)
	}
}

func TestExtractor_TruncateText(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"longer than max", 5, "longe..."},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncateText(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncateText(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
