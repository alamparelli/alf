package comms

import "testing"

func TestExtractReaction_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantEmoji string
		wantText  string
	}{
		{
			name:      "no marker returns original text",
			input:     "Hello world",
			wantEmoji: "",
			wantText:  "Hello world",
		},
		{
			name:      "empty string",
			input:     "",
			wantEmoji: "",
			wantText:  "",
		},
		{
			name:      "valid emoji marker",
			input:     "[[react:👍]] Great job!",
			wantEmoji: "👍",
			wantText:  "Great job!",
		},
		{
			name:      "thumbs down emoji",
			input:     "[[react:👎]] Not so great",
			wantEmoji: "👎",
			wantText:  "Not so great",
		},
		{
			name:      "none yields empty emoji",
			input:     "[[react:none]] Some text here",
			wantEmoji: "",
			wantText:  "Some text here",
		},
		{
			name:      "empty emoji yields empty emoji",
			input:     "[[react:]] Some text here",
			wantEmoji: "",
			wantText:  "Some text here",
		},
		{
			name:      "no closing brackets returns original",
			input:     "[[react:👍 Great job!",
			wantEmoji: "",
			wantText:  "[[react:👍 Great job!",
		},
		{
			name:      "leading whitespace before marker",
			input:     "  \n [[react:🔥]] Nice work",
			wantEmoji: "🔥",
			wantText:  "Nice work",
		},
		{
			name:      "leading tabs and newlines",
			input:     "\t\n\r [[react:😂]] Funny",
			wantEmoji: "😂",
			wantText:  "Funny",
		},
		{
			name:      "only marker no trailing text",
			input:     "[[react:👍]]",
			wantEmoji: "👍",
			wantText:  "",
		},
		{
			name:      "marker with whitespace after",
			input:     "[[react:👍]]   \n  ",
			wantEmoji: "👍",
			wantText:  "",
		},
		{
			name:      "multiple markers all stripped",
			input:     "[[react:👍]] text [[react:🔥]] more",
			wantEmoji: "👍",
			wantText:  "text  more",
		},
		{
			name:      "marker in middle of text stripped",
			input:     "Hello [[react:👍]] world",
			wantEmoji: "",
			wantText:  "Hello  world",
		},
		{
			name:      "text emoji without marker syntax",
			input:     "react:👍 hello",
			wantEmoji: "",
			wantText:  "react:👍 hello",
		},
		{
			name:      "single bracket prefix not matched",
			input:     "[react:👍]] hello",
			wantEmoji: "",
			wantText:  "[react:👍]] hello",
		},
		{
			name:      "text-based emoji name",
			input:     "[[react:thumbsup]] All good",
			wantEmoji: "thumbsup",
			wantText:  "All good",
		},
		{
			name:      "marker with newline before rest",
			input:     "[[react:❤️]]\nI love it",
			wantEmoji: "❤️",
			wantText:  "I love it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEmoji, gotText := ExtractReaction(tt.input)
			if gotEmoji != tt.wantEmoji {
				t.Errorf("emoji: got %q, want %q", gotEmoji, tt.wantEmoji)
			}
			if gotText != tt.wantText {
				t.Errorf("text: got %q, want %q", gotText, tt.wantText)
			}
		})
	}
}
