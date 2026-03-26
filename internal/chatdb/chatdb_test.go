package chatdb

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("newTestDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInsertAndGet(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("conv1", "Test Conv", "cc")

	now := time.Now().Truncate(time.Second)
	msg := Message{
		ID: "msg1", ConvID: "conv1", Role: "user", Text: "hello",
		Source: "cc", Model: "sonnet", Tier: "sonnet", CostUSD: 0.01,
		DurationMs: 500, SessionID: "sess1", ReplyTo: "msg0", CreatedAt: now,
		Blocks: []ContentBlock{
			{BlockIndex: 0, BlockType: "thinking", Text: "let me think"},
			{BlockIndex: 1, BlockType: "text", Text: "hello back"},
		},
		Media: []MediaRef{
			{UploadID: "up1", FileName: "photo.jpg", MimeType: "image/jpeg", MediaType: "photo"},
		},
	}
	if err := db.InsertMessage(msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	got, err := db.Get("msg1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Text != "hello" {
		t.Errorf("text = %q, want %q", got.Text, "hello")
	}
	if got.Model != "sonnet" {
		t.Errorf("model = %q, want %q", got.Model, "sonnet")
	}
	if got.CostUSD != 0.01 {
		t.Errorf("cost = %f, want 0.01", got.CostUSD)
	}
	if got.DurationMs != 500 {
		t.Errorf("duration = %d, want 500", got.DurationMs)
	}
	if got.SessionID != "sess1" {
		t.Errorf("session = %q, want %q", got.SessionID, "sess1")
	}
	if got.ReplyTo != "msg0" {
		t.Errorf("reply_to = %q, want %q", got.ReplyTo, "msg0")
	}
	if len(got.Blocks) != 2 {
		t.Fatalf("blocks len = %d, want 2", len(got.Blocks))
	}
	if got.Blocks[0].BlockType != "thinking" || got.Blocks[0].Text != "let me think" {
		t.Errorf("block[0] = %+v", got.Blocks[0])
	}
	if got.Blocks[1].BlockType != "text" || got.Blocks[1].Text != "hello back" {
		t.Errorf("block[1] = %+v", got.Blocks[1])
	}
	if len(got.Media) != 1 || got.Media[0].UploadID != "up1" {
		t.Errorf("media = %+v", got.Media)
	}
}

func TestContentBlocksPersistence(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")

	blocks := []ContentBlock{
		{BlockIndex: 0, BlockType: "thinking", Text: "deep thought"},
		{BlockIndex: 1, BlockType: "tool_use", Name: "bash", Input: `{"cmd":"ls"}`, ToolID: "tool_1"},
		{BlockIndex: 2, BlockType: "tool_result", ToolID: "tool_1", Output: "file1\nfile2"},
		{BlockIndex: 3, BlockType: "text", Text: "here are your files"},
	}
	db.InsertMessage(Message{ID: "m1", ConvID: "c1", Role: "assistant", Text: "here are your files", Blocks: blocks})

	got, _ := db.Get("m1")
	if len(got.Blocks) != 4 {
		t.Fatalf("blocks = %d, want 4", len(got.Blocks))
	}
	if got.Blocks[1].Name != "bash" || got.Blocks[1].Input != `{"cmd":"ls"}` {
		t.Errorf("tool_use block = %+v", got.Blocks[1])
	}
	if got.Blocks[2].Output != "file1\nfile2" {
		t.Errorf("tool_result block = %+v", got.Blocks[2])
	}
}

func TestReactionsPersistence(t *testing.T) {
	dir := t.TempDir()
	db1, _ := New(dir)

	db1.EnsureConversation("c1", "", "cc")
	db1.InsertMessage(Message{ID: "m1", ConvID: "c1", Role: "assistant", Text: "hi"})
	db1.AddReaction("m1", "👍", "user")
	db1.AddReaction("m1", "🎉", "alf")
	db1.Close()

	// Reopen — reactions must survive.
	db2, _ := New(dir)
	defer db2.Close()

	got, _ := db2.Get("m1")
	if len(got.Reactions) != 2 {
		t.Fatalf("reactions = %d, want 2", len(got.Reactions))
	}
	emojis := map[string]string{}
	for _, r := range got.Reactions {
		emojis[r.Source] = r.Emoji
	}
	if emojis["user"] != "👍" || emojis["alf"] != "🎉" {
		t.Errorf("reactions = %+v", got.Reactions)
	}
}

func TestReaction_Idempotent(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")
	db.InsertMessage(Message{ID: "m1", ConvID: "c1", Role: "user", Text: "hi"})

	db.AddReaction("m1", "👍", "user")
	db.AddReaction("m1", "👍", "user") // duplicate — should not error

	got, _ := db.Get("m1")
	if len(got.Reactions) != 1 {
		t.Errorf("reactions = %d, want 1 (idempotent)", len(got.Reactions))
	}
}

func TestHistory_Pagination(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 150; i++ {
		db.InsertMessage(Message{
			ID: fmt.Sprintf("m%d", i), ConvID: "c1", Role: "user",
			Text: fmt.Sprintf("msg %d", i), CreatedAt: base.Add(time.Duration(i) * time.Second),
		})
	}

	// First page.
	page1, _ := db.History("c1", 50, time.Time{})
	if len(page1) != 50 {
		t.Fatalf("page1 = %d, want 50", len(page1))
	}
	// Should be the LAST 50 (chronological).
	if page1[0].Text != "msg 100" {
		t.Errorf("page1[0] = %q, want 'msg 100'", page1[0].Text)
	}

	// Second page — before the oldest of page1.
	page2, _ := db.History("c1", 50, page1[0].CreatedAt)
	if len(page2) != 50 {
		t.Fatalf("page2 = %d, want 50", len(page2))
	}
	if page2[0].Text != "msg 50" {
		t.Errorf("page2[0] = %q, want 'msg 50'", page2[0].Text)
	}

	// Third page.
	page3, _ := db.History("c1", 50, page2[0].CreatedAt)
	if len(page3) != 50 {
		t.Fatalf("page3 = %d, want 50", len(page3))
	}
	if page3[0].Text != "msg 0" {
		t.Errorf("page3[0] = %q, want 'msg 0'", page3[0].Text)
	}
}

func TestHistory_ConvFilter(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("a", "", "cc")
	db.EnsureConversation("b", "", "cc")

	db.InsertMessage(Message{ID: "m1", ConvID: "a", Role: "user", Text: "in a"})
	db.InsertMessage(Message{ID: "m2", ConvID: "b", Role: "user", Text: "in b"})
	db.InsertMessage(Message{ID: "m3", ConvID: "a", Role: "assistant", Text: "reply a"})

	msgs, _ := db.History("a", 100, time.Time{})
	if len(msgs) != 2 {
		t.Fatalf("history(a) = %d, want 2", len(msgs))
	}
	for _, m := range msgs {
		if m.ConvID != "a" {
			t.Errorf("got conv_id = %q in filtered history", m.ConvID)
		}
	}
}

func TestHistory_SourceFilter(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("cc1", "", "cc")
	db.EnsureConversation("tg1", "", "telegram")

	db.InsertMessage(Message{ID: "m1", ConvID: "cc1", Role: "user", Text: "cc msg", Source: "cc"})
	db.InsertMessage(Message{ID: "m2", ConvID: "tg1", Role: "user", Text: "tg msg", Source: "telegram"})

	// History filters by conv_id, not source — both should be accessible.
	cc, _ := db.History("cc1", 100, time.Time{})
	if len(cc) != 1 || cc[0].Source != "cc" {
		t.Errorf("cc history = %+v", cc)
	}
	tg, _ := db.History("tg1", 100, time.Time{})
	if len(tg) != 1 || tg[0].Source != "telegram" {
		t.Errorf("tg history = %+v", tg)
	}
}

func TestConversations_List(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "Chat One", "cc")
	db.EnsureConversation("c2", "Chat Two", "cc")

	db.InsertMessage(Message{ID: "m1", ConvID: "c1", Role: "user", Text: "hello"})
	db.InsertMessage(Message{ID: "m2", ConvID: "c1", Role: "assistant", Text: "hi"})
	db.InsertMessage(Message{ID: "m3", ConvID: "c2", Role: "user", Text: "bye"})

	convs, _ := db.Conversations("", false)
	if len(convs) != 2 {
		t.Fatalf("conversations = %d, want 2", len(convs))
	}
	// Find c1.
	var c1 *ConversationInfo
	for i := range convs {
		if convs[i].ID == "c1" {
			c1 = &convs[i]
		}
	}
	if c1 == nil {
		t.Fatal("c1 not found")
	}
	if c1.Title != "Chat One" {
		t.Errorf("title = %q", c1.Title)
	}
	if c1.MsgCount != 2 {
		t.Errorf("msg_count = %d, want 2", c1.MsgCount)
	}
}

