// Package memtest exposes a reusable contract-test harness for any
// memory.Store implementation. It lives in a separate package so the
// testing import does not leak into the main memory package's compiled
// binaries.
//
// Usage from a Store implementation's test file:
//
//	func TestMyStore(t *testing.T) {
//	    memtest.RunStoreContract(t, func() memory.Store { return newMyStore() })
//	}
//
// Any new Store implementation landing in Steps 1.2 / 1.3 MUST pass this
// harness unchanged.
package memtest

import (
	"context"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
)

// Factory returns a fresh, isolated Store for each sub-test.
type Factory func() memory.Store

// RunStoreContract exercises the entire memory.Store contract defined in
// internal/memory/doc.go + store.go. Each sub-test gets a new Store via
// factory, so they are independent.
func RunStoreContract(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("AppendList_OrderAndIDs", func(t *testing.T) { testAppendListOrder(t, factory) })
	t.Run("AppendList_AssignsSeq", func(t *testing.T) { testAppendAssignsSeq(t, factory) })
	t.Run("ConvIsolation", func(t *testing.T) { testConvIsolation(t, factory) })
	t.Run("UnknownConvReturnsEmpty", func(t *testing.T) { testUnknownConv(t, factory) })
	t.Run("ListOpts_Limit", func(t *testing.T) { testListLimit(t, factory) })
	t.Run("ListOpts_AfterCursor", func(t *testing.T) { testListAfter(t, factory) })
	t.Run("ListOpts_BeforeCursor", func(t *testing.T) { testListBefore(t, factory) })
	t.Run("ListOpts_AfterAndBefore", func(t *testing.T) { testListAfterBefore(t, factory) })
	t.Run("Summarize_EmptyIsZero", func(t *testing.T) { testSummarizeEmpty(t, factory) })
	t.Run("Summarize_NonEmpty", func(t *testing.T) { testSummarizeNonEmpty(t, factory) })
	t.Run("AppendMessage_RejectsEmptyConvID", func(t *testing.T) { testAppendEmptyConv(t, factory) })
	t.Run("AppendMessage_PreservesBlocksMediaReactionsBookkeeping", func(t *testing.T) { testAppendRichMessage(t, factory) })
	t.Run("GetMessage_ScopedToConv", func(t *testing.T) { testGetMessageScoped(t, factory) })
	t.Run("GetMessage_UnknownReturnsNil", func(t *testing.T) { testGetMessageUnknown(t, factory) })
	t.Run("AddReaction_Idempotent", func(t *testing.T) { testAddReactionIdempotent(t, factory) })
	t.Run("AddReaction_UnknownMessage", func(t *testing.T) { testAddReactionUnknownMsg(t, factory) })
	t.Run("AppendSummary_ReplacesCoveredOnApply", func(t *testing.T) { testAppendSummaryApplies(t, factory) })
	t.Run("AppendSummary_EmptyIsNoop", func(t *testing.T) { testAppendSummaryEmpty(t, factory) })
	t.Run("LatestSummaryCovered", func(t *testing.T) { testLatestSummaryCovered(t, factory) })
	t.Run("ListMessages_ApplySummaryFalseShowsRaw", func(t *testing.T) { testListApplyFalse(t, factory) })
	t.Run("Convs_EnsureGetUpdate", func(t *testing.T) { testConvEnsureGetUpdate(t, factory) })
	t.Run("Convs_ListFilterByChannel", func(t *testing.T) { testConvListFilterChannel(t, factory) })
	t.Run("Convs_ListExcludesArchivedByDefault", func(t *testing.T) { testConvListExcludesArchived(t, factory) })
	t.Run("Convs_DeleteCascadesMessages", func(t *testing.T) { testConvDeleteCascades(t, factory) })
	t.Run("Convs_LatestByChannel", func(t *testing.T) { testLatestConvID(t, factory) })
	t.Run("IndexSearch_Roundtrip", func(t *testing.T) { testIndexSearch(t, factory) })
	t.Run("Search_ScopeIsolation", func(t *testing.T) { testSearchScopeIsolation(t, factory) })
	t.Run("Search_Reindex_Replaces", func(t *testing.T) { testReindexReplaces(t, factory) })
	t.Run("Search_NoHitsReturnsEmpty", func(t *testing.T) { testSearchNoHits(t, factory) })
	t.Run("Search_KZeroReturnsEmpty", func(t *testing.T) { testSearchKZero(t, factory) })
	t.Run("Search_KNegativeErrors", func(t *testing.T) { testSearchKNegative(t, factory) })
	t.Run("Index_RejectsEmptyID", func(t *testing.T) { testIndexEmptyID(t, factory) })
	t.Run("GetDocument_Roundtrip", func(t *testing.T) { testGetDocumentRoundtrip(t, factory) })
	t.Run("GetDocument_UnknownReturnsNil", func(t *testing.T) { testGetDocumentUnknown(t, factory) })
	t.Run("GetDocument_ScopeIsolation", func(t *testing.T) { testGetDocumentScopeIsolation(t, factory) })
	t.Run("GetDocument_RejectsEmpty", func(t *testing.T) { testGetDocumentRejectsEmpty(t, factory) })
	t.Run("DeleteDocument_RemovesAndSearchExcludes", func(t *testing.T) { testDeleteDocumentRemoves(t, factory) })
	t.Run("DeleteDocument_UnknownReturnsFalse", func(t *testing.T) { testDeleteDocumentUnknown(t, factory) })
	t.Run("DeleteDocument_ScopeIsolation", func(t *testing.T) { testDeleteDocumentScopeIsolation(t, factory) })
	t.Run("DeleteDocument_RejectsEmpty", func(t *testing.T) { testDeleteDocumentRejectsEmpty(t, factory) })
	t.Run("Prefs_Roundtrip", func(t *testing.T) { testPrefsRoundtrip(t, factory) })
	t.Run("Prefs_UnsetReturnsNil", func(t *testing.T) { testPrefsUnset(t, factory) })
	t.Run("Prefs_NilValueClears", func(t *testing.T) { testPrefsNilClears(t, factory) })
	t.Run("Prefs_RejectsEmptyKey", func(t *testing.T) { testPrefsEmptyKey(t, factory) })
	t.Run("ContextCancellation", func(t *testing.T) { testCtxCancel(t, factory) })
}

