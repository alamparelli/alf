package runtime

import (
	"testing"
)

func TestInMessage_DisplayText(t *testing.T) {
	// RawText set → wins.
	m := InMessage{Text: "prompt with media refs", RawText: "user raw"}
	if got := m.DisplayText(); got != "user raw" {
		t.Errorf("expected RawText to win, got %q", got)
	}

	// RawText empty → falls back to Text.
	m2 := InMessage{Text: "only text"}
	if got := m2.DisplayText(); got != "only text" {
		t.Errorf("expected fallback to Text, got %q", got)
	}
}

func TestChannelID_ConvChannel_Default(t *testing.T) {
	// Unknown prefix should pass through to Prefix() directly.
	c := ChannelID("foo:123")
	if got := c.ConvChannel(); got != "foo" {
		t.Errorf("unknown prefix: got %q, want %q", got, "foo")
	}
	// No colon → whole string is the prefix.
	c2 := ChannelID("unknown")
	if got := c2.ConvChannel(); got != "unknown" {
		t.Errorf("colon-less: got %q, want %q", got, "unknown")
	}
}