func TestConversations_SourceFilter(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("cc1", "", "cc")
	db.EnsureConversation("tg1", "", "telegram")

	convs, _ := db.Conversations("cc", false)
	if len(convs) != 1 || convs[0].ID != "cc1" {
		t.Errorf("cc convs = %+v", convs)
	}
	all, _ := db.Conversations("", false)
	if len(all) != 2 {
		t.Errorf("all convs = %d, want 2", len(all))
	}
}

func TestEnsureConversation_Idempotent(t *testing.T) {
	db := newTestDB(t)
	if err := db.EnsureConversation("c1", "First", "cc"); err != nil {
		t.Fatal(err)
	}
	// Second call with different title — should not error, title stays "First".
	if err := db.EnsureConversation("c1", "Second", "cc"); err != nil {
		t.Fatal(err)
	}
	convs, _ := db.Conversations("", false)
	if len(convs) != 1 {
		t.Fatalf("convs = %d", len(convs))
	}
	if convs[0].Title != "First" {
		t.Errorf("title = %q, want 'First' (INSERT OR IGNORE preserves original)", convs[0].Title)
	}
}

func TestArchiveConversation(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")
	db.EnsureConversation("c2", "", "cc")
	db.ArchiveConversation("c1")

	active, _ := db.Conversations("", false)
	if len(active) != 1 || active[0].ID != "c2" {
		t.Errorf("active = %+v, want only c2", active)
	}

	all, _ := db.Conversations("", true)
	if len(all) != 2 {
		t.Errorf("all = %d, want 2", len(all))
	}
}

