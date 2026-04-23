package ai_test

import (
	"testing"

	"github.com/alamparelli/alf/internal/ai"
)

func TestResolveModel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ai.ModelID
	}{
		{"haiku short", "haiku", "claude-haiku-4-5"},
		{"sonnet short", "sonnet", "claude-sonnet-4-6"},
		{"opus short", "opus", "claude-opus-4-6"},
		{"sonnet-max short", "sonnet-max", "claude-sonnet-4-6-max"},
		{"opus-max short", "opus-max", "claude-opus-4-6-max"},
		{"case-insensitive", "Haiku", "claude-haiku-4-5"},
		{"full id passthrough", "claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"custom claude id passthrough", "claude-opus-4-7-1m", "claude-opus-4-7-1m"},
		{"unknown short returns empty", "unknown", ""},
		{"empty input returns empty", "", ""},
		{"non-claude custom returns empty", "gpt-4", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ai.ResolveModel(tc.input)
			if got != tc.want {
				t.Errorf("ResolveModel(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
