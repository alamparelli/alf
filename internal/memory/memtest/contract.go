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
	t.Run("ConvIsolation", func(t *testing.T) { testConvIsolation(t, factory) })
	t.Run("UnknownConvReturnsEmpty", func(t *testing.T) { testUnknownConv(t, factory) })
	t.Run("ListOpts_Limit", func(t *testing.T) { testListLimit(t, factory) })
	t.Run("ListOpts_AfterCursor", func(t *testing.T) { testListAfter(t, factory) })
	t.Run("ListOpts_BeforeCursor", func(t *testing.T) { testListBefore(t, factory) })
	t.Run("ListOpts_AfterAndBefore", func(t *testing.T) { testListAfterBefore(t, factory) })
	t.Run("Summarize_EmptyIsZero", func(t *testing.T) { testSummarizeEmpty(t, factory) })
	t.Run("Summarize_NonEmpty", func(t *testing.T) { testSummarizeNonEmpty(t, factory) })
	t.Run("AppendMessage_RejectsEmptyConvID", func(t *testing.T) { testAppendEmptyConv(t, factory) })
	t.Run("IndexSearch_Roundtrip", func(t *testing.T) { testIndexSearch(t, factory) })
	t.Run("Search_ScopeIsolation", func(t *testing.T) { testSearchScopeIsolation(t, factory) })
	t.Run("Search_Reindex_Replaces", func(t *testing.T) { testReindexReplaces(t, factory) })
	t.Run("Search_NoHitsReturnsEmpty", func(t *testing.T) { testSearchNoHits(t, factory) })
	t.Run("Search_KZeroReturnsEmpty", func(t *testing.T) { testSearchKZero(t, factory) })
	t.Run("Search_KNegativeErrors", func(t *testing.T) { testSearchKNegative(t, factory) })
	t.Run("Index_RejectsEmptyID", func(t *testing.T) { testIndexEmptyID(t, factory) })
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
		if err := s.AppendMessage(ctx, "c1", memory.Message{Role: role, Content: role}); err != nil {
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
	_ = s.AppendMessage(ctx, "convA", memory.Message{Role: "user", Content: "A"})
	_ = s.AppendMessage(ctx, "convB", memory.Message{Role: "user", Content: "B"})
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
		_ = s.AppendMessage(ctx, "c", memory.Message{Role: "user", Content: "m"})
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
		_ = s.AppendMessage(ctx, "c", memory.Message{Role: "user", Content: "m"})
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
		_ = s.AppendMessage(ctx, "c", memory.Message{Role: "user", Content: "m"})
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
		_ = s.AppendMessage(ctx, "c", memory.Message{Role: "user", Content: "m"})
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
	_ = s.AppendMessage(ctx, "c", memory.Message{Role: "user", Content: "hi"})
	_ = s.AppendMessage(ctx, "c", memory.Message{Role: "assistant", Content: "hello"})
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
	err := s.AppendMessage(context.Background(), "", memory.Message{Role: "user", Content: "x"})
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

// --- ctx cancellation ------------------------------------------------------

func testCtxCancel(t *testing.T, factory Factory) {
	s := factory()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := s.AppendMessage(ctx, "c", memory.Message{Role: "user", Content: "x"}); err == nil {
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