func TestDeleteConversation_Cascade(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")
	db.InsertMessage(Message{
		ID: "m1", ConvID: "c1", Role: "user", Text: "hi",
		Blocks: []ContentBlock{{BlockIndex: 0, BlockType: "text", Text: "hi"}},
		Media:  []MediaRef{{UploadID: "up1", FileName: "f.jpg", MimeType: "image/jpeg", MediaType: "photo"}},
	})
	db.AddReaction("m1", "👍", "user")

	db.DeleteConversation("c1")

	// Everything should be gone.
	if msg, _ := db.Get("m1"); msg != nil {
		t.Error("message still exists after cascade delete")
	}
	convs, _ := db.Conversations("", true)
	if len(convs) != 0 {
		t.Errorf("conversations still exist: %+v", convs)
	}
}

func TestUpdateConversation(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "Old", "cc")
	db.UpdateConversation("c1", "New Title")

	convs, _ := db.Conversations("", false)
	if convs[0].Title != "New Title" {
		t.Errorf("title = %q", convs[0].Title)
	}
}

func TestSessionStats(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")

	db.InsertMessage(Message{ID: "m1", ConvID: "c1", Role: "user", SessionID: "sess-a", CostUSD: 0.01})
	db.InsertMessage(Message{ID: "m2", ConvID: "c1", Role: "assistant", SessionID: "sess-a", CostUSD: 0.05})
	db.InsertMessage(Message{ID: "m3", ConvID: "c1", Role: "assistant", SessionID: "scheduled:daily", CostUSD: 0.02})

	sid, count, cost, err := db.SessionStats("scheduled:")
	if err != nil {
		t.Fatal(err)
	}
	if sid != "sess-a" {
		t.Errorf("session = %q", sid)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
	if cost < 0.059 || cost > 0.061 {
		t.Errorf("cost = %f, want ~0.06", cost)
	}
}

func TestMigrateFromJSONL(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := filepath.Join(dir, "logs", "chat_messages.jsonl")
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)

	// Write sample JSONL.
	lines := []string{
		`{"id":"m1","role":"user","text":"hello","ts":"2026-01-01T00:00:00Z","conv_id":"c1"}`,
		`{"id":"m2","role":"assistant","text":"hi","ts":"2026-01-01T00:01:00Z","model":"sonnet","tier":"sonnet","cost_usd":0.01,"conv_id":"c1","reactions":[{"emoji":"👍","from":"user"}]}`,
		`{"id":"m3","role":"user","text":"no conv","ts":"2026-01-01T00:02:00Z"}`,
	}
	os.WriteFile(jsonlPath, []byte(fmt.Sprintf("%s\n%s\n%s\n", lines[0], lines[1], lines[2])), 0o644)

	db, _ := New(dir)
	defer db.Close()

	if err := db.MigrateFromJSONL(jsonlPath); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// JSONL file should be renamed.
	if _, err := os.Stat(jsonlPath); !os.IsNotExist(err) {
		t.Error("JSONL file not renamed after migration")
	}
	if _, err := os.Stat(jsonlPath + ".migrated"); err != nil {
		t.Error("migrated file not found")
	}

	// Check data.
	msgs, _ := db.History("c1", 100, time.Time{})
	if len(msgs) != 2 {
		t.Fatalf("c1 msgs = %d, want 2", len(msgs))
	}

	// m3 with empty conv_id should be in _default.
	msgs2, _ := db.History("_default", 100, time.Time{})
	if len(msgs2) != 1 {
		t.Fatalf("_default msgs = %d, want 1", len(msgs2))
	}

	// Reactions should be migrated.
	m2, _ := db.Get("m2")
	if len(m2.Reactions) != 1 || m2.Reactions[0].Emoji != "👍" {
		t.Errorf("reactions = %+v", m2.Reactions)
	}
}

