package chatdb

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// Regression lock for critical-path scenarios #1 (multi-conv isolation)
// and #2 (convID scoping on every read/write) from TEST-BASELINE.md.
// The milestone 0.7.9 memory rework moves chatdb behind a ConvStore
// abstraction — these tests pin the existing semantics so the move
// cannot silently change them.

// ----- ConvID scoping on reads -----------------------------------------

func TestGet_ReturnsRequestedMessageOnly(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "A", "cc")
	db.EnsureConversation("b", "B", "cc")

	db.InsertMessage(Message{ID: "a1", ConvID: "a", Role: "user", Text: "from-a"})
	db.InsertMessage(Message{ID: "b1", ConvID: "b", Role: "user", Text: "from-b"})

	got, err := db.Get("a1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get a1 returned nil")
	}
	if got.ConvID != "a" || got.Text != "from-a" {
		t.Errorf("Get returned wrong message: %+v", got)
	}
}

func TestGet_MissingReturnsNilNil(t *testing.T) {
	db := newTestDB(t)
	got, err := db.Get("does-not-exist")
	if err != nil {
		t.Fatalf("Get on missing: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestHistory_ScopedToConvID(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "A", "cc")
	db.EnsureConversation("b", "B", "cc")

	for i := 0; i < 3; i++ {
		db.InsertMessage(Message{ID: fmt.Sprintf("a%d", i), ConvID: "a", Role: "user", Text: "a"})
	}
	for i := 0; i < 2; i++ {
		db.InsertMessage(Message{ID: fmt.Sprintf("b%d", i), ConvID: "b", Role: "user", Text: "b"})
	}

	msgsA, _ := db.History("a", 50, time.Time{})
	msgsB, _ := db.History("b", 50, time.Time{})

	for _, m := range msgsA {
		if m.ConvID != "a" {
			t.Errorf("History(a) leaked msg from conv %s: %+v", m.ConvID, m)
		}
	}
	for _, m := range msgsB {
		if m.ConvID != "b" {
			t.Errorf("History(b) leaked msg from conv %s: %+v", m.ConvID, m)
		}
	}
}

func TestHistory_EmptyConvIDReturnsAll(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "A", "cc")
	db.EnsureConversation("b", "B", "cc")

	db.InsertMessage(Message{ID: "a1", ConvID: "a", Role: "user", Text: "."})
	db.InsertMessage(Message{ID: "b1", ConvID: "b", Role: "user", Text: "."})

	all, err := db.History("", 50, time.Time{})
	if err != nil {
		t.Fatalf("History(empty): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("History(empty) should return both convs, got %d", len(all))
	}
}

// ----- AddReaction is message-scoped -----------------------------------

func TestAddReaction_ScopedToMessage(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "A", "cc")
	db.InsertMessage(Message{ID: "m1", ConvID: "a", Role: "user"})
	db.InsertMessage(Message{ID: "m2", ConvID: "a", Role: "user"})

	if err := db.AddReaction("m1", "👍", "user"); err != nil {
		t.Fatalf("AddReaction: %v", err)
	}

	got, _ := db.Get("m1")
	if len(got.Reactions) != 1 || got.Reactions[0].Emoji != "👍" {
		t.Errorf("m1 reactions: %+v", got.Reactions)
	}

	other, _ := db.Get("m2")
	if len(other.Reactions) != 0 {
		t.Errorf("m2 should have no reactions, got: %+v", other.Reactions)
	}
}

func TestAddReaction_Idempotent(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "A", "cc")
	db.InsertMessage(Message{ID: "m1", ConvID: "a", Role: "user"})

	db.AddReaction("m1", "👍", "user")
	db.AddReaction("m1", "👍", "user") // same emoji+source
	db.AddReaction("m1", "👍", "bot")  // different source → distinct

	got, _ := db.Get("m1")
	if len(got.Reactions) != 2 {
		t.Errorf("expected 2 distinct reactions, got %+v", got.Reactions)
	}
}

// ----- Conversations listing scoping ----------------------------------

func TestConversations_FilterBySource(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("cc-1", "", "cc")
	db.EnsureConversation("cc-2", "", "cc")
	db.EnsureConversation("tg-1", "", "telegram")

	ccOnly, _ := db.Conversations("cc", false)
	if len(ccOnly) != 2 {
		t.Errorf("cc filter: expected 2, got %d", len(ccOnly))
	}
	for _, c := range ccOnly {
		if c.Source != "cc" {
			t.Errorf("cc filter leaked source=%s", c.Source)
		}
	}

	tgOnly, _ := db.Conversations("telegram", false)
	if len(tgOnly) != 1 {
		t.Errorf("telegram filter: expected 1, got %d", len(tgOnly))
	}

	all, _ := db.Conversations("", false)
	if len(all) != 3 {
		t.Errorf("no filter: expected 3, got %d", len(all))
	}
}