// --- conversations ----------------------------------------------------------

func testAppendListOrder(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	for _, role := range []string{"user", "assistant", "user"} {
		if _, err := s.AppendMessage(ctx, "c1", memory.Message{Role: role, Content: role}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	msgs, err := s.ListMessages(ctx, "c1", memory.ListOpts{})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		if m.ID == "" {
			t.Errorf("message %d has empty ID", i)
		}
		if m.CreatedAt == 0 {
			t.Errorf("message %d has zero CreatedAt", i)
		}
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[2].Role != "user" {
		t.Errorf("wrong order: %v / %v / %v", msgs[0].Role, msgs[1].Role, msgs[2].Role)
	}
	// IDs must be distinct.
	if msgs[0].ID == msgs[1].ID || msgs[1].ID == msgs[2].ID || msgs[0].ID == msgs[2].ID {
		t.Errorf("message IDs not distinct: %v / %v / %v", msgs[0].ID, msgs[1].ID, msgs[2].ID)
	}
}

func testConvIsolation(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_, _ = s.AppendMessage(ctx,"convA", memory.Message{Role: "user", Content: "A"})
	_, _ = s.AppendMessage(ctx,"convB", memory.Message{Role: "user", Content: "B"})
	a, _ := s.ListMessages(ctx, "convA", memory.ListOpts{})
	b, _ := s.ListMessages(ctx, "convB", memory.ListOpts{})
	if len(a) != 1 || a[0].Content != "A" {
		t.Errorf("convA leaked: %#v", a)
	}
	if len(b) != 1 || b[0].Content != "B" {
		t.Errorf("convB leaked: %#v", b)
	}
}

func testUnknownConv(t *testing.T, factory Factory) {
	s := factory()
	msgs, err := s.ListMessages(context.Background(), "nope", memory.ListOpts{})
	if err != nil {
		t.Fatalf("want nil error on unknown conv, got %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("want empty result, got %d messages", len(msgs))
	}
}

func testListLimit(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user", Content: "m"})
	}
	msgs, err := s.ListMessages(ctx, "c", memory.ListOpts{Limit: 2})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("want 2, got %d", len(msgs))
	}
}

func testListAfter(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user", Content: "m"})
	}
	all, _ := s.ListMessages(ctx, "c", memory.ListOpts{})
	mid := all[1].ID
	msgs, err := s.ListMessages(ctx, "c", memory.ListOpts{After: mid})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("want 2 after cursor, got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.ID == mid {
			t.Errorf("cursor msg %s returned (After is exclusive)", mid)
		}
	}
}

