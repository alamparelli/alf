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
	s.SetDedupConfig(DedupConfig{TextThreshold: 1.1}) // disable dedup — test targets limit, not dedup

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
// Entity extraction & overlap
// ---------------------------------------------------------------------------

func TestExtractEntities(t *testing.T) {
	tests := []struct {
		text     string
		expected []string
		absent   []string
	}{
		{
			"Contact Pookie on YouTube for ALF testing",
			[]string{"Pookie", "YouTube"},
			[]string{"Contact", "ALF"}, // first word skipped; ALF is ≤3 chars
		},
		{
			"User sent email to Alessandro about Dictato marketing",
			[]string{"Alessandro", "Dictato"},
			[]string{"User"}, // first word
		},
		{
			"The quick brown Fox jumped. Next was the Cat",
			nil,
			[]string{"The", "Fox", "Next", "Cat"}, // first word; Fox/Cat ≤3 chars; Next after "."
		},
		{
			"all lowercase text here",
			nil, // no entities
			nil,
		},
		{
			"Meeting with John and Jane about ProjectX at Google",
			[]string{"John", "Jane", "ProjectX", "Google"},
			[]string{"Meeting"}, // first word
		},
		// French: articles/pronouns filtered by length
		{
			"Le projet avec Alessandro et Dictato fonctionne",
			[]string{"Alessandro", "Dictato"},
			[]string{"Le"}, // first word
		},
	}

	for _, tt := range tests {
		entities := extractEntities(tt.text)
		for _, e := range tt.expected {
			if !entities[e] {
				t.Errorf("extractEntities(%q): missing %q, got %v", tt.text, e, entities)
			}
		}
		for _, e := range tt.absent {
			if entities[e] {
				t.Errorf("extractEntities(%q): should not contain %q", tt.text, e)
			}
		}
	}
}

