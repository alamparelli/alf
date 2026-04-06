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
	response  string
	responses []string // if set, returns responses in order
	err       error
	calls     []mockCall
}

type mockCall struct {
	Prompt string
	Params ExtractorParams
}

func (m *mockProvider) Invoke(_ context.Context, prompt string, params ExtractorParams) (string, error) {
	m.calls = append(m.calls, mockCall{Prompt: prompt, Params: params})
	if len(m.responses) > 0 {
		idx := len(m.calls) - 1
		if idx < len(m.responses) {
			return m.responses[idx], m.err
		}
	}
	return m.response, m.err
}

func TestExtractFacts_CleanJSON(t *testing.T) {
	prov := &mockProvider{
		response: `[{"text":"user prefers Go","type":"preference"},{"text":"project uses Docker","type":"fact"}]`,
	}
	e := &Extractor{dataDir: "/tmp", stateDir: t.TempDir(), timeout: time.Minute, provider: prov}

	facts, err := e.extractFacts("some diff text")
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

	if len(prov.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(prov.calls))
	}
	if prov.calls[0].Params.Model != "claude-haiku-4-5" {
		t.Errorf("expected haiku model, got %s", prov.calls[0].Params.Model)
	}
}

func TestExtractFacts_MarkdownWrapped(t *testing.T) {
	prov := &mockProvider{
		response: "```json\n[{\"text\":\"wrapped fact\",\"type\":\"fact\"}]\n```",
	}
	e := &Extractor{dataDir: "/tmp", stateDir: t.TempDir(), timeout: time.Minute, provider: prov}

	facts, err := e.extractFacts("diff content")
	if err != nil {
		t.Fatalf("extractFacts: %v", err)
	}
	if len(facts) != 1 || facts[0].Text != "wrapped fact" {
		t.Errorf("failed to parse markdown-wrapped JSON: %+v", facts)
	}
}

