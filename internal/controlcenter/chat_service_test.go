package controlcenter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/eventlog"
	"github.com/alamparelli/alf/internal/provider"
	chatsession "github.com/alamparelli/alf/internal/session"
)

// mockProvider is a no-op provider for tests that don't invoke Claude.
type mockProvider struct{}

func (m *mockProvider) Invoke(_ context.Context, _ string, _ provider.Params, _ provider.OnProgress) (*provider.Result, error) {
	return &provider.Result{Text: "mock response", Model: "mock"}, nil
}

// newTestChatService creates a ChatService with real stores backed by temp dirs.
// No Claude CLI, router, or transcriber - tests that need those should mock them.
func newTestChatService(t *testing.T) *ChatService {
	t.Helper()
	dataDir := t.TempDir()
	configDir := t.TempDir()
	contextDir := filepath.Join(dataDir, "context")
	os.MkdirAll(contextDir, 0o755)
	os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755)
	os.MkdirAll(filepath.Join(dataDir, "logs", "events"), 0o755)

	tierStore := NewFileTierStore(filepath.Join(configDir, "tiers.json"))
	sessions := chatsession.New(dataDir, 30*time.Minute)
	eventLog := eventlog.New(dataDir)
	t.Cleanup(func() { eventLog.Close() })

	chatStore := NewChatStore(dataDir)

	return NewChatService(
		dataDir, configDir, contextDir,
		tierStore, sessions, eventLog, chatStore,
		nil, // no transcriber
		func(msg, lastTier string, msgCount int) RouteResult {
			// Default test router: always route to "test_tier".
			return RouteResult{Tier: "test_tier", Reason: "test"}
		},
		func(short string) string {
			// Simple model resolver for tests.
			switch short {
			case "haiku":
				return "claude-haiku-4-5"
			case "sonnet":
				return "claude-sonnet-4-6"
			case "opus":
				return "claude-opus-4-6"
			default:
				return short
			}
		},
		&mockProvider{},
	)
}

func TestChatService_UploadAndGetUpload(t *testing.T) {
	svc := newTestChatService(t)

	result, err := svc.Upload(
		strings.NewReader("test content"),
		"test.txt",
		"document",
	)
	if err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	if result.UploadID == "" {
		t.Fatal("expected non-empty upload_id")
	}
	if result.FileName != "test.txt" {
		t.Errorf("expected file_name test.txt, got %q", result.FileName)
	}

	// Verify GetUpload.
	entry := svc.GetUpload(result.UploadID)
	if entry == nil {
		t.Fatal("GetUpload returned nil")
	}
	if entry.TempPath == "" {
		t.Error("expected non-empty temp path")
	}

	// Verify file exists.
	data, err := os.ReadFile(entry.TempPath)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	if string(data) != "test content" {
		t.Errorf("expected %q, got %q", "test content", string(data))
	}

	os.Remove(entry.TempPath)
}