func TestEntityOverlap(t *testing.T) {
	tests := []struct {
		a, b map[string]bool
		min  float64
		max  float64
	}{
		// Same entities
		{map[string]bool{"Pookie": true, "YouTube": true}, map[string]bool{"Pookie": true, "YouTube": true}, 1.0, 1.0},
		// No overlap
		{map[string]bool{"Pookie": true, "ALF": true}, map[string]bool{"Dictato": true, "LinkedIn": true}, 0.0, 0.0},
		// Partial overlap
		{map[string]bool{"YouTube": true, "Pookie": true}, map[string]bool{"YouTube": true, "Dictato": true}, 0.49, 0.51},
		// Empty a
		{map[string]bool{}, map[string]bool{"Pookie": true}, 0.0, 0.0},
	}

	for _, tt := range tests {
		overlap := entityOverlap(tt.a, tt.b)
		if overlap < tt.min || overlap > tt.max {
			t.Errorf("entityOverlap(%v, %v) = %f, want [%f, %f]", tt.a, tt.b, overlap, tt.min, tt.max)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate: got %q", got)
	}
	if got := truncate("short", 80); got != "short" {
		t.Errorf("truncate: got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

func TestConcurrentStoreAndSearch(t *testing.T) {
	s := newTestStore(t)
	s.SetDedupConfig(DedupConfig{TextThreshold: 1.1}) // disable dedup — test targets concurrency, not dedup

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
// Socket input validation (#14)
// ---------------------------------------------------------------------------

func TestServerStoreEmptyText(t *testing.T) {
	_, sockPath := startTestServer(t)

	resp := socketRoundTrip(t, sockPath, socketRequest{
		Action: "store",
		Text:   "",
		Type:   "fact",
	})
	if resp.Error == "" {
		t.Fatal("expected error for empty text")
	}
	if !strings.Contains(resp.Error, "text required") {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestServerStoreTextTooLarge(t *testing.T) {
	_, sockPath := startTestServer(t)

	bigText := strings.Repeat("x", 11*1024) // 11KB > 10KB limit
	resp := socketRoundTrip(t, sockPath, socketRequest{
		Action: "store",
		Text:   bigText,
		Type:   "fact",
	})
	if resp.Error == "" {
		t.Fatal("expected error for oversized text")
	}
	if !strings.Contains(resp.Error, "too large") {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestServerStoreTextAtLimit(t *testing.T) {
	_, sockPath := startTestServer(t)

	// Exactly 10KB should be accepted.
	text := strings.Repeat("a", 10*1024)
	resp := socketRoundTrip(t, sockPath, socketRequest{
		Action: "store",
		Text:   text,
		Type:   "fact",
	})
	if resp.Error != "" {
		t.Fatalf("10KB text should be accepted, got: %s", resp.Error)
	}
}

func TestServerStoreInvalidType(t *testing.T) {
	_, sockPath := startTestServer(t)

	resp := socketRoundTrip(t, sockPath, socketRequest{
		Action: "store",
		Text:   "test memory",
		Type:   "bogus",
	})
	if resp.Error == "" {
		t.Fatal("expected error for invalid type")
	}
	if !strings.Contains(resp.Error, "invalid type") {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

func TestServerStoreValidTypes(t *testing.T) {
	_, sockPath := startTestServer(t)

	for _, typ := range []string{"fact", "summary", "preference", "decision"} {
		resp := socketRoundTrip(t, sockPath, socketRequest{
			Action: "store",
			Text:   "valid type test " + typ,
			Type:   typ,
		})
		if resp.Error != "" {
			t.Fatalf("type %q should be accepted, got: %s", typ, resp.Error)
		}
	}
}

func TestServerOversizedPayload(t *testing.T) {
	_, sockPath := startTestServer(t)

	conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send 70KB payload (> 64KB limit). The JSON decoder should fail.
	huge := `{"action":"store","text":"` + strings.Repeat("x", 70*1024) + `","type":"fact"}`
	conn.Write([]byte(huge + "\n"))

	var resp socketResponse
	json.NewDecoder(conn).Decode(&resp)
	if resp.Error == "" {
		t.Fatal("expected error for oversized payload")
	}
}

// ---------------------------------------------------------------------------
// Mock embedder for Store tests
// ---------------------------------------------------------------------------

type mockEmbedder struct {
	dims   int
	ready  bool
	embeds int // count of Embed calls
}

func (m *mockEmbedder) Embed(text string) ([]float32, error) {
	m.embeds++
	vec := make([]float32, m.dims)
	// Deterministic embedding based on text content (not just length).
	var hash uint32
	for _, c := range text {
		hash = hash*31 + uint32(c)
	}
	for i := range vec {
		hash = hash*1103515245 + 12345
		vec[i] = float32(int32(hash)) / float32(1<<31)
	}
	return vec, nil
}

func (m *mockEmbedder) EmbedQuery(text string) ([]float32, error) {
	return m.Embed(text)
}

func (m *mockEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i, t := range texts {
		v, _ := m.Embed(t)
		vecs[i] = v
	}
	return vecs, nil
}

func (m *mockEmbedder) IsReady() bool { return m.ready }
func (m *mockEmbedder) Dims() int     { return m.dims }

func newTestStoreWithEmbedder(t *testing.T) (*Store, *mockEmbedder) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_memory.db")

	emb := &mockEmbedder{dims: 384, ready: true}
	store, err := New(dbPath, emb)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store, emb
}

func TestStoreWithMockEmbedder(t *testing.T) {
	s, emb := newTestStoreWithEmbedder(t)

	id, err := s.Store("test memory with embedder", "fact", "test", nil)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if id <= 0 {
		t.Fatal("expected positive ID")
	}

	// Embed should have been called (once for dedup check + once for insert).
	if emb.embeds < 1 {
		t.Fatalf("expected embed calls, got %d", emb.embeds)
	}
}

func TestStoreSearchWithMockEmbedder(t *testing.T) {
	s, _ := newTestStoreWithEmbedder(t)

	s.Store("Go programming language is great for systems", "fact", "test", nil)
	s.Store("Python is popular for data science", "fact", "test", nil)

	results, err := s.Search("programming", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// With mock embedder, KNN should return results (not FTS5 fallback).
	if len(results) == 0 {
		t.Fatal("expected search results with mock embedder")
	}
}

func TestSetEmbedder(t *testing.T) {
	s := newTestStore(t) // starts with nil embedder

	emb := &mockEmbedder{dims: 384, ready: true}
	s.SetEmbedder(emb)

	// Now Store should use the embedder.
	_, err := s.Store("fact after embedder swap", "fact", "test", nil)
	if err != nil {
		t.Fatalf("Store after SetEmbedder: %v", err)
	}
	if emb.embeds < 1 {
		t.Fatal("expected embed calls after SetEmbedder")
	}
}

// Extractor tests are in extractor_test.go

// Regression for #193: dedup thresholds must be updatable at runtime
// (no daemon restart) so config hot-reload can propagate new values.
func TestSetDedupConfig_HotReload(t *testing.T) {
	s := newTestStore(t)

	initial := s.dedupCfg()
	if initial.TextThreshold != 0.7 || initial.CosineThreshold != 0.15 {
		t.Fatalf("unexpected defaults: %+v", initial)
	}

	applied := s.SetDedupConfig(DedupConfig{TextThreshold: 0.9, CosineThreshold: 0.08})
	if applied.TextThreshold != 0.9 || applied.CosineThreshold != 0.08 {
		t.Fatalf("applied config wrong: %+v", applied)
	}

	got := s.dedupCfg()
	if got.TextThreshold != 0.9 || got.CosineThreshold != 0.08 {
		t.Fatalf("dedup not swapped: %+v", got)
	}

	// Zero values must keep the existing threshold (partial update).
	applied = s.SetDedupConfig(DedupConfig{TextThreshold: 0.0, CosineThreshold: 0.05})
	if applied.TextThreshold != 0.9 {
		t.Fatalf("text threshold should be preserved on zero update, got %.2f", applied.TextThreshold)
	}
	if applied.CosineThreshold != 0.05 {
		t.Fatalf("cosine threshold should update to 0.05, got %.2f", applied.CosineThreshold)
	}
}
