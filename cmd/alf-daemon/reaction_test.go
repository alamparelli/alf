package main

import "testing"

func TestExtractReaction(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantEmoji string
		wantText  string
	}{
		{"with emoji", "[[react:🔥]] Hello there!", "🔥", "Hello there!"},
		{"with none", "[[react:none]] Hello there!", "", "Hello there!"},
		{"no marker", "Hello there!", "", "Hello there!"},
		{"leading whitespace", "  [[react:👍]] Nice work", "👍", "Nice work"},
		{"leading newline", "\n[[react:😂]] That's funny", "😂", "That's funny"},
		{"empty emoji", "[[react:]] Hello", "", "Hello"},
		{"no closing bracket", "[[react:🔥 Hello", "", "[[react:🔥 Hello"},
		{"multi-char emoji", "[[react:❤️]] Love it", "❤️", "Love it"},
		{"empty input", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			emoji, text := extractReaction(tt.input)
			if emoji != tt.wantEmoji {
				t.Errorf("emoji: got %q, want %q", emoji, tt.wantEmoji)
			}
			if text != tt.wantText {
				t.Errorf("text: got %q, want %q", text, tt.wantText)
			}
		})
	}
}