func TestExtractFacts_ProviderError(t *testing.T) {
	prov := &mockProvider{err: fmt.Errorf("connection refused")}
	e := &Extractor{dataDir: "/tmp", stateDir: t.TempDir(), timeout: time.Minute, provider: prov}

	_, err := e.extractFacts("diff content")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "claude extraction: connection refused" {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestExtractFacts_InvalidJSON(t *testing.T) {
	prov := &mockProvider{response: "I couldn't extract any facts from this."}
	e := &Extractor{dataDir: "/tmp", stateDir: t.TempDir(), timeout: time.Minute, provider: prov}

	_, err := e.extractFacts("diff content")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if got := err.Error(); !contains(got, "parse extraction response") {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestExtractFacts_EmptyArray(t *testing.T) {
	prov := &mockProvider{response: "[]"}
	e := &Extractor{dataDir: "/tmp", stateDir: t.TempDir(), timeout: time.Minute, provider: prov}

	facts, err := e.extractFacts("diff content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(facts) != 0 {
		t.Errorf("expected 0 facts, got %d", len(facts))
	}
}

func TestExtractFacts_ContactType(t *testing.T) {
	prov := &mockProvider{
		response: `[{"text":"Miguel Rebelo (hello@mirebelo.com) — author of Zapier roundup","type":"contact"}]`,
	}
	e := &Extractor{dataDir: "/tmp", stateDir: t.TempDir(), timeout: time.Minute, provider: prov}

	facts, err := e.extractFacts("diff with contact info")
	if err != nil {
		t.Fatalf("extractFacts: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Type != "contact" {
		t.Errorf("expected type 'contact', got %q", facts[0].Type)
	}
}

func TestSelectFiles_ParsesResponse(t *testing.T) {
	prov := &mockProvider{
		response: `["logs/events/2026-03-17.jsonl", "context/plan.md"]`,
	}
	e := &Extractor{dataDir: "/tmp", stateDir: t.TempDir(), timeout: time.Minute, provider: prov}

	files, err := e.selectFiles("some stat output")
	if err != nil {
		t.Fatalf("selectFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0] != "logs/events/2026-03-17.jsonl" {
		t.Errorf("unexpected file[0]: %s", files[0])
	}
}

func TestSelectFiles_EmptyArray(t *testing.T) {
	prov := &mockProvider{response: "[]"}
	e := &Extractor{dataDir: "/tmp", stateDir: t.TempDir(), timeout: time.Minute, provider: prov}

	files, err := e.selectFiles("empty stat")
	if err != nil {
		t.Fatalf("selectFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestStatePersistence(t *testing.T) {
	dir := t.TempDir()

	e := &Extractor{
		statePath: filepath.Join(dir, "state.json"),
	}

	// Default state when no file exists.
	state := e.loadState()
	if state.LastHash != "" {
		t.Errorf("default state should have empty hash, got %q", state.LastHash)
	}

	// Save and reload.
	e.saveState("abc123")
	state = e.loadState()
	if state.LastHash != "abc123" {
		t.Errorf("expected hash abc123, got %q", state.LastHash)
	}
	if time.Since(state.LastRun) > 5*time.Second {
		t.Errorf("saved state should be recent, got %s ago", time.Since(state.LastRun))
	}

	// Verify file content.
	data, _ := os.ReadFile(e.statePath)
	var saved ExtractorState
	json.Unmarshal(data, &saved)
	if saved.LastHash != "abc123" {
		t.Errorf("persisted hash should be abc123, got %q", saved.LastHash)
	}
}

func TestStatePersistence_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	os.WriteFile(statePath, []byte("not json"), 0o644)

	e := &Extractor{statePath: statePath}
	state := e.loadState()
	if state.LastHash != "" {
		t.Errorf("corrupt state should have empty hash")
	}
}

func TestNewExtractor_Defaults(t *testing.T) {
	prov := &mockProvider{}
	e := NewExtractor(nil, "/data", "/ctx", ExtractorConfig{}, prov, nil)

	if e.timeout != 10*time.Minute {
		t.Errorf("expected 10m timeout, got %s", e.timeout)
	}
	if e.msgThreshold != 10 {
		t.Errorf("expected 10 msg threshold, got %d", e.msgThreshold)
	}
	if e.provider != prov {
		t.Error("provider not set")
	}
}

func TestNewExtractor_CustomValues(t *testing.T) {
	prov := &mockProvider{}
	e := NewExtractor(nil, "/data", "/ctx", ExtractorConfig{
		Timeout:      2 * time.Minute,
		MsgThreshold: 5,
	}, prov, nil)

	if e.timeout != 2*time.Minute {
		t.Errorf("expected 2m timeout, got %s", e.timeout)
	}
	if e.msgThreshold != 5 {
		t.Errorf("expected 5 msg threshold, got %d", e.msgThreshold)
	}
}

func TestOnMessage_Threshold(t *testing.T) {
	e := NewExtractor(nil, "/tmp", t.TempDir(), ExtractorConfig{
		MsgThreshold: 3,
	}, &mockProvider{}, nil)

	// Counter should increment.
	e.OnMessage("session-1")
	e.mu.Lock()
	count := e.msgCounts["session-1"]
	e.mu.Unlock()
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}

	e.OnMessage("session-1")
	e.mu.Lock()
	count = e.msgCounts["session-1"]
	e.mu.Unlock()
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}

func TestOnSessionEnd_ClearsCounter(t *testing.T) {
	e := NewExtractor(nil, "/tmp", t.TempDir(), ExtractorConfig{
		MsgThreshold: 100, // high threshold to avoid triggering extract
	}, &mockProvider{}, nil)

	e.OnMessage("session-1")
	e.OnMessage("session-1")

	// This will try to call Extract (which will fail since /tmp is not a git repo)
	// but we just want to verify the counter is cleared.
	e.OnSessionEnd("session-1")

	// Give goroutine time to start.
	time.Sleep(50 * time.Millisecond)

	e.mu.Lock()
	_, exists := e.msgCounts["session-1"]
	e.mu.Unlock()
	if exists {
		t.Error("session counter should be cleared after OnSessionEnd")
	}
}

func TestLoadExtractionGuide(t *testing.T) {
	dir := t.TempDir()
	e := &Extractor{stateDir: dir}

	// No file — should create default and return it.
	guide := e.loadExtractionGuide()
	if !contains(guide, "<extraction_guide>") {
		t.Errorf("default guide should be wrapped in XML tags, got %q", guide)
	}
	if !contains(guide, "What to extract") {
		t.Errorf("default guide should contain extraction instructions")
	}

	// Overwrite with custom guide.
	os.WriteFile(filepath.Join(dir, "extraction-guide.md"), []byte("Focus on marketing contacts and SEO decisions."), 0o644)
	guide = e.loadExtractionGuide()
	if !contains(guide, "marketing contacts") {
		t.Errorf("guide should contain user content, got %q", guide)
	}
	if !contains(guide, "<extraction_guide>") {
		t.Errorf("guide should be wrapped in XML tags, got %q", guide)
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

func TestParseJSONStringArray(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{`["a.txt", "b.md"]`, 2},
		{`[]`, 0},
		{"```json\n[\"a.txt\"]\n```", 1},
	}
	for _, tt := range tests {
		got, err := parseJSONStringArray(tt.input)
		if err != nil {
			t.Errorf("parseJSONStringArray(%q): %v", tt.input, err)
			continue
		}
		if len(got) != tt.want {
			t.Errorf("parseJSONStringArray(%q): got %d, want %d", tt.input, len(got), tt.want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