func TestConversations_ExcludesArchivedByDefault(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("live", "", "cc")
	db.EnsureConversation("old", "", "cc")
	db.ArchiveConversation("old")

	live, _ := db.Conversations("cc", false)
	if len(live) != 1 || live[0].ID != "live" {
		t.Errorf("default should hide archived, got: %+v", live)
	}

	all, _ := db.Conversations("cc", true)
	if len(all) != 2 {
		t.Errorf("includeArchived=true should return 2, got %d", len(all))
	}
}

// ----- LatestConversationID source-scoped ------------------------------

func TestLatestConversationID_ScopedToSource(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("cc-old", "", "cc")
	db.EnsureConversation("cc-new", "", "cc")
	db.EnsureConversation("tg-1", "", "telegram")

	// Timestamps determine latest — insert in order.
	db.InsertMessage(Message{ID: "1", ConvID: "cc-old", Role: "user", CreatedAt: time.Now().Add(-time.Hour)})
	db.InsertMessage(Message{ID: "2", ConvID: "tg-1", Role: "user", CreatedAt: time.Now().Add(-30 * time.Minute)})
	db.InsertMessage(Message{ID: "3", ConvID: "cc-new", Role: "user", CreatedAt: time.Now()})

	if id := db.LatestConversationID("cc"); id != "cc-new" {
		t.Errorf("cc latest: got %q, want cc-new", id)
	}
	if id := db.LatestConversationID("telegram"); id != "tg-1" {
		t.Errorf("telegram latest: got %q, want tg-1", id)
	}
	if id := db.LatestConversationID("unknown"); id != "" {
		t.Errorf("unknown source: got %q, want empty", id)
	}
}

func TestLatestConversationID_SkipsArchived(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("live", "", "cc")
	db.EnsureConversation("archived", "", "cc")

	db.InsertMessage(Message{ID: "x", ConvID: "live", Role: "user", CreatedAt: time.Now().Add(-time.Hour)})
	db.InsertMessage(Message{ID: "y", ConvID: "archived", Role: "user", CreatedAt: time.Now()}) // newer but archived
	db.ArchiveConversation("archived")

	if id := db.LatestConversationID("cc"); id != "live" {
		t.Errorf("archived conv should be skipped, got %q", id)
	}
}

func TestLatestConversationID_EmptyWhenNoMessages(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("empty", "", "cc")
	if id := db.LatestConversationID("cc"); id != "" {
		t.Errorf("conv without messages shouldn't be latest, got %q", id)
	}
}

// ----- Conversation CRUD target-scoping -------------------------------

func TestUpdateConversation_AffectsOnlyTarget(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "before-a", "cc")
	db.EnsureConversation("b", "before-b", "cc")

	if err := db.UpdateConversation("a", "after-a"); err != nil {
		t.Fatalf("UpdateConversation: %v", err)
	}

	convs, _ := db.Conversations("cc", false)
	titles := map[string]string{}
	for _, c := range convs {
		titles[c.ID] = c.Title
	}
	if titles["a"] != "after-a" {
		t.Errorf("a title = %q, want after-a", titles["a"])
	}
	if titles["b"] != "before-b" {
		t.Errorf("b title was mutated: %q", titles["b"])
	}
}

func TestArchiveConversation_AffectsOnlyTarget(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "", "cc")
	db.EnsureConversation("b", "", "cc")

	if err := db.ArchiveConversation("a"); err != nil {
		t.Fatalf("ArchiveConversation: %v", err)
	}

	live, _ := db.Conversations("cc", false)
	if len(live) != 1 || live[0].ID != "b" {
		t.Errorf("only b should remain live, got %+v", live)
	}
}

func TestDeleteConversation_CascadesMessages(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "", "cc")
	db.EnsureConversation("b", "", "cc")
	db.InsertMessage(Message{ID: "a1", ConvID: "a", Role: "user", Text: "kept-or-gone"})
	db.InsertMessage(Message{ID: "b1", ConvID: "b", Role: "user", Text: "kept"})

	if err := db.DeleteConversation("a"); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}

	// a1 must be cascade-deleted via FK ON DELETE CASCADE.
	if got, _ := db.Get("a1"); got != nil {
		t.Errorf("a1 survived cascade delete: %+v", got)
	}
	// b1 must be untouched.
	if got, _ := db.Get("b1"); got == nil {
		t.Error("b1 disappeared when deleting conv a")
	}

	// Conv b itself still present.
	remaining, _ := db.Conversations("cc", false)
	if len(remaining) != 1 || remaining[0].ID != "b" {
		t.Errorf("conv listing after delete: %+v", remaining)
	}
}

