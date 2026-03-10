package cli

import "testing"

func TestExtractOAuthToken(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single line",
			input: "Your OAuth token:\n\nsk-ant-oat01-abc123DEF_-xyz\n\nStore this token securely.",
			want:  "sk-ant-oat01-abc123DEF_-xyz",
		},
		{
			name: "wrapped across two lines",
			input: "Your OAuth token (valid for 1 year):\n\n" +
				"sk-ant-oat01-HAaW4FGPoxX4naF2xM0HuS_ybQZF7k5k-0Hn25Nfk6aMyyT_XszxwW027dzWRl0xjAV\n" +
				"xQzkWFTbkSW7WEqQgUQ-C3vu5AAA\n\n" +
				"Store this token securely. You won't be able to see it again.",
			want: "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",
		},
		{
			name: "with ANSI codes",
			input: "\x1b[1mYour OAuth token:\x1b[0m\n\nsk-ant-oat01-token123\n\nStore this token securely.",
			want:  "sk-ant-oat01-token123",
		},
		{
			name: "no token",
			input: "Error: authentication failed",
			want:  "",
		},
		{
			name: "token not joined with prose",
			input: "sk-ant-oat01-abc123AAA\nStorethistokensecurely",
			want:  "sk-ant-oat01-abc123AAA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOAuthToken(tt.input)
			if got != tt.want {
				t.Errorf("extractOAuthToken() = %q, want %q", got, tt.want)
			}
		})
	}
}
