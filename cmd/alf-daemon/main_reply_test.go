package main

import (
	"testing"
)

func TestExtractReplyContext(t *testing.T) {
	tests := []struct {
		name    string
		msg     *Message
		want    string
	}{
		{
			name: "no reply",
			msg: &Message{
				Text: "hello",
			},
			want: "",
		},
		{
			name: "reply with text",
			msg: &Message{
				Text: "hello",
				ReplyToMessage: &Message{
					Text: "original message",
				},
			},
			want: "original message",
		},
		{
			name: "reply with long text",
			msg: &Message{
				Text: "hello",
				ReplyToMessage: &Message{
					Text: "a" + string(make([]byte, 600)), // 601 chars — no longer truncated
				},
			},
			want: "a" + string(make([]byte, 600)), // extractReplyContext returns full text
		},
		{
			name: "nil message",
			msg:  nil,
			want: "",
		},
		{
			name: "nil reply",
			msg: &Message{
				Text:           "hello",
				ReplyToMessage: nil,
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractReplyContext(tt.msg)
			if got != tt.want {
				t.Errorf("extractReplyContext() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrependReplyContext(t *testing.T) {
	tests := []struct {
		name    string
		msg     *Message
		want    string
	}{
		{
			name: "no reply",
			msg: &Message{
				Text: "hello",
			},
			want: "hello",
		},
		{
			name: "reply with text",
			msg: &Message{
				Text: "my response",
				ReplyToMessage: &Message{
					Text: "original question",
				},
			},
			want: "[The user is replying to this previous message:\n---\noriginal question\n---\n]\nmy response",
		},
		{
			name: "reply with long text",
			msg: &Message{
				Text: "response",
				ReplyToMessage: &Message{
					Text: "a" + string(make([]byte, 600)), // 601 chars
				},
			},
			want: "[The user is replying to this previous message:\n---\n" + "a" + string(make([]byte, 600)) + "\n---\n]\nresponse",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prependReplyContext(tt.msg)
			if got != tt.want {
				t.Errorf("prependReplyContext() = %q, want %q", got, tt.want)
			}
		})
	}
}
