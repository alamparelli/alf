package controlcenter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/eventlog"
	"github.com/alamparelli/alf/internal/memory"
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

	mem := memory.NewInMem()

	return NewChatService(
		dataDir, configDir, contextDir,
		tierStore, sessions, eventLog, mem,
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

// appendTestMessage is a small helper that appends a message to the memory
// store and returns the store-assigned ID. Used by tests that need to
// reference specific messages by ID (reactions, replies, etc.).
func appendTestMessage(t *testing.T, svc *ChatService, convID, role, text string) string {
	t.Helper()
	ctx := context.Background()
	_ = svc.Memory.EnsureConv(ctx, memory.ConvID(convID), "", "cc")
	stored, err := svc.Memory.AppendMessage(ctx, memory.ConvID(convID), memory.Message{
		Role: role, Channel: "cc", Content: text,
		Blocks: []memory.ContentBlock{{Type: memory.BlockText, Text: text}},
	})
	if err != nil {
		t.Fatalf("append test message: %v", err)
	}
	return string(stored.ID)
}

func TestChatService_History(t *testing.T) {
	svc := newTestChatService(t)

	_ = appendTestMessage(t, svc, "test", "user", "first")
	_ = appendTestMessage(t, svc, "test", "assistant", "second")

	msgs := svc.History(50, time.Time{}, "test")
	if len(msgs) != 2 {
		t.Fatalf("expected 2, got %d", len(msgs))
	}
	if msgs[0].Text != "first" {
		t.Errorf("expected first message text 'first', got %q", msgs[0].Text)
	}
}

func TestChatService_HistoryLimitCap(t *testing.T) {
	svc := newTestChatService(t)

	// Requesting limit > 200 with empty convID should be capped and return empty slice.
	msgs := svc.History(999, time.Time{}, "")
	if len(msgs) != 0 {
		t.Errorf("expected empty history, got %d", len(msgs))
	}
}

func TestChatService_ReactValid(t *testing.T) {
	svc := newTestChatService(t)
	id := appendTestMessage(t, svc, "test", "assistant", "test")

	result, err := svc.React(ReactRequest{MsgID: id, Emoji: "👍"})
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
	origID := appendTestMessage(t, svc, "test", "assistant", "original message")
	prompt = svc.buildPrompt(ChatRequest{Message: "my reply", ReplyTo: origID, ConvID: "test"})
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

	// Unknown tier should fallback to the user's configured default
	// (via DefaultFallbackModel), not a hardcoded Claude model.
	tp = svc.resolveTierParams("nonexistent")
	if tp.Model == "" {
		t.Error("expected fallback model resolved from user config")
	}
	// Verify the fallback actually matches a configured tier (no baked value).
	tiers := svc.TierStore.Current()
	want := DefaultFallbackModel(tiers)
	if tp.Model != want {
		t.Errorf("fallback mismatch: got %q, want %q (from DefaultFallbackModel)", tp.Model, want)
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

// --- buildPrompt: reply context tests ---

func TestBuildPrompt_ReplyExistingMessage(t *testing.T) {
	svc := newTestChatService(t)
	id := appendTestMessage(t, svc, "c1", "assistant", "This is the original message.")

	prompt := svc.buildPrompt(ChatRequest{
		Message: "what did you mean?",
		ReplyTo: id,
		ConvID:  "c1",
	})

	if !strings.Contains(prompt, "This is the original message.") {
		t.Error("expected original text in reply context")
	}
	if !strings.Contains(prompt, "replying to this previous message") {
		t.Error("expected reply context preamble")
	}
	if !strings.Contains(prompt, "what did you mean?") {
		t.Error("expected user message in prompt")
	}
}

func TestBuildPrompt_ReplyLongMessageNotTruncated(t *testing.T) {
	// The current implementation does NOT truncate long replies.
	// This test documents that behavior — if truncation is added later,
	// update this test accordingly.
	svc := newTestChatService(t)

	longText := strings.Repeat("x", 2000)
	id := appendTestMessage(t, svc, "c1", "assistant", longText)

	prompt := svc.buildPrompt(ChatRequest{Message: "reply", ReplyTo: id, ConvID: "c1"})
	if !strings.Contains(prompt, longText) {
		t.Error("expected full (untruncated) original text in reply context")
	}
}

func TestBuildPrompt_ReplyNonExistentMessage(t *testing.T) {
	svc := newTestChatService(t)

	prompt := svc.buildPrompt(ChatRequest{
		Message: "replying to nothing",
		ReplyTo: "does-not-exist",
	})

	// Should just be the user message with no reply context.
	if prompt != "replying to nothing" {
		t.Errorf("expected plain message, got %q", prompt)
	}
}

func TestBuildPrompt_ReplyWithNilMemory(t *testing.T) {
	svc := newTestChatService(t)
	svc.Memory = nil

	// buildPrompt guards against nil Memory — no panic when ReplyTo is unset.
	prompt := svc.buildPrompt(ChatRequest{Message: "hello", ReplyTo: ""})
	if prompt != "hello" {
		t.Errorf("expected %q, got %q", "hello", prompt)
	}
}

// --- buildPrompt: reply + media combined ---

func TestBuildPrompt_ReplyWithMedia(t *testing.T) {
	svc := newTestChatService(t)
	id := appendTestMessage(t, svc, "c1", "assistant", "look at this")

	svc.RegisterUpload(&UploadEntry{
		ID:        "img-1",
		FileName:  "screenshot.png",
		MimeType:  "image/png",
		TempPath:  "/tmp/fake-screenshot.png",
		CreatedAt: time.Now(),
	})

	prompt := svc.buildPrompt(ChatRequest{
		Message:  "here is my screenshot",
		ReplyTo:  id,
		ConvID:   "c1",
		MediaIDs: []string{"img-1"},
	})

	// Should contain all three parts: reply context, media ref, user text.
	if !strings.Contains(prompt, "look at this") {
		t.Error("missing reply context")
	}
	if !strings.Contains(prompt, "PHOTO") {
		t.Error("missing photo media reference")
	}
	if !strings.Contains(prompt, "here is my screenshot") {
		t.Error("missing user message")
	}

	// Verify ordering: reply context first, then media, then user text.
	replyIdx := strings.Index(prompt, "replying to this previous message")
	photoIdx := strings.Index(prompt, "PHOTO")
	textIdx := strings.Index(prompt, "here is my screenshot")
	if replyIdx >= photoIdx || photoIdx >= textIdx {
		t.Errorf("wrong order: reply@%d photo@%d text@%d", replyIdx, photoIdx, textIdx)
	}
}

func TestBuildPrompt_MediaOnly_NoText(t *testing.T) {
	svc := newTestChatService(t)
	svc.RegisterUpload(&UploadEntry{
		ID:        "doc-1",
		FileName:  "notes.txt",
		MimeType:  "text/plain",
		TempPath:  "/tmp/fake-notes.txt",
		CreatedAt: time.Now(),
	})

	prompt := svc.buildPrompt(ChatRequest{
		Message:  "",
		MediaIDs: []string{"doc-1"},
	})
	if !strings.Contains(prompt, "notes.txt") {
		t.Error("expected file reference in prompt")
	}
}

// --- extractReactionTag parity ---

func TestExtractReactionTag_ParityWithTelegram(t *testing.T) {
	// extractReactionTag is the CC equivalent of extractReaction in the Telegram
	// handler (internal/telegram/). Both parse the [[react:emoji]] prefix from
	// LLM output. This test documents the shared contract so changes to the
	// format are caught in both paths.
	//
	// If you change extractReactionTag behavior, also update the TG equivalent.
	for _, tc := range []struct {
		input     string
		wantEmoji string
		wantText  string
	}{
		{"[[react:👍]] Great job!", "👍", "Great job!"},
		{"[[react:none]] neutral", "", "neutral"},
		{"no reaction here", "", "no reaction here"},
		{"[[react:🔥]]", "🔥", ""},
		{"[[react:🔥]]  ", "🔥", ""},
	} {
		emoji, text := extractReactionTag(tc.input)
		if emoji != tc.wantEmoji || text != tc.wantText {
			t.Errorf("extractReactionTag(%q) = (%q, %q), want (%q, %q)",
				tc.input, emoji, text, tc.wantEmoji, tc.wantText)
		}
	}
}

// --- Force command: tier lookup + message extraction ---

func TestForceCommand_TierLookupPattern(t *testing.T) {
	// Tests the force command parsing logic that lives in askViaEngine.
	// We replicate the matching algorithm here since it's not extracted into
	// a standalone function. If it gets refactored, replace with a direct call.
	tiers := DefaultTiersConfig().Tiers

	for _, tc := range []struct {
		name      string
		input     string
		wantTier  string
		wantMsg   string
		wantMatch bool
	}{
		{
			name:      "known force tier with message",
			input:     "/codex-fast what time is it",
			wantTier:  "codex-fast",
			wantMsg:   "what time is it",
			wantMatch: true,
		},
		{
			name:      "known force tier without message",
			input:     "/codex-fast",
			wantTier:  "codex-fast",
			wantMsg:   "",
			wantMatch: true,
		},
		{
			name:      "unknown tier",
			input:     "/nonexistent hello",
			wantTier:  "",
			wantMsg:   "",
			wantMatch: false,
		},
		{
			name:      "not a command",
			input:     "hello world",
			wantTier:  "",
			wantMsg:   "",
			wantMatch: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matched := false
			var gotTier, gotMsg string

			if strings.HasPrefix(tc.input, "/") {
				parts := strings.SplitN(tc.input, " ", 2)
				cmdName := strings.TrimPrefix(parts[0], "/")

				for _, tier := range tiers {
					if tier.Enabled && tier.ForceCommand && tier.Name == cmdName {
						matched = true
						gotTier = tier.Name
						if len(parts) > 1 {
							gotMsg = strings.TrimSpace(parts[1])
						}
						break
					}
				}
			}

			if matched != tc.wantMatch {
				t.Errorf("match: got %v, want %v", matched, tc.wantMatch)
			}
			if gotTier != tc.wantTier {
				t.Errorf("tier: got %q, want %q", gotTier, tc.wantTier)
			}
			if gotMsg != tc.wantMsg {
				t.Errorf("msg: got %q, want %q", gotMsg, tc.wantMsg)
			}
		})
	}
}