// ----- Concurrent multi-conv writes -----------------------------------

// Concurrent writers across distinct conversations. Today chatdb
// returns "database is locked" under real concurrency (tracked in
// #346) because the Go sql pool does not re-apply the busy_timeout
// pragma to pool-borrowed connections. Callers must retry. This
// test pins the isolation property — not the concurrency limit.
// Once #346 lands, the retry loop becomes a no-op; the test still
// passes, so we do not need to edit it.
func TestConcurrentInserts_NoCrossConvBleed(t *testing.T) {
	db := newTestDB(t)
	const nConvs = 4
	const nMsgs = 25

	for i := 0; i < nConvs; i++ {
		db.EnsureConversation(fmt.Sprintf("c%d", i), "", "cc")
	}

	insertWithRetry := func(msg Message) error {
		for attempt := 0; attempt < 50; attempt++ {
			err := db.InsertMessage(msg)
			if err == nil {
				return nil
			}
			if !strings.Contains(err.Error(), "locked") {
				return err
			}
			time.Sleep(5 * time.Millisecond)
		}
		return fmt.Errorf("still locked after retries")
	}

	var wg sync.WaitGroup
	errs := make(chan error, nConvs*nMsgs)
	for c := 0; c < nConvs; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			convID := fmt.Sprintf("c%d", c)
			for i := 0; i < nMsgs; i++ {
				if err := insertWithRetry(Message{
					ID:     fmt.Sprintf("%s-%d", convID, i),
					ConvID: convID,
					Role:   "user",
					Text:   fmt.Sprintf("payload-%s-%d", convID, i),
				}); err != nil {
					errs <- fmt.Errorf("%s-%d: %w", convID, i, err)
				}
			}
		}(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent insert failed: %v", err)
	}

	for c := 0; c < nConvs; c++ {
		convID := fmt.Sprintf("c%d", c)
		msgs, err := db.History(convID, 1000, time.Time{})
		if err != nil {
			t.Fatalf("History(%s): %v", convID, err)
		}
		if len(msgs) != nMsgs {
			t.Errorf("conv %s: expected %d msgs, got %d", convID, nMsgs, len(msgs))
		}
		// Verify no foreign text leaked in.
		for _, m := range msgs {
			if m.ConvID != convID {
				t.Errorf("conv %s leaked a message from %s", convID, m.ConvID)
			}
		}
		// Seq numbers must be 1..nMsgs with no gaps or duplicates.
		seen := map[int64]bool{}
		for _, m := range msgs {
			if m.Seq < 1 || m.Seq > int64(nMsgs) {
				t.Errorf("conv %s: out-of-range seq %d", convID, m.Seq)
			}
			if seen[m.Seq] {
				t.Errorf("conv %s: duplicate seq %d", convID, m.Seq)
			}
			seen[m.Seq] = true
		}
	}
}

// ----- Media refs stay conv-scoped ------------------------------------

func TestMediaRef_ScopedToConvViaInsertMediaRef(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "", "cc")
	db.EnsureConversation("b", "", "cc")
	db.InsertMessage(Message{ID: "a1", ConvID: "a", Role: "user"})
	db.InsertMessage(Message{ID: "b1", ConvID: "b", Role: "user"})

	err := db.InsertMediaRef(MediaRef{
		UploadID: "upl-1",
		FileName: "cat.png",
		MimeType: "image/png",
	}, "a1", "a")
	if err != nil {
		t.Fatalf("InsertMediaRef: %v", err)
	}

	got, err := db.GetMediaByUploadID("upl-1")
	if err != nil {
		t.Fatalf("GetMediaByUploadID: %v", err)
	}
	if got == nil || got.FileName != "cat.png" {
		t.Errorf("unexpected media: %+v", got)
	}

	// Conv b message has no media attached.
	m, _ := db.Get("b1")
	if len(m.Media) != 0 {
		t.Errorf("conv b msg has leaked media: %+v", m.Media)
	}
}

func TestDeleteMedia_RemovesOnlyTarget(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "", "cc")
	db.InsertMessage(Message{ID: "a1", ConvID: "a", Role: "user"})

	db.InsertMediaRef(MediaRef{UploadID: "keep", FileName: "k.png"}, "a1", "a")
	db.InsertMediaRef(MediaRef{UploadID: "gone", FileName: "g.png"}, "a1", "a")

	if err := db.DeleteMedia("gone"); err != nil {
		t.Fatalf("DeleteMedia: %v", err)
	}

	if m, _ := db.GetMediaByUploadID("gone"); m != nil {
		t.Errorf("gone still present: %+v", m)
	}
	if m, _ := db.GetMediaByUploadID("keep"); m == nil {
		t.Error("keep disappeared after unrelated delete")
	}
}