func testListBefore(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user", Content: "m"})
	}
	all, _ := s.ListMessages(ctx, "c", memory.ListOpts{})
	mid := all[2].ID
	msgs, err := s.ListMessages(ctx, "c", memory.ListOpts{Before: mid})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("want 2 before cursor, got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.ID == mid {
			t.Errorf("cursor msg %s returned (Before is exclusive)", mid)
		}
	}
}

func testListAfterBefore(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user", Content: "m"})
	}
	all, _ := s.ListMessages(ctx, "c", memory.ListOpts{})
	msgs, err := s.ListMessages(ctx, "c", memory.ListOpts{After: all[0].ID, Before: all[4].ID})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Errorf("want 3 in (after,before), got %d", len(msgs))
	}
}

func testSummarizeEmpty(t *testing.T, factory Factory) {
	s := factory()
	sum, err := s.Summarize(context.Background(), "empty")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if sum.Text != "" || sum.UpToMsgID != "" {
		t.Errorf("want zero Summary for empty conv, got %#v", sum)
	}
}

func testSummarizeNonEmpty(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user", Content: "hi"})
	_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "assistant", Content: "hello"})
	sum, err := s.Summarize(ctx, "c")
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if sum.ConvID != "c" {
		t.Errorf("Summary.ConvID = %q, want c", sum.ConvID)
	}
	if sum.Text == "" {
		t.Errorf("non-empty conv should produce non-empty Summary.Text")
	}
	if sum.UpToMsgID == "" {
		t.Errorf("Summary.UpToMsgID should reference the last message")
	}
}

func testAppendEmptyConv(t *testing.T, factory Factory) {
	s := factory()
	_, err := s.AppendMessage(context.Background(), "", memory.Message{Role: "user", Content: "x"})
	if err == nil {
		t.Errorf("want error on empty convID, got nil")
	}
}

// --- embeddings -------------------------------------------------------------

func testIndexSearch(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	docs := []memory.Document{
		{ID: "a", Text: "the quick brown fox"},
		{ID: "b", Text: "jumped over the lazy dog"},
		{ID: "c", Text: "fox in socks"},
	}
	for _, d := range docs {
		if err := s.Index(ctx, "s1", d); err != nil {
			t.Fatalf("Index(%s): %v", d.ID, err)
		}
	}
	hits, err := s.Search(ctx, "s1", "fox", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Errorf("want 2 fox hits, got %d", len(hits))
	}
	for _, h := range hits {
		if !strings.Contains(h.Document.Text, "fox") {
			t.Errorf("hit %q does not contain 'fox'", h.Document.Text)
		}
	}
}

func testSearchScopeIsolation(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.Index(ctx, "scopeA", memory.Document{ID: "1", Text: "apple"})
	_ = s.Index(ctx, "scopeB", memory.Document{ID: "1", Text: "apple"})
	hitsA, _ := s.Search(ctx, "scopeA", "apple", 10)
	hitsB, _ := s.Search(ctx, "scopeB", "apple", 10)
	hitsC, _ := s.Search(ctx, "scopeC", "apple", 10)
	if len(hitsA) != 1 || len(hitsB) != 1 {
		t.Errorf("want 1/1 in A/B, got %d/%d", len(hitsA), len(hitsB))
	}
	if len(hitsC) != 0 {
		t.Errorf("scope C should be empty, got %d", len(hitsC))
	}
}

func testReindexReplaces(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.Index(ctx, "s", memory.Document{ID: "k", Text: "original"})
	_ = s.Index(ctx, "s", memory.Document{ID: "k", Text: "updated"})
	hits, _ := s.Search(ctx, "s", "updated", 10)
	if len(hits) != 1 || hits[0].Document.Text != "updated" {
		t.Errorf("reindex failed: %#v", hits)
	}
	hitsOld, _ := s.Search(ctx, "s", "original", 10)
	if len(hitsOld) != 0 {
		t.Errorf("old text should be gone, got %d hits", len(hitsOld))
	}
}

