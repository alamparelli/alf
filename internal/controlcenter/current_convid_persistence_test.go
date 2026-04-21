package controlcenter

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/platform/eventlog"
	"github.com/alamparelli/alf/internal/memory"
	chatsession "github.com/alamparelli/alf/internal/platform/session"
)

// TestCurrentConvID_ResumesAfterRestart is the regression guard for #318:
// after a daemon restart, CC's active conversation must be the last one the
// user was working on, not a freshly-minted internal ID.
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
		mem, err := memory.NewSQLiteStore(dataDir)
		if err != nil {
			t.Fatalf("memory: %v", err)
		}
		sessions := chatsession.New(dataDir, 30*time.Minute)
		evtLog := eventlog.New(dataDir)
		defer evtLog.Close()

		svc := NewChatService(
			dataDir, configDir, contextDir,
			NewFileTierStore(filepath.Join(configDir, "tiers.json")),
			sessions, evtLog, mem, nil,
			func(string, string, int) RouteResult { return RouteResult{Tier: "test"} },
			func(s string) string { return s },
			&mockProvider{},
		)

		// User's active CC conv and a message.
		userConvID := "tab-A"
		ctx := context.Background()
		_ = mem.EnsureConv(ctx, memory.ConvID(userConvID), "First tab", "cc")
		_, _ = mem.AppendMessage(ctx, memory.ConvID(userConvID), memory.Message{
			Role: "user", Channel: "cc", Content: "hello",
			Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: "hello"}},
		})
		svc.SetActiveConvID(userConvID)

		mem.Close()
	}

	// --- Boot #2: same dataDir, fresh ChatService — the "daemon restart". ---
	mem2, err := memory.NewSQLiteStore(dataDir)
	if err != nil {
		t.Fatalf("memory #2: %v", err)
	}
	defer mem2.Close()

	sessions2 := chatsession.New(dataDir, 30*time.Minute)
	evtLog2 := eventlog.New(dataDir)
	defer evtLog2.Close()

	svc2 := NewChatService(
		dataDir, configDir, contextDir,
		NewFileTierStore(filepath.Join(configDir, "tiers.json")),
		sessions2, evtLog2, mem2, nil,
		func(string, string, int) RouteResult { return RouteResult{Tier: "test"} },
		func(s string) string { return s },
		&mockProvider{},
	)

	got := svc2.CurrentConvID()
	if got != "tab-A" {
		t.Fatalf("CurrentConvID after restart = %q, want %q", got, "tab-A")
	}
}

// TestCurrentConvID_FallsBackToLatestConversationOnEmptyMeta guards the
// case where a fresh user has never triggered SetActiveConvID: the backend
// should return an ID that actually exists in the conversations list, not
// an internal ID the frontend has no visibility into (#318).
func TestCurrentConvID_FallsBackToLatestConversationOnEmptyMeta(t *testing.T) {
	dataDir := t.TempDir()
	configDir := t.TempDir()
	contextDir := filepath.Join(dataDir, "context")

	mem, err := memory.NewSQLiteStore(dataDir)
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	defer mem.Close()
	sessions := chatsession.New(dataDir, 30*time.Minute)
	evtLog := eventlog.New(dataDir)
	defer evtLog.Close()

	svc := NewChatService(
		dataDir, configDir, contextDir,
		NewFileTierStore(filepath.Join(configDir, "tiers.json")),
		sessions, evtLog, mem, nil,
		func(string, string, int) RouteResult { return RouteResult{Tier: "test"} },
		func(s string) string { return s },
		&mockProvider{},
	)

	// Pre-existing CC conversation but no active_conv_id pref.
	ctx := context.Background()
	_ = mem.EnsureConv(ctx, "tab-existing", "Existing", "cc")

	got := svc.CurrentConvID()
	if got != "tab-existing" {
		t.Fatalf("CurrentConvID = %q, want %q (should fall back to existing CC conv)", got, "tab-existing")
	}
}
