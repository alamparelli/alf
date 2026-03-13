package memory

import "testing"

func TestFilterSections(t *testing.T) {
	input := `shared content

<!-- @begin cli -->
cli-only content
<!-- @end cli -->

<!-- @begin api -->
api-only content
<!-- @end api -->

<!-- @begin tg -->
telegram-only
<!-- @end tg -->

<!-- @begin cc -->
cc-only
<!-- @end cc -->

footer`

	tests := []struct {
		name    string
		cfg     PromptConfig
		want    []string
		exclude []string
	}{
		{
			name:    "cli+tg",
			cfg:     PromptConfig{Backend: "cli", Channel: "tg"},
			want:    []string{"shared content", "cli-only content", "telegram-only", "footer"},
			exclude: []string{"api-only content", "cc-only"},
		},
		{
			name:    "api+cc",
			cfg:     PromptConfig{Backend: "api", Channel: "cc"},
			want:    []string{"shared content", "api-only content", "cc-only", "footer"},
			exclude: []string{"cli-only content", "telegram-only"},
		},
		{
			name:    "cli+cc",
			cfg:     PromptConfig{Backend: "cli", Channel: "cc"},
			want:    []string{"shared content", "cli-only content", "cc-only", "footer"},
			exclude: []string{"api-only content", "telegram-only"},
		},
		{
			name:    "api+tg",
			cfg:     PromptConfig{Backend: "api", Channel: "tg"},
			want:    []string{"shared content", "api-only content", "telegram-only", "footer"},
			exclude: []string{"cli-only content", "cc-only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterSections(input, tt.cfg)
			for _, s := range tt.want {
				if !contains(result, s) {
					t.Errorf("expected %q in result, got:\n%s", s, result)
				}
			}
			for _, s := range tt.exclude {
				if contains(result, s) {
					t.Errorf("unexpected %q in result, got:\n%s", s, result)
				}
			}
			// Markers should never appear in output.
			if contains(result, "<!-- @begin") || contains(result, "<!-- @end") {
				t.Errorf("markers should be stripped, got:\n%s", result)
			}
		})
	}
}

func TestFilterSections_NoTripleNewlines(t *testing.T) {
	input := `before

<!-- @begin cli -->
removed
<!-- @end cli -->

after`

	result := filterSections(input, PromptConfig{Backend: "api", Channel: "cc"})
	if contains(result, "\n\n\n") {
		t.Errorf("result should not have triple newlines:\n%q", result)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