func testSearchNoHits(t *testing.T, factory Factory) {
	s := factory()
	hits, err := s.Search(context.Background(), "empty-scope", "nope", 5)
	if err != nil {
		t.Fatalf("Search on empty scope: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("want empty hits, got %d", len(hits))
	}
}

func testSearchKZero(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.Index(ctx, "s", memory.Document{ID: "a", Text: "x"})
	hits, err := s.Search(ctx, "s", "x", 0)
	if err != nil {
		t.Fatalf("Search k=0: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("k=0 should return empty, got %d", len(hits))
	}
}

func testSearchKNegative(t *testing.T, factory Factory) {
	s := factory()
	_, err := s.Search(context.Background(), "s", "x", -1)
	if err == nil {
		t.Errorf("Search k=-1 should error")
	}
}

func testIndexEmptyID(t *testing.T, factory Factory) {
	s := factory()
	err := s.Index(context.Background(), "s", memory.Document{ID: "", Text: "x"})
	if err == nil {
		t.Errorf("Index with empty doc.ID should error")
	}
}

func testGetDocumentRoundtrip(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.Index(ctx, "s", memory.Document{
		ID:       "doc-1",
		Text:     "hello world",
		Metadata: map[string]string{"k": "v"},
	})
	got, err := s.GetDocument(ctx, "s", "doc-1")
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if got == nil {
		t.Fatal("GetDocument returned nil on existing doc")
	}
	if got.ID != "doc-1" || got.Text != "hello world" {
		t.Errorf("document mismatch: %+v", got)
	}
	if got.Metadata["k"] != "v" {
		t.Errorf("metadata lost on roundtrip: %+v", got.Metadata)
	}
}

func testGetDocumentUnknown(t *testing.T, factory Factory) {
	s := factory()
	got, err := s.GetDocument(context.Background(), "s", "does-not-exist")
	if err != nil {
		t.Fatalf("GetDocument unknown: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown doc, got %+v", got)
	}
}

func testGetDocumentScopeIsolation(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.Index(ctx, "A", memory.Document{ID: "shared", Text: "in A"})

	// Same docID under a different scope must be a miss, not a leak.
	got, err := s.GetDocument(ctx, "B", "shared")
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if got != nil {
		t.Errorf("scope B saw doc from scope A: %+v", got)
	}
}

func testGetDocumentRejectsEmpty(t *testing.T, factory Factory) {
	s := factory()
	if _, err := s.GetDocument(context.Background(), "", "id"); err == nil {
		t.Errorf("GetDocument with empty scope should error")
	}
	if _, err := s.GetDocument(context.Background(), "s", ""); err == nil {
		t.Errorf("GetDocument with empty docID should error")
	}
}

func testDeleteDocumentRemoves(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.Index(ctx, "s", memory.Document{ID: "gone", Text: "ephemeral fact"})
	_ = s.Index(ctx, "s", memory.Document{ID: "stay", Text: "persistent fact"})

	ok, err := s.DeleteDocument(ctx, "s", "gone")
	if err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	if !ok {
		t.Errorf("DeleteDocument returned false on existing doc")
	}

	// GetDocument must now miss.
	got, _ := s.GetDocument(ctx, "s", "gone")
	if got != nil {
		t.Errorf("GetDocument after delete returned %+v", got)
	}
	// Search must not surface the deleted doc — the FTS/vec triggers must
	// have cleaned up too, not just the base row.
	hits, _ := s.Search(ctx, "s", "ephemeral", 10)
	for _, h := range hits {
		if h.Document.ID == "gone" {
			t.Errorf("Search still returns deleted doc: %+v", h)
		}
	}
	// And the sibling doc must survive.
	if g, _ := s.GetDocument(ctx, "s", "stay"); g == nil {
		t.Errorf("Delete wiped an unrelated doc")
	}
}

func testDeleteDocumentUnknown(t *testing.T, factory Factory) {
	s := factory()
	ok, err := s.DeleteDocument(context.Background(), "s", "never-existed")
	if err != nil {
		t.Fatalf("DeleteDocument unknown: %v", err)
	}
	if ok {
		t.Errorf("DeleteDocument returned true on missing doc")
	}
}

func testDeleteDocumentScopeIsolation(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.Index(ctx, "A", memory.Document{ID: "shared", Text: "in A"})
	_ = s.Index(ctx, "B", memory.Document{ID: "shared", Text: "in B"})

	ok, err := s.DeleteDocument(ctx, "A", "shared")
	if err != nil || !ok {
		t.Fatalf("Delete(A/shared) ok=%v err=%v", ok, err)
	}
	// The B-scoped doc with the same docID must survive.
	if g, _ := s.GetDocument(ctx, "B", "shared"); g == nil {
		t.Errorf("DeleteDocument leaked across scopes: B/shared is gone")
	}
}

func testDeleteDocumentRejectsEmpty(t *testing.T, factory Factory) {
	s := factory()
	if _, err := s.DeleteDocument(context.Background(), "", "id"); err == nil {
		t.Errorf("DeleteDocument with empty scope should error")
	}
	if _, err := s.DeleteDocument(context.Background(), "s", ""); err == nil {
		t.Errorf("DeleteDocument with empty docID should error")
	}
}

// --- preferences -----------------------------------------------------------

func testPrefsRoundtrip(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	if err := s.SetPref(ctx, "theme", "dark"); err != nil {
		t.Fatalf("SetPref: %v", err)
	}
	v, err := s.GetPref(ctx, "theme")
	if err != nil {
		t.Fatalf("GetPref: %v", err)
	}
	if v != "dark" {
		t.Errorf("GetPref = %v, want dark", v)
	}
}

func testPrefsUnset(t *testing.T, factory Factory) {
	s := factory()
	v, err := s.GetPref(context.Background(), "missing")
	if err != nil {
		t.Fatalf("GetPref missing: %v", err)
	}
	if v != nil {
		t.Errorf("want nil for unset key, got %v", v)
	}
}

func testPrefsNilClears(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.SetPref(ctx, "k", "v")
	if err := s.SetPref(ctx, "k", nil); err != nil {
		t.Fatalf("SetPref(nil): %v", err)
	}
	v, _ := s.GetPref(ctx, "k")
	if v != nil {
		t.Errorf("SetPref(nil) should clear; got %v", v)
	}
}

func testPrefsEmptyKey(t *testing.T, factory Factory) {
	s := factory()
	if err := s.SetPref(context.Background(), "", "x"); err == nil {
		t.Errorf("SetPref(\"\") should error")
	}
	if _, err := s.GetPref(context.Background(), ""); err == nil {
		t.Errorf("GetPref(\"\") should error")
	}
}

// --- widened conv/message surface ------------------------------------------

func testAppendAssignsSeq(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user", Content: "m"})
	}
	msgs, _ := s.ListMessages(ctx, "c", memory.ListOpts{})
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		want := int64(i + 1)
		if m.Seq != want {
			t.Errorf("msg[%d].Seq = %d, want %d", i, m.Seq, want)
		}
	}
}