func TestPersistence_Reopen(t *testing.T) {
	dir := t.TempDir()
	db1, _ := New(dir)
	db1.EnsureConversation("c1", "Test", "cc")
	db1.InsertMessage(Message{ID: "m1", ConvID: "c1", Role: "user", Text: "persisted"})
	db1.Close()

	db2, _ := New(dir)
	defer db2.Close()

	got, _ := db2.Get("m1")
	if got == nil || got.Text != "persisted" {
		t.Errorf("after reopen: %+v", got)
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")

	var wg sync.WaitGroup

	// Writer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			db.InsertMessage(Message{
				ID: fmt.Sprintf("w%d", i), ConvID: "c1", Role: "user",
				Text: fmt.Sprintf("msg %d", i),
			})
		}
	}()

	// Reader.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			db.History("c1", 10, time.Time{})
		}
	}()

	wg.Wait()

	// All writes should be visible.
	msgs, _ := db.History("c1", 100, time.Time{})
	if len(msgs) != 50 {
		t.Errorf("after concurrent: %d msgs, want 50", len(msgs))
	}
}

func TestGet_NotFound(t *testing.T) {
	db := newTestDB(t)
	got, err := db.Get("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestInsertMediaRef_Basic(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")
	db.InsertMessage(Message{ID: "m1", ConvID: "c1", Role: "user", Text: "check this"})

	ref := MediaRef{
		UploadID:  "up-100",
		FileName:  "report.pdf",
		MimeType:  "application/pdf",
		MediaType: "document",
		FilePath:  "/tmp/report.pdf",
		URL:       "https://example.com/report.pdf",
	}
	if err := db.InsertMediaRef(ref, "m1", "c1"); err != nil {
		t.Fatalf("InsertMediaRef: %v", err)
	}

	got, err := db.Get("m1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Media) != 1 {
		t.Fatalf("media len = %d, want 1", len(got.Media))
	}
	if got.Media[0].UploadID != "up-100" {
		t.Errorf("upload_id = %q, want %q", got.Media[0].UploadID, "up-100")
	}
	if got.Media[0].FileName != "report.pdf" {
		t.Errorf("file_name = %q, want %q", got.Media[0].FileName, "report.pdf")
	}
}

func TestGetMediaByUploadID_Found(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")
	db.InsertMessage(Message{ID: "m1", ConvID: "c1", Role: "user", Text: "pic"})

	ref := MediaRef{
		UploadID:  "up-200",
		FileName:  "photo.jpg",
		MimeType:  "image/jpeg",
		MediaType: "photo",
		FilePath:  "/data/uploads/photo.jpg",
		URL:       "/api/chat/media/up-200",
	}
	if err := db.InsertMediaRef(ref, "m1", "c1"); err != nil {
		t.Fatalf("InsertMediaRef: %v", err)
	}

	got, err := db.GetMediaByUploadID("up-200")
	if err != nil {
		t.Fatalf("GetMediaByUploadID: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil MediaRef")
	}
	if got.UploadID != "up-200" {
		t.Errorf("upload_id = %q", got.UploadID)
	}
	if got.FileName != "photo.jpg" {
		t.Errorf("file_name = %q", got.FileName)
	}
	if got.MimeType != "image/jpeg" {
		t.Errorf("mime_type = %q", got.MimeType)
	}
	if got.MediaType != "photo" {
		t.Errorf("media_type = %q", got.MediaType)
	}
	if got.FilePath != "/data/uploads/photo.jpg" {
		t.Errorf("file_path = %q", got.FilePath)
	}
	if got.URL != "/api/chat/media/up-200" {
		t.Errorf("url = %q", got.URL)
	}
}

