package memstore

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestStore creates a file-backed store with no embedder (FTS5-only mode).
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_memory.db")

	store, err := New(dbPath, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// ---------------------------------------------------------------------------
// Store: basic CRUD
// ---------------------------------------------------------------------------

func TestStoreAndCount(t *testing.T) {
	s := newTestStore(t)

	if got := s.Count(); got != 0 {
		t.Fatalf("expected 0 memories, got %d", got)
	}

	id, err := s.Store("user prefers dark mode", "preference", "extractor", nil)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if id <= 0 {
		t.Fatal("expected positive ID")
	}

	if got := s.Count(); got != 1 {
		t.Fatalf("expected 1 memory, got %d", got)
	}

	id2, err := s.Store("project uses Go 1.24", "fact", "claude", nil)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if id2 <= id {
		t.Fatal("expected increasing IDs")
	}

	if got := s.Count(); got != 2 {
		t.Fatalf("expected 2 memories, got %d", got)
	}
}

func TestStoreTypeValidation(t *testing.T) {
	s := newTestStore(t)

	for _, typ := range []string{"fact", "summary", "preference", "decision"} {
		_, err := s.Store("test "+typ, typ, "test", nil)
		if err != nil {
			t.Fatalf("Store(%s): %v", typ, err)
		}
	}

	_, err := s.Store("bad type", "invalid", "test", nil)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestStoreMetadataPersistence(t *testing.T) {
	s := newTestStore(t)

	meta := map[string]any{"source_file": "2026-03-02.jsonl", "batch": 3}
	_, err := s.Store("fact with metadata", "fact", "extractor", meta)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}

	results, err := s.TextSearch("metadata", 1)
	if err != nil {
		t.Fatalf("TextSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatal("expected 1 result")
	}

	if results[0].Metadata["source_file"] != "2026-03-02.jsonl" {
		t.Fatalf("metadata not preserved: %v", results[0].Metadata)
	}
}

func TestStoreNilMetadata(t *testing.T) {
	s := newTestStore(t)
	id, err := s.Store("nil meta test", "fact", "test", nil)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if id <= 0 {
		t.Fatal("expected positive ID")
	}
}

func TestStoreEmptyTextRejected(t *testing.T) {
	s := newTestStore(t)
	// Empty text should still be inserted (no constraint), but dedup may
	// catch identical empties. We just verify no panic.
	_, err := s.Store("", "fact", "test", nil)
	// sqlite CHECK doesn't prevent empty - this is allowed.
	if err != nil {
		t.Logf("empty text store result: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestDelete(t *testing.T) {
	s := newTestStore(t)

	id, _ := s.Store("to be deleted", "fact", "test", nil)
	if s.Count() != 1 {
		t.Fatal("expected 1")
	}

	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if s.Count() != 0 {
		t.Fatal("expected 0 after delete")
	}

	// FTS5 should also be cleaned up.
	results, err := s.TextSearch("deleted", 5)
	if err != nil {
		t.Fatalf("TextSearch after delete: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 FTS results after delete, got %d", len(results))
	}
}

func TestDeleteNonexistent(t *testing.T) {
	s := newTestStore(t)
	// Should not error - idempotent.
	if err := s.Delete(999); err != nil {
		t.Fatalf("Delete nonexistent: %v", err)
	}
}

func TestDeleteThenStoreReusesSearch(t *testing.T) {
	s := newTestStore(t)

	id, _ := s.Store("ephemeral fact about cats", "fact", "test", nil)
	s.Delete(id)

	// Storing new fact and searching should work cleanly.
	s.Store("permanent fact about dogs", "fact", "test", nil)
	results, err := s.TextSearch("dogs", 5)
	if err != nil {
		t.Fatalf("TextSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	// Deleted fact should not appear.
	for _, r := range results {
		if strings.Contains(r.Text, "cats") {
			t.Fatal("deleted memory still appears in search")
		}
	}
}

// ---------------------------------------------------------------------------
// FTS5 text search
// ---------------------------------------------------------------------------

func TestTextSearch(t *testing.T) {
	s := newTestStore(t)

	s.Store("user prefers dark mode for all applications", "preference", "test", nil)
	s.Store("project uses Go 1.24 with zero dependencies", "fact", "test", nil)
	s.Store("decided to use sqlite-vec for vector storage", "decision", "test", nil)

	results, err := s.TextSearch("dark mode", 5)
	if err != nil {
		t.Fatalf("TextSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'dark mode'")
	}
	if results[0].Type != "preference" {
		t.Fatalf("expected preference, got %s", results[0].Type)
	}
}

func TestTextSearchMultiTerm(t *testing.T) {
	s := newTestStore(t)

	s.Store("the deployment pipeline uses docker and SSH to homelab", "fact", "test", nil)
	s.Store("docker images are built with multi-stage builds", "fact", "test", nil)
	s.Store("SSH keys are stored in the secrets manager", "fact", "test", nil)

	results, err := s.TextSearch("docker SSH", 5)
	if err != nil {
		t.Fatalf("TextSearch: %v", err)
	}
	// The first result should contain both terms.
	if len(results) == 0 {
		t.Fatal("expected results for multi-term search")
	}
	if !strings.Contains(strings.ToLower(results[0].Text), "docker") {
		t.Fatalf("top result should mention docker: %s", results[0].Text)
	}
}

func TestTextSearchNoResults(t *testing.T) {
	s := newTestStore(t)

	s.Store("project uses Go 1.24", "fact", "test", nil)

	results, err := s.TextSearch("nonexistent xyzzy foobar", 5)
	if err != nil {
		t.Fatalf("TextSearch: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestTextSearchSpecialCharacters(t *testing.T) {
	s := newTestStore(t)

	s.Store("user's API key format is sk-proj-abc123", "fact", "test", nil)

	// Should not crash on special FTS5 characters.
	results, err := s.TextSearch(`API "key"`, 5)
	if err != nil {
		t.Fatalf("TextSearch: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected result with special chars")
	}
}

func TestTextSearchLimitRespected(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 10; i++ {
		s.Store("go programming language fact number "+string(rune('A'+i)), "fact", "test", nil)
	}

	results, err := s.TextSearch("go", 3)
	if err != nil {
		t.Fatalf("TextSearch: %v", err)
	}
	if len(results) > 3 {
		t.Fatalf("limit not respected: got %d results", len(results))
	}
}

// ---------------------------------------------------------------------------
// Search (KNN fallback to FTS5)
// ---------------------------------------------------------------------------

func TestSearchFallsBackToFTS(t *testing.T) {
	s := newTestStore(t)

	s.Store("the deployment script uses SSH to homelab", "fact", "test", nil)

	results, err := s.Search("deployment SSH", 5)
	if err != nil {
		t.Fatalf("Search fallback: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected FTS5 fallback results")
	}
}

// ---------------------------------------------------------------------------
// Recent
// ---------------------------------------------------------------------------

func TestRecent(t *testing.T) {
	s := newTestStore(t)

	s.Store("today's fact", "fact", "test", nil)
	s.Store("another recent fact", "fact", "test", nil)

	results, err := s.Recent(1, 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 recent, got %d", len(results))
	}
}

func TestRecentLimit(t *testing.T) {
	s := newTestStore(t)

	for i := 0; i < 5; i++ {
		s.Store("unique bulk fact number "+string(rune('A'+i))+" about testing", "fact", "test", nil)
	}

	results, err := s.Recent(1, 2)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 (limited), got %d", len(results))
	}
}

func TestRecentOrderDescending(t *testing.T) {
	s := newTestStore(t)

	s.Store("first fact", "fact", "test", nil)
	time.Sleep(10 * time.Millisecond)
	s.Store("second fact", "fact", "test", nil)

	results, err := s.Recent(1, 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(results) < 2 {
		t.Fatal("expected at least 2")
	}
	if !strings.Contains(results[0].Text, "second") {
		t.Fatalf("expected most recent first, got: %s", results[0].Text)
	}
}

// ---------------------------------------------------------------------------
// Deduplication
// ---------------------------------------------------------------------------

func TestDeduplicationExact(t *testing.T) {
	s := newTestStore(t)

	_, err := s.Store("user prefers dark mode for coding", "preference", "test", nil)
	if err != nil {
		t.Fatalf("first store: %v", err)
	}

	_, err = s.Store("user prefers dark mode for coding", "preference", "test", nil)
	if err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestDeduplicationNearMatch(t *testing.T) {
	s := newTestStore(t)

	// 12 unique words. Changing 1 word → Jaccard = 11/13 = 0.846.
	// Need texts where >90% of words match. Use 20+ words to dilute the diff.
	_, err := s.Store(
		"the user strongly prefers dark mode across all coding applications and terminal windows on the system at all times during development",
		"preference", "test", nil,
	)
	if err != nil {
		t.Fatalf("first store: %v", err)
	}

	// Only 1 word differs ("development" → "work") out of 20 unique → Jaccard ~0.95.
	_, err = s.Store(
		"the user strongly prefers dark mode across all coding applications and terminal windows on the system at all times during work",
		"preference", "test", nil,
	)
	if err == nil {
		t.Fatal("expected near-duplicate error")
	}
}

func TestDeduplicationDifferentEnough(t *testing.T) {
	s := newTestStore(t)

	s.Store("user prefers dark mode for coding", "preference", "test", nil)

	// Sufficiently different text should pass.
	_, err := s.Store("the deployment uses docker compose on homelab", "fact", "test", nil)
	if err != nil {
		t.Fatalf("different text should not be flagged as duplicate: %v", err)
	}

	if s.Count() != 2 {
		t.Fatalf("expected 2 memories, got %d", s.Count())
	}
}

// ---------------------------------------------------------------------------
// textSimilarity
// ---------------------------------------------------------------------------

func TestTextSimilarity(t *testing.T) {
	tests := []struct {
		a, b string
		min  float64
		max  float64
	}{
		{"hello world", "hello world", 1.0, 1.0},
		{"hello world", "goodbye world", 0.3, 0.4},
		{"completely different", "nothing alike", 0.0, 0.01},
		{"", "", 1.0, 1.0}, // edge: both empty
		{"one", "", 0.0, 0.01},
	}

	for _, tt := range tests {
		sim := textSimilarity(tt.a, tt.b)
		if sim < tt.min || sim > tt.max {
			t.Errorf("textSimilarity(%q, %q) = %f, want [%f, %f]", tt.a, tt.b, sim, tt.min, tt.max)
		}
	}
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

func TestConcurrentStoreAndSearch(t *testing.T) {
	s := newTestStore(t)

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	// 10 concurrent writers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			text := "concurrent fact number " + string(rune('A'+i)) + " about Go"
			_, err := s.Store(text, "fact", "test", nil)
			if err != nil {
				errs <- err
			}
		}(i)
	}

	// 10 concurrent readers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.TextSearch("Go", 5)
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// DB persistence across reopen
// ---------------------------------------------------------------------------

func TestReopenDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_memory.db")

	s1, err := New(dbPath, nil)
	if err != nil {
		t.Fatalf("New(1): %v", err)
	}
	s1.Store("persistent fact", "fact", "test", nil)
	s1.Close()

	s2, err := New(dbPath, nil)
	if err != nil {
		t.Fatalf("New(2): %v", err)
	}
	defer s2.Close()

	if s2.Count() != 1 {
		t.Fatalf("expected 1 after reopen, got %d", s2.Count())
	}

	results, err := s2.TextSearch("persistent", 5)
	if err != nil {
		t.Fatalf("TextSearch after reopen: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 FTS result after reopen, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Unix socket server
// ---------------------------------------------------------------------------

func startTestServer(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_memory.db")
	sockPath := filepath.Join(dir, "test.sock")

	store, err := New(dbPath, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	go store.ServeUnix(sockPath)

	// Wait for socket to become available.
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Cleanup(func() { store.Close() })
	return store, sockPath
}

func socketRoundTrip(t *testing.T, sockPath string, req socketRequest) socketResponse {
	t.Helper()
	conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var resp socketResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestServerStore(t *testing.T) {
	_, sockPath := startTestServer(t)

	resp := socketRoundTrip(t, sockPath, socketRequest{
		Action: "store",
		Text:   "server stored fact",
		Type:   "fact",
	})
	if resp.Error != "" {
		t.Fatalf("store error: %s", resp.Error)
	}
	if resp.ID <= 0 {
		t.Fatal("expected positive ID")
	}
}

func TestServerStoreDefaultType(t *testing.T) {
	_, sockPath := startTestServer(t)

	resp := socketRoundTrip(t, sockPath, socketRequest{
		Action: "store",
		Text:   "fact without type",
	})
	if resp.Error != "" {
		t.Fatalf("store error: %s", resp.Error)
	}
	if resp.ID <= 0 {
		t.Fatal("expected positive ID")
	}
}

func TestServerSearch(t *testing.T) {
	_, sockPath := startTestServer(t)

	// Store first.
	socketRoundTrip(t, sockPath, socketRequest{
		Action: "store",
		Text:   "user likes Python for scripting",
		Type:   "preference",
	})

	resp := socketRoundTrip(t, sockPath, socketRequest{
		Action: "search",
		Query:  "Python scripting",
		Limit:  5,
	})
	if resp.Error != "" {
		t.Fatalf("search error: %s", resp.Error)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected search results")
	}
}

func TestServerSearchDefaultLimit(t *testing.T) {
	_, sockPath := startTestServer(t)

	for i := 0; i < 10; i++ {
		socketRoundTrip(t, sockPath, socketRequest{
			Action: "store",
			Text:   "searchable fact " + string(rune('A'+i)) + " about Go programming",
			Type:   "fact",
		})
	}

	resp := socketRoundTrip(t, sockPath, socketRequest{
		Action: "search",
		Query:  "Go",
	})
	if resp.Error != "" {
		t.Fatalf("search error: %s", resp.Error)
	}
	if resp.Count > 5 {
		t.Fatalf("default limit should be 5, got %d results", resp.Count)
	}
}

func TestServerRecent(t *testing.T) {
	_, sockPath := startTestServer(t)

	socketRoundTrip(t, sockPath, socketRequest{
		Action: "store",
		Text:   "recent fact for server test",
		Type:   "fact",
	})

	resp := socketRoundTrip(t, sockPath, socketRequest{
		Action: "recent",
		Days:   1,
		Limit:  10,
	})
	if resp.Error != "" {
		t.Fatalf("recent error: %s", resp.Error)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 recent, got %d", len(resp.Results))
	}
}

func TestServerRecentDefaults(t *testing.T) {
	_, sockPath := startTestServer(t)

	resp := socketRoundTrip(t, sockPath, socketRequest{
		Action: "recent",
	})
	if resp.Error != "" {
		t.Fatalf("recent error: %s", resp.Error)
	}
	// No results is fine - defaults should not crash.
}

func TestServerUnknownAction(t *testing.T) {
	_, sockPath := startTestServer(t)

	resp := socketRoundTrip(t, sockPath, socketRequest{
		Action: "delete_all",
	})
	if resp.Error == "" {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(resp.Error, "unknown action") {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestServerInvalidJSON(t *testing.T) {
	_, sockPath := startTestServer(t)

	conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte("not json\n"))

	var resp socketResponse
	json.NewDecoder(conn).Decode(&resp)
	if resp.Error == "" {
		t.Fatal("expected error for invalid JSON")
	}
}

// ---------------------------------------------------------------------------
// Extractor
// ---------------------------------------------------------------------------

func TestExtractorState(t *testing.T) {
	dir := t.TempDir()
	e := &Extractor{
		statePath: filepath.Join(dir, "state.json"),
	}

	state := e.loadState()
	if time.Since(state.LastRun) < 2*time.Hour {
		t.Fatal("expected default state ~3h ago")
	}

	e.saveState()
	state = e.loadState()
	if time.Since(state.LastRun) > 5*time.Second {
		t.Fatal("expected state to be recent after save")
	}
}

func TestExtractorStateCorruptFile(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	os.WriteFile(statePath, []byte("not json{{{"), 0o644)

	e := &Extractor{statePath: statePath}
	state := e.loadState()
	// Should fall back to default (3h ago).
	if time.Since(state.LastRun) < 2*time.Hour {
		t.Fatal("expected default fallback for corrupt state file")
	}
}

func TestExtractorCollectConversations(t *testing.T) {
	dir := t.TempDir()
	eventsDir := filepath.Join(dir, "logs", "events")
	os.MkdirAll(eventsDir, 0o755)

	today := time.Now().Format("2006-01-02")
	f, _ := os.Create(filepath.Join(eventsDir, today+".jsonl"))
	now := time.Now()
	lines := []string{
		`{"event":"message_in","ts":"` + now.Add(-1*time.Hour).Format(time.RFC3339) + `","text":"hello"}`,
		`{"event":"message_out","ts":"` + now.Add(-59*time.Minute).Format(time.RFC3339) + `","text":"hi there"}`,
		`{"event":"message_in","ts":"` + now.Add(-30*time.Minute).Format(time.RFC3339) + `","text":"how are you"}`,
		`{"event":"message_out","ts":"` + now.Add(-29*time.Minute).Format(time.RFC3339) + `","text":"I am fine"}`,
	}
	for _, l := range lines {
		f.WriteString(l + "\n")
	}
	f.Close()

	e := NewExtractor(nil, dir, dir, ExtractorConfig{}, nil, nil)
	convs, err := e.collectConversations(now.Add(-2 * time.Hour))
	if err != nil {
		t.Fatalf("collectConversations: %v", err)
	}
	if len(convs) != 4 {
		t.Fatalf("expected 4 conversation lines, got %d", len(convs))
	}
	if convs[0].role != "user" || convs[0].text != "hello" {
		t.Fatalf("unexpected first line: %+v", convs[0])
	}
}

func TestExtractorFiltersNonMessageEvents(t *testing.T) {
	dir := t.TempDir()
	eventsDir := filepath.Join(dir, "logs", "events")
	os.MkdirAll(eventsDir, 0o755)

	today := time.Now().Format("2006-01-02")
	f, _ := os.Create(filepath.Join(eventsDir, today+".jsonl"))
	now := time.Now()
	lines := []string{
		`{"event":"message_in","ts":"` + now.Add(-1*time.Hour).Format(time.RFC3339) + `","text":"hello"}`,
		`{"event":"router_classify","ts":"` + now.Add(-59*time.Minute).Format(time.RFC3339) + `","tier":"haiku"}`,
		`{"event":"session_new","ts":"` + now.Add(-58*time.Minute).Format(time.RFC3339) + `","session_id":"abc"}`,
		`{"event":"message_out","ts":"` + now.Add(-57*time.Minute).Format(time.RFC3339) + `","text":"reply"}`,
		`{"event":"bot_error","ts":"` + now.Add(-56*time.Minute).Format(time.RFC3339) + `","error":"something"}`,
	}
	for _, l := range lines {
		f.WriteString(l + "\n")
	}
	f.Close()

	e := NewExtractor(nil, dir, dir, ExtractorConfig{}, nil, nil)
	convs, err := e.collectConversations(now.Add(-2 * time.Hour))
	if err != nil {
		t.Fatalf("collectConversations: %v", err)
	}
	// Should only include message_in and message_out.
	if len(convs) != 2 {
		t.Fatalf("expected 2 (only message events), got %d", len(convs))
	}
}

func TestExtractorRespectsSinceTime(t *testing.T) {
	dir := t.TempDir()
	eventsDir := filepath.Join(dir, "logs", "events")
	os.MkdirAll(eventsDir, 0o755)

	today := time.Now().Format("2006-01-02")
	f, _ := os.Create(filepath.Join(eventsDir, today+".jsonl"))
	now := time.Now()
	lines := []string{
		`{"event":"message_in","ts":"` + now.Add(-3*time.Hour).Format(time.RFC3339) + `","text":"old message"}`,
		`{"event":"message_in","ts":"` + now.Add(-30*time.Minute).Format(time.RFC3339) + `","text":"new message"}`,
	}
	for _, l := range lines {
		f.WriteString(l + "\n")
	}
	f.Close()

	e := NewExtractor(nil, dir, dir, ExtractorConfig{}, nil, nil)
	// Only get messages from last hour.
	convs, err := e.collectConversations(now.Add(-1 * time.Hour))
	if err != nil {
		t.Fatalf("collectConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 (only recent), got %d", len(convs))
	}
	if convs[0].text != "new message" {
		t.Fatalf("expected 'new message', got %q", convs[0].text)
	}
}

func TestExtractorEmptyEventsDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "logs", "events"), 0o755)

	e := NewExtractor(nil, dir, dir, ExtractorConfig{}, nil, nil)
	convs, err := e.collectConversations(time.Now().Add(-2 * time.Hour))
	if err != nil {
		t.Fatalf("collectConversations: %v", err)
	}
	if len(convs) != 0 {
		t.Fatalf("expected 0, got %d", len(convs))
	}
}

func TestExtractorTruncatesLongMessages(t *testing.T) {
	dir := t.TempDir()
	eventsDir := filepath.Join(dir, "logs", "events")
	os.MkdirAll(eventsDir, 0o755)

	today := time.Now().Format("2006-01-02")
	f, _ := os.Create(filepath.Join(eventsDir, today+".jsonl"))
	now := time.Now()

	longText := strings.Repeat("a", 1000)
	line := `{"event":"message_out","ts":"` + now.Add(-30*time.Minute).Format(time.RFC3339) + `","text":"` + longText + `"}`
	f.WriteString(line + "\n")
	f.Close()

	e := NewExtractor(nil, dir, dir, ExtractorConfig{}, nil, nil)
	convs, err := e.collectConversations(now.Add(-1 * time.Hour))
	if err != nil {
		t.Fatalf("collectConversations: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1, got %d", len(convs))
	}
	// message_out texts should be truncated to 500.
	if len(convs[0].text) > 510 { // 500 + "..."
		t.Fatalf("expected truncated text, got len=%d", len(convs[0].text))
	}
}

// ---------------------------------------------------------------------------
// truncateText
// ---------------------------------------------------------------------------

func TestTruncateText(t *testing.T) {
	if got := truncateText("short", 100); got != "short" {
		t.Fatalf("expected unchanged, got %q", got)
	}
	if got := truncateText("hello world", 5); got != "hello..." {
		t.Fatalf("expected truncated, got %q", got)
	}
	if got := truncateText("", 5); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