func testAppendRichMessage(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	in := memory.Message{
		Role:    "assistant",
		Channel: memory.ChannelCC,
		Content: "hello",
		Blocks: []memory.ContentBlock{
			{Type: memory.BlockText, Text: "hello"},
			{Type: memory.BlockToolUse, Name: "read_file", Input: `{"path":"x"}`, ToolID: "t1"},
			{Type: memory.BlockToolResult, ToolID: "t1", Output: "file contents"},
		},
		Media: []memory.Media{
			{UploadID: "up1", FileName: "cat.png", MimeType: "image/png", MediaType: "photo"},
		},
		Reactions:  []memory.Reaction{{Emoji: "👍", Source: "user"}},
		Model:      "claude-opus-4-7",
		Tier:       "hero",
		Backend:    "anthropic",
		CostUSD:    0.42,
		DurationMs: 1500,
		SessionID:  "sess-1",
		ReplyTo:    memory.MsgID("prev-id"),
	}
	if _, err := s.AppendMessage(ctx, "c", in); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	msgs, _ := s.ListMessages(ctx, "c", memory.ListOpts{})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(msgs))
	}
	got := msgs[0]
	if got.Role != "assistant" || got.Channel != memory.ChannelCC {
		t.Errorf("role/channel not preserved: %+v", got)
	}
	if len(got.Blocks) != 3 || got.Blocks[1].Name != "read_file" || got.Blocks[2].Output != "file contents" {
		t.Errorf("blocks not preserved: %+v", got.Blocks)
	}
	if len(got.Media) != 1 || got.Media[0].UploadID != "up1" {
		t.Errorf("media not preserved: %+v", got.Media)
	}
	if len(got.Reactions) != 1 || got.Reactions[0].Emoji != "👍" {
		t.Errorf("reactions not preserved: %+v", got.Reactions)
	}
	if got.Model != "claude-opus-4-7" || got.Tier != "hero" || got.Backend != "anthropic" ||
		got.CostUSD != 0.42 || got.DurationMs != 1500 || got.SessionID != "sess-1" ||
		got.ReplyTo != memory.MsgID("prev-id") {
		t.Errorf("bookkeeping fields not preserved: %+v", got)
	}
}

