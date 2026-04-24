package runtime

import (
	"testing"
)

func TestChannelID_Prefix(t *testing.T) {
	tests := []struct {
		id   ChannelID
		want string
	}{
		{"tg:12345", "tg"},
		{"cc:default", "cc"},
		{"cc:conv_abc", "cc"},
		{"tg:-100123456", "tg"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		if got := tt.id.Prefix(); got != tt.want {
			t.Errorf("ChannelID(%q).Prefix() = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestChannelID_SessionKey(t *testing.T) {
	tests := []struct {
		id   ChannelID
		want int64
	}{
		{"tg:12345", 12345},
		{"tg:999999999", 999999999},
		{"cc:default", -1},
		{"tg:notanumber", -1},
		{"unknown", -1},
	}
	for _, tt := range tests {
		if got := tt.id.SessionKey(); got != tt.want {
			t.Errorf("ChannelID(%q).SessionKey() = %d, want %d", tt.id, got, tt.want)
		}
	}

	// CC conv_ids must produce unique, negative session keys.
	keyA := ChannelID("cc:conv_abc").SessionKey()
	keyB := ChannelID("cc:conv_xyz").SessionKey()
	if keyA == keyB {
		t.Errorf("different cc conv_ids must produce different session keys, both got %d", keyA)
	}
	if keyA >= 0 || keyB >= 0 {
		t.Errorf("cc session keys must be negative, got %d and %d", keyA, keyB)
	}
	if keyA == -1 || keyB == -1 {
		t.Errorf("cc session keys must not be -1 (default), got %d and %d", keyA, keyB)
	}
}

func TestChannelID_ConvChannel(t *testing.T) {
	tests := []struct {
		id   ChannelID
		want string
	}{
		{"tg:123", "tg"},
		{"cc:default", "cc"},
	}
	for _, tt := range tests {
		if got := tt.id.ConvChannel(); got != tt.want {
			t.Errorf("ChannelID(%q).ConvChannel() = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestTierParams_EffectiveContextWeight(t *testing.T) {
	tests := []struct {
		weight string
		want   string
	}{
		{"light", "light"},
		{"standard", "standard"},
		{"full", "full"},
		{"", "full"},
		{"unknown", "full"},
	}
	for _, tt := range tests {
		tp := TierParams{ContextWeight: tt.weight}
		if got := tp.EffectiveContextWeight(); got != tt.want {
			t.Errorf("TierParams{ContextWeight: %q}.EffectiveContextWeight() = %q, want %q", tt.weight, got, tt.want)
		}
	}
}

func TestExtractReaction(t *testing.T) {
	tests := []struct {
		input     string
		wantEmoji string
		wantText  string
	}{
		{"[[react:🔥]] Hello", "🔥", "Hello"},
		{"[[react:none]] Hello", "", "Hello"},
		{"[[react:]] Hello", "", "Hello"},
		{"No reaction here", "", "No reaction here"},
		{"  \n[[react:👍]] Nice", "👍", "Nice"},
		{"[[react:😂]]", "😂", ""},
	}
	for _, tt := range tests {
		emoji, text := ExtractReaction(tt.input)
		if emoji != tt.wantEmoji || text != tt.wantText {
			t.Errorf("ExtractReaction(%q) = (%q, %q), want (%q, %q)", tt.input, emoji, text, tt.wantEmoji, tt.wantText)
		}
	}
}