func TestGetMediaByUploadID_NotFound(t *testing.T) {
	db := newTestDB(t)

	got, err := db.GetMediaByUploadID("nonexistent-id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestInsertMediaRef_Replace(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")
	db.InsertMessage(Message{ID: "m1", ConvID: "c1", Role: "user", Text: "file"})

	ref1 := MediaRef{
		UploadID:  "up-300",
		FileName:  "draft.pdf",
		MimeType:  "application/pdf",
		MediaType: "document",
		FilePath:  "/tmp/draft.pdf",
	}
	if err := db.InsertMediaRef(ref1, "m1", "c1"); err != nil {
		t.Fatalf("InsertMediaRef (first): %v", err)
	}

	// Same upload_id, different file_name — second write should win.
	ref2 := MediaRef{
		UploadID:  "up-300",
		FileName:  "final.pdf",
		MimeType:  "application/pdf",
		MediaType: "document",
		FilePath:  "/tmp/final.pdf",
	}
	if err := db.InsertMediaRef(ref2, "m1", "c1"); err != nil {
		t.Fatalf("InsertMediaRef (replace): %v", err)
	}

	got, err := db.GetMediaByUploadID("up-300")
	if err != nil {
		t.Fatalf("GetMediaByUploadID: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.FileName != "final.pdf" {
		t.Errorf("file_name = %q, want %q (second write should win)", got.FileName, "final.pdf")
	}
	if got.FilePath != "/tmp/final.pdf" {
		t.Errorf("file_path = %q, want %q", got.FilePath, "/tmp/final.pdf")
	}
}

func TestInsertMediaRef_CascadeDelete(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")
	db.InsertMessage(Message{
		ID: "m1", ConvID: "c1", Role: "user", Text: "with media",
	})
	db.InsertMediaRef(MediaRef{
		UploadID: "up-400", FileName: "gone.jpg", MimeType: "image/jpeg", MediaType: "photo",
	}, "m1", "c1")

	// Verify it exists first.
	got, _ := db.GetMediaByUploadID("up-400")
	if got == nil {
		t.Fatal("media ref should exist before delete")
	}

	// Delete conversation — should cascade to messages and media.
	db.DeleteConversation("c1")

	got, err := db.GetMediaByUploadID("up-400")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("media ref should be gone after cascade delete, got %+v", got)
	}
}

func TestExpiredMediaForConversation(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")
	db.InsertMessage(Message{ID: "m1", ConvID: "c1", Role: "user", Text: "old media"})
	db.InsertMessage(Message{ID: "m2", ConvID: "c1", Role: "user", Text: "new media"})

	// Insert old media (manually set created_at in the past).
	db.db.Exec(`INSERT INTO media (upload_id, message_id, conv_id, file_name, mime_type, media_type, file_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"old-1", "m1", "c1", "old.jpg", "image/jpeg", "photo", "/tmp/old.jpg",
		time.Now().AddDate(0, 0, -10))

	// Insert recent media.
	db.InsertMediaRef(MediaRef{
		UploadID: "new-1", FileName: "new.jpg", MimeType: "image/jpeg", MediaType: "photo", FilePath: "/tmp/new.jpg",
	}, "m2", "c1")

	cutoff := time.Now().AddDate(0, 0, -7)
	expired := db.ExpiredMediaForConversation("c1", cutoff)
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired media, got %d", len(expired))
	}
	if expired[0].UploadID != "old-1" {
		t.Errorf("expected old-1, got %s", expired[0].UploadID)
	}
}

func TestDeleteMedia(t *testing.T) {
	db := newTestDB(t)
	db.EnsureConversation("c1", "", "cc")
	db.InsertMessage(Message{ID: "m1", ConvID: "c1", Role: "user", Text: "test"})
	db.InsertMediaRef(MediaRef{
		UploadID: "del-1", FileName: "bye.jpg", MimeType: "image/jpeg", MediaType: "photo",
	}, "m1", "c1")

	// Verify exists.
	got, _ := db.GetMediaByUploadID("del-1")
	if got == nil {
		t.Fatal("media should exist before delete")
	}

	if err := db.DeleteMedia("del-1"); err != nil {
		t.Fatalf("DeleteMedia: %v", err)
	}

	got, _ = db.GetMediaByUploadID("del-1")
	if got != nil {
		t.Error("media should be gone after DeleteMedia")
	}
}

func TestEmptyState(t *testing.T) {
	db := newTestDB(t)

	msgs, err := db.History("", 50, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Errorf("empty history = %d", len(msgs))
	}

	convs, err := db.Conversations("", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 0 {
		t.Errorf("empty convs = %d", len(convs))
	}
}