func testGetMessageScoped(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_, _ = s.AppendMessage(ctx,"a", memory.Message{Role: "user", Content: "from-a"})
	_, _ = s.AppendMessage(ctx,"b", memory.Message{Role: "user", Content: "from-b"})

	aMsgs, _ := s.ListMessages(ctx, "a", memory.ListOpts{})
	got, err := s.GetMessage(ctx, "a", aMsgs[0].ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got == nil || got.Content != "from-a" {
		t.Errorf("GetMessage returned wrong content: %+v", got)
	}
	// GetMessage with wrong convID must not cross-leak.
	leak, _ := s.GetMessage(ctx, "b", aMsgs[0].ID)
	if leak != nil {
		t.Errorf("GetMessage leaked across convs: %+v", leak)
	}
}

func testGetMessageUnknown(t *testing.T, factory Factory) {
	s := factory()
	got, err := s.GetMessage(context.Background(), "c", "nope")
	if err != nil {
		t.Fatalf("GetMessage unknown: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func testAddReactionIdempotent(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user"})
	msgs, _ := s.ListMessages(ctx, "c", memory.ListOpts{})
	id := msgs[0].ID

	ok, err := s.AddReaction(ctx, "c", id, memory.Reaction{Emoji: "👍", Source: "user"})
	if err != nil || !ok {
		t.Fatalf("AddReaction: ok=%v err=%v", ok, err)
	}
	// Same emoji+source is a no-op.
	_, _ = s.AddReaction(ctx, "c", id, memory.Reaction{Emoji: "👍", Source: "user"})
	// Same emoji, different source is a distinct reaction.
	_, _ = s.AddReaction(ctx, "c", id, memory.Reaction{Emoji: "👍", Source: "alf"})

	got, _ := s.GetMessage(ctx, "c", id)
	if got == nil {
		t.Fatal("message disappeared")
	}
	if len(got.Reactions) != 2 {
		t.Errorf("want 2 distinct reactions, got %d: %+v", len(got.Reactions), got.Reactions)
	}
}

func testAddReactionUnknownMsg(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.EnsureConv(ctx, "c", "", memory.ChannelCC)
	ok, err := s.AddReaction(ctx, "c", "nope", memory.Reaction{Emoji: "👍", Source: "user"})
	if err != nil {
		t.Fatalf("AddReaction unknown: %v", err)
	}
	if ok {
		t.Errorf("AddReaction on unknown msg should return false, got true")
	}
}

func testAppendSummaryApplies(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user", Content: "old"})
	}
	all, _ := s.ListMessages(ctx, "c", memory.ListOpts{})
	var covered []memory.MsgID
	for _, m := range all[:2] {
		covered = append(covered, m.ID)
	}
	if err := s.AppendSummary(ctx, "c", "the first two", covered); err != nil {
		t.Fatalf("AppendSummary: %v", err)
	}

	applied, err := s.ListMessages(ctx, "c", memory.ListOpts{ApplySummary: true})
	if err != nil {
		t.Fatalf("ListMessages applied: %v", err)
	}
	// Expect: [summary, 3rd original msg].
	if len(applied) != 2 {
		t.Fatalf("expected 2 visible msgs, got %d", len(applied))
	}
	if applied[0].Role != memory.RoleSummary {
		t.Errorf("first visible msg should be summary, got %q", applied[0].Role)
	}
	if applied[1].ID != all[2].ID {
		t.Errorf("second visible msg should be the uncovered original, got %+v", applied[1])
	}
}