func TestChatService_UploadSanitizesFilename(t *testing.T) {
	svc := newTestChatService(t)

	for _, tc := range []struct {
		name     string
		input    string
		contains string // should NOT contain
	}{
		{"path traversal", "../../etc/passwd", "/"},
		{"newline", "file\nname.txt", "\n"},
		{"quotes", `file"name.txt`, `"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := svc.Upload(strings.NewReader("x"), tc.input, "document")
			if err != nil {
				t.Fatalf("Upload error: %v", err)
			}
			entry := svc.GetUpload(result.UploadID)
			if entry == nil {
				t.Fatal("entry not found")
			}
			for _, c := range entry.FileName {
				if string(c) == tc.contains {
					t.Errorf("filename %q contains %q", entry.FileName, tc.contains)
					break
				}
			}
			os.Remove(entry.TempPath)
		})
	}
}

func TestChatService_UploadInvalidMediaType(t *testing.T) {
	svc := newTestChatService(t)

	result, err := svc.Upload(strings.NewReader("x"), "test.txt", "invalid_type")
	if err != nil {
		t.Fatalf("Upload error: %v", err)
	}
	entry := svc.GetUpload(result.UploadID)
	if entry.MediaType != "document" {
		t.Errorf("expected mediaType 'document', got %q", entry.MediaType)
	}
	os.Remove(entry.TempPath)
}

func TestChatService_History(t *testing.T) {
	svc := newTestChatService(t)

	now := time.Now()
	svc.ChatStore.Append(ChatMessage{
		ID: "h1", Role: "user", Text: "first",
		Timestamp: now.Add(-2 * time.Minute),
	})
	svc.ChatStore.Append(ChatMessage{
		ID: "h2", Role: "assistant", Text: "second",
		Timestamp: now.Add(-time.Minute),
	})

	msgs := svc.History(50, time.Time{}, "")
	if len(msgs) != 2 {
		t.Fatalf("expected 2, got %d", len(msgs))
	}
	if msgs[0].ID != "h1" {
		t.Errorf("expected first message h1, got %q", msgs[0].ID)
	}
}

func TestChatService_HistoryLimitCap(t *testing.T) {
	svc := newTestChatService(t)

	// Requesting limit > 200 should be capped and return empty slice.
	msgs := svc.History(999, time.Time{}, "")
	if len(msgs) != 0 {
		t.Errorf("expected empty history, got %d", len(msgs))
	}
}

func TestChatService_ReactValid(t *testing.T) {
	svc := newTestChatService(t)
	svc.ChatStore.Append(ChatMessage{
		ID: "react-msg", Role: "assistant", Text: "test",
		Timestamp: time.Now(),
	})

	result, err := svc.React(ReactRequest{MsgID: "react-msg", Emoji: "👍"})
	if err != nil {
		t.Fatalf("React error: %v", err)
	}
	if !result.OK {
		t.Error("expected ok=true")
	}
}

func TestChatService_BuildPrompt(t *testing.T) {
	svc := newTestChatService(t)

	// Simple text message.
	prompt := svc.buildPrompt(ChatRequest{Message: "hello"})
	if prompt != "hello" {
		t.Errorf("expected %q, got %q", "hello", prompt)
	}

	// Message with reply context.
	svc.ChatStore.Append(ChatMessage{
		ID: "orig", Role: "assistant", Text: "original message",
		Timestamp: time.Now(),
	})
	prompt = svc.buildPrompt(ChatRequest{Message: "my reply", ReplyTo: "orig"})
	if !strings.Contains(prompt, "original message") {
		t.Error("expected reply context in prompt")
	}
	if !strings.Contains(prompt, "my reply") {
		t.Error("expected user text in prompt")
	}
}

func TestChatService_BuildPromptWithMedia(t *testing.T) {
	svc := newTestChatService(t)

	// Register a fake upload.
	svc.RegisterUpload(&UploadEntry{
		ID:       "upload-1",
		FileName: "photo.jpg",
		MimeType: "image/jpeg",
		TempPath: "/tmp/fake.jpg",
		CreatedAt: time.Now(),
	})

	prompt := svc.buildPrompt(ChatRequest{
		Message:  "check this",
		MediaIDs: []string{"upload-1"},
	})
	if !strings.Contains(prompt, "PHOTO") {
		t.Error("expected PHOTO reference in prompt")
	}
	if !strings.Contains(prompt, "check this") {
		t.Error("expected user text in prompt")
	}
}

func TestChatService_ResolveTierParams(t *testing.T) {
	svc := newTestChatService(t)

	// Default tiers should include "haiku".
	tp := svc.resolveTierParams("haiku")
	if tp.Model == "" {
		t.Error("expected non-empty model for haiku tier")
	}

	// Unknown tier should fallback.
	tp = svc.resolveTierParams("nonexistent")
	if tp.Model == "" {
		t.Error("expected fallback model")
	}
	if tp.Model != "claude-haiku-4-5" {
		t.Errorf("expected fallback model claude-haiku-4-5, got %q", tp.Model)
	}
}

func TestExtractReactionTag(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     string
		wantEmoji string
		wantText  string
	}{
		{"with emoji", "[[react:🔥]] Hello", "🔥", "Hello"},
		{"no tag", "Just text", "", "Just text"},
		{"none tag", "[[react:none]] Hello", "", "Hello"},
		{"empty emoji", "[[react:]] Hello", "", "Hello"},
		{"whitespace before", "  \n[[react:👍]] Hi", "👍", "Hi"},
		{"no closing", "[[react:🔥 broken", "", "[[react:🔥 broken"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emoji, text := extractReactionTag(tc.input)
			if emoji != tc.wantEmoji {
				t.Errorf("emoji: got %q, want %q", emoji, tc.wantEmoji)
			}
			if text != tc.wantText {
				t.Errorf("text: got %q, want %q", text, tc.wantText)
			}
		})
	}
}

func TestApiChatID_NegativeConstant(t *testing.T) {
	// Verify the sentinel doesn't collide with any valid Telegram ID.
	if apiChatID >= 0 {
		t.Errorf("apiChatID should be negative, got %d", apiChatID)
	}
}
