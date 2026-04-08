package main

import (
	"strings"
	"testing"
)

func TestBuildRouterMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  *Message
		want string
	}{
		{
			name: "text only",
			msg:  &Message{Text: "hello"},
			want: "hello",
		},
		{
			name: "caption only",
			msg:  &Message{Caption: "a caption"},
			want: "a caption",
		},
		{
			name: "text and caption prepends caption",
			msg:  &Message{Text: "body", Caption: "cap"},
			want: "cap\nbody",
		},
		{
			name: "empty message returns empty",
			msg:  &Message{},
			want: "",
		},
		{
			name: "reply with text",
			msg: &Message{
				Text:           "my reply",
				ReplyToMessage: &Message{Text: "original"},
			},
			want: "[Replying to: \"original\"]\nmy reply",
		},
		{
			name: "reply truncates quoted text over 100 chars",
			msg: &Message{
				Text:           "short",
				ReplyToMessage: &Message{Text: strings.Repeat("x", 150)},
			},
			want: "[Replying to: \"" + strings.Repeat("x", 100) + "...\"]\nshort",
		},
		{
			name: "reply exactly 100 chars not truncated",
			msg: &Message{
				Text:           "ok",
				ReplyToMessage: &Message{Text: strings.Repeat("a", 100)},
			},
			want: "[Replying to: \"" + strings.Repeat("a", 100) + "\"]\nok",
		},
		{
			name: "reply with no additional text",
			msg: &Message{
				ReplyToMessage: &Message{Text: "quoted"},
			},
			want: "[Replying to: \"quoted\"] (no additional text)",
		},
		{
			name: "reply with caption becomes text",
			msg: &Message{
				Caption:        "photo desc",
				ReplyToMessage: &Message{Text: "orig"},
			},
			want: "[Replying to: \"orig\"]\nphoto desc",
		},
		{
			name: "reply with caption and text",
			msg: &Message{
				Text:           "body",
				Caption:        "cap",
				ReplyToMessage: &Message{Text: "orig"},
			},
			want: "[Replying to: \"orig\"]\ncap\nbody",
		},
		{
			name: "reply to empty quoted text with no text",
			msg: &Message{
				ReplyToMessage: &Message{},
			},
			want: "[Replying to: \"\"] (no additional text)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildRouterMessage(tt.msg)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtFromMime(t *testing.T) {
	tests := []struct {
		name     string
		mimeType string
		fileName string
		want     string
	}{
		{"jpeg", "image/jpeg", "", ".jpg"},
		{"png", "image/png", "", ".png"},
		{"gif", "image/gif", "", ".gif"},
		{"webp", "image/webp", "", ".webp"},
		{"pdf", "application/pdf", "", ".pdf"},
		{"mp4", "video/mp4", "", ".mp4"},
		{"mov", "video/quicktime", "", ".mov"},
		{"webm", "video/webm", "", ".webm"},
		{"mkv", "video/x-matroska", "", ".mkv"},
		{"unknown mime fallback to filename ext", "application/octet-stream", "doc.xlsx", ".xlsx"},
		{"unknown mime no filename ext", "application/octet-stream", "noext", ""},
		{"empty mime with filename", "", "file.tar.gz", ".gz"},
		{"empty mime empty filename", "", "", ""},
		{"known mime ignores filename ext", "image/png", "photo.jpeg", ".png"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extFromMime(tt.mimeType, tt.fileName)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