func testAppendSummaryEmpty(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user"})
	msgs, _ := s.ListMessages(ctx, "c", memory.ListOpts{})
	before := len(msgs)
	// Empty text → no-op.
	_ = s.AppendSummary(ctx, "c", "", []memory.MsgID{msgs[0].ID})
	// Empty coveredIDs → no-op.
	_ = s.AppendSummary(ctx, "c", "would-summarize", nil)
	after, _ := s.ListMessages(ctx, "c", memory.ListOpts{})
	if len(after) != before {
		t.Errorf("AppendSummary empty should be no-op, msgs went %d → %d", before, len(after))
	}
}

func testLatestSummaryCovered(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user"})
	_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user"})
	msgs, _ := s.ListMessages(ctx, "c", memory.ListOpts{})
	firstTwo := []memory.MsgID{msgs[0].ID, msgs[1].ID}

	covered, _ := s.LatestSummaryCovered(ctx, "c")
	if len(covered) != 0 {
		t.Errorf("no summary yet, want empty; got %v", covered)
	}

	_ = s.AppendSummary(ctx, "c", "sum", firstTwo)
	covered, err := s.LatestSummaryCovered(ctx, "c")
	if err != nil {
		t.Fatalf("LatestSummaryCovered: %v", err)
	}
	if len(covered) != 2 {
		t.Errorf("want 2 covered, got %d", len(covered))
	}
}

func testListApplyFalse(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user"})
	_, _ = s.AppendMessage(ctx,"c", memory.Message{Role: "user"})
	msgs, _ := s.ListMessages(ctx, "c", memory.ListOpts{})
	_ = s.AppendSummary(ctx, "c", "sum", []memory.MsgID{msgs[0].ID})

	raw, _ := s.ListMessages(ctx, "c", memory.ListOpts{ApplySummary: false})
	// Raw timeline: 2 originals + summary = 3 entries.
	if len(raw) != 3 {
		t.Errorf("ApplySummary=false should return raw timeline of 3, got %d", len(raw))
	}
}

func testConvEnsureGetUpdate(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	if err := s.EnsureConv(ctx, "c", "hello", memory.ChannelCC); err != nil {
		t.Fatalf("EnsureConv: %v", err)
	}
	// Idempotent.
	if err := s.EnsureConv(ctx, "c", "different-title", memory.ChannelCC); err != nil {
		t.Fatalf("EnsureConv second: %v", err)
	}
	info, err := s.GetConv(ctx, "c")
	if err != nil {
		t.Fatalf("GetConv: %v", err)
	}
	if info.ID != "c" || info.Title != "hello" {
		t.Errorf("EnsureConv should not overwrite title on second call, got %+v", info)
	}
	if err := s.UpdateConvTitle(ctx, "c", "renamed"); err != nil {
		t.Fatalf("UpdateConvTitle: %v", err)
	}
	info, _ = s.GetConv(ctx, "c")
	if info.Title != "renamed" {
		t.Errorf("UpdateConvTitle did not persist, got %+v", info)
	}
}

func testConvListFilterChannel(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.EnsureConv(ctx, "cc-1", "", memory.ChannelCC)
	_ = s.EnsureConv(ctx, "cc-2", "", memory.ChannelCC)
	_ = s.EnsureConv(ctx, "tg-1", "", memory.ChannelTelegram)

	cc, _ := s.ListConvs(ctx, memory.ConvFilter{Channel: memory.ChannelCC})
	if len(cc) != 2 {
		t.Errorf("cc channel: want 2, got %d", len(cc))
	}
	tg, _ := s.ListConvs(ctx, memory.ConvFilter{Channel: memory.ChannelTelegram})
	if len(tg) != 1 {
		t.Errorf("tg channel: want 1, got %d", len(tg))
	}
	all, _ := s.ListConvs(ctx, memory.ConvFilter{})
	if len(all) != 3 {
		t.Errorf("no filter: want 3, got %d", len(all))
	}
}

