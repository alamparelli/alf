package controlcenter

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/chatdb"
	"github.com/alamparelli/alf/internal/conversation"
	"github.com/alamparelli/alf/internal/eventlog"
	chatsession "github.com/alamparelli/alf/internal/session"
)

// TestCurrentConvID_ResumesAfterRestart is the regression guard for #318:
// after a daemon restart, CC's active conversation must be the last one the
// user was working on, not a freshly-minted ConvStore ID.
//
// Simulates: boot, persist a message for conv "tab-A", close stores,
// re-open from the same dataDir (mimicking daemon restart), and verify
// CurrentConvID returns "tab-A".
func TestCurrentConvID_ResumesAfterRestart(t *testing.T) {
	dataDir := t.TempDir()
	configDir := t.TempDir()
	contextDir := filepath.Join(dataDir, "context")

	// --- Boot #1: simulate the daemon running and a user sending a message. ---
	{
		chatDB, err := chatdb.New(dataDir)
		if err != nil {
			t.Fatalf("chatdb: %v", err)
		}
		convStore := conversation.NewStore(dataDir)
		sessions := chatsession.New(dataDir, 30*time.Minute)
		evtLog := eventlog.New(dataDir)
		defer evtLog.Close()

		svc := NewChatService(
			dataDir, configDir, contextDir,
			NewFileTierStore(filepath.Join(configDir, "tiers.json")),
			sessions, evtLog, chatDB, nil,
			func(string, string, int) RouteResult { return RouteResult{Tier: "test"} },
			func(s string) string { return s },
			&mockProvider{},
		)
		svc.ConvStore = convStore

		// User's active CC conv and a message.
		userConvID := "tab-A"
		chatDB.EnsureConversation(userConvID, "First tab", "cc")
		chatDB.InsertMessage(chatdb.Message{
			ID: NewMessageID(), ConvID: userConvID, Role: "user",
			Text: "hello", Source: "cc", CreatedAt: time.Now(),
		})
		svc.SetActiveConvID(userConvID)

		// Also simulate a ConvStore write (the engine would do this).
		convStore.Append(conversation.Message{
			ID:        "m1",
			ConvID:    "convstore-internal-xyz",
			Channel:   conversation.ChannelCC,
			Role:      "user",
			Timestamp: time.Now(),
		})

		chatDB.Close()
	}

	// --- Boot #2: same dataDir, fresh ChatService — the "daemon restart". ---
	chatDB2, err := chatdb.New(dataDir)
	if err != nil {
		t.Fatalf("chatdb #2: %v", err)
	}
	defer chatDB2.Close()

	convStore2 := conversation.NewStore(dataDir)
	sessions2 := chatsession.New(dataDir, 30*time.Minute)
	evtLog2 := eventlog.New(dataDir)
	defer evtLog2.Close()

	svc2 := NewChatService(
		dataDir, configDir, contextDir,
		NewFileTierStore(filepath.Join(configDir, "tiers.json")),
		sessions2, evtLog2, chatDB2, nil,
		func(string, string, int) RouteResult { return RouteResult{Tier: "test"} },
		func(s string) string { return s },
		&mockProvider{},
	)
	svc2.ConvStore = convStore2

	got := svc2.CurrentConvID()
	if got != "tab-A" {
		t.Fatalf("CurrentConvID after restart = %q, want %q", got, "tab-A")
	}
}

// TestCurrentConvID_FallsBackToLatestConversationOnEmptyMeta guards the
// case where a fresh user has never triggered SetActiveConvID: the backend
// should return an ID that actually exists in the conversations list, not
// a ConvStore internal ID the frontend has no visibility into (#318).
func TestCurrentConvID_FallsBackToLatestConversationOnEmptyMeta(t *testing.T) {
	dataDir := t.TempDir()
	configDir := t.TempDir()
	contextDir := filepath.Join(dataDir, "context")

	chatDB, err := chatdb.New(dataDir)
	if err != nil {
		t.Fatalf("chatdb: %v", err)
	}
	defer chatDB.Close()
	convStore := conversation.NewStore(dataDir)
	sessions := chatsession.New(dataDir, 30*time.Minute)
	evtLog := eventlog.New(dataDir)
	defer evtLog.Close()

	svc := NewChatService(
		dataDir, configDir, contextDir,
		NewFileTierStore(filepath.Join(configDir, "tiers.json")),
		sessions, evtLog, chatDB, nil,
		func(string, string, int) RouteResult { return RouteResult{Tier: "test"} },
		func(s string) string { return s },
		&mockProvider{},
	)
	svc.ConvStore = convStore

	// Pre-existing CC conversation but no kv_meta("active_conv_id").
	chatDB.EnsureConversation("tab-existing", "Existing", "cc")

	got := svc.CurrentConvID()
	if got != "tab-existing" {
		t.Fatalf("CurrentConvID = %q, want %q (should fall back to existing CC conv, not ConvStore ID)", got, "tab-existing")
	}
}