func testConvListExcludesArchived(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.EnsureConv(ctx, "live", "", memory.ChannelCC)
	_ = s.EnsureConv(ctx, "old", "", memory.ChannelCC)
	_ = s.ArchiveConv(ctx, "old")

	live, _ := s.ListConvs(ctx, memory.ConvFilter{Channel: memory.ChannelCC})
	if len(live) != 1 || live[0].ID != "live" {
		t.Errorf("default should hide archived, got %+v", live)
	}
	all, _ := s.ListConvs(ctx, memory.ConvFilter{Channel: memory.ChannelCC, IncludeArchived: true})
	if len(all) != 2 {
		t.Errorf("IncludeArchived=true should return 2, got %d", len(all))
	}
}

func testConvDeleteCascades(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_, _ = s.AppendMessage(ctx,"a", memory.Message{Role: "user", Content: "kept-or-gone"})
	_, _ = s.AppendMessage(ctx,"b", memory.Message{Role: "user", Content: "kept"})
	if err := s.DeleteConv(ctx, "a"); err != nil {
		t.Fatalf("DeleteConv: %v", err)
	}
	aMsgs, _ := s.ListMessages(ctx, "a", memory.ListOpts{})
	if len(aMsgs) != 0 {
		t.Errorf("DeleteConv should cascade messages, got %d", len(aMsgs))
	}
	bMsgs, _ := s.ListMessages(ctx, "b", memory.ListOpts{})
	if len(bMsgs) != 1 {
		t.Errorf("unrelated conv b was affected: %d msgs", len(bMsgs))
	}
}

func testLatestConvID(t *testing.T, factory Factory) {
	s := factory()
	ctx := context.Background()
	_ = s.EnsureConv(ctx, "cc-old", "", memory.ChannelCC)
	_ = s.EnsureConv(ctx, "cc-new", "", memory.ChannelCC)
	_ = s.EnsureConv(ctx, "tg-1", "", memory.ChannelTelegram)
	// cc-old gets a message, then cc-new, then tg-1.
	_, _ = s.AppendMessage(ctx,"cc-old", memory.Message{Role: "user"})
	_, _ = s.AppendMessage(ctx,"tg-1", memory.Message{Role: "user"})
	_, _ = s.AppendMessage(ctx,"cc-new", memory.Message{Role: "user"})

	ccLatest, _ := s.LatestConvID(ctx, memory.ChannelCC)
	if ccLatest != "cc-new" {
		t.Errorf("cc latest: got %q, want cc-new", ccLatest)
	}
	tgLatest, _ := s.LatestConvID(ctx, memory.ChannelTelegram)
	if tgLatest != "tg-1" {
		t.Errorf("tg latest: got %q, want tg-1", tgLatest)
	}

	// Archived convs are skipped.
	_ = s.ArchiveConv(ctx, "cc-new")
	ccLatest, _ = s.LatestConvID(ctx, memory.ChannelCC)
	if ccLatest != "cc-old" {
		t.Errorf("after archiving cc-new, cc latest should be cc-old, got %q", ccLatest)
	}
}

// --- ctx cancellation ------------------------------------------------------

func testCtxCancel(t *testing.T, factory Factory) {
	s := factory()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.AppendMessage(ctx, "c", memory.Message{Role: "user", Content: "x"}); err == nil {
		t.Errorf("AppendMessage with canceled ctx should return ctx.Err()")
	}
	if _, err := s.ListMessages(ctx, "c", memory.ListOpts{}); err == nil {
		t.Errorf("ListMessages with canceled ctx should return ctx.Err()")
	}
	if _, err := s.Summarize(ctx, "c"); err == nil {
		t.Errorf("Summarize with canceled ctx should return ctx.Err()")
	}
	if err := s.Index(ctx, "s", memory.Document{ID: "a", Text: "x"}); err == nil {
		t.Errorf("Index with canceled ctx should return ctx.Err()")
	}
	if _, err := s.Search(ctx, "s", "x", 1); err == nil {
		t.Errorf("Search with canceled ctx should return ctx.Err()")
	}
	if _, err := s.GetPref(ctx, "k"); err == nil {
		t.Errorf("GetPref with canceled ctx should return ctx.Err()")
	}
	if err := s.SetPref(ctx, "k", "v"); err == nil {
		t.Errorf("SetPref with canceled ctx should return ctx.Err()")
	}
}
