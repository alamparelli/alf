package main

import (
	"testing"
)

func TestBuildMessageContent(t *testing.T) {
	tests := []struct {
		name    string
		msg     *Message
		want    string
	}{
		{
			name: "text only",
			msg: &Message{
				Text: "hello",
			},
			want: "hello",
		},
		{
			name: "photo with caption",
			msg: &Message{
				Caption: "check this out",
				Photo:   []*Photo{{FileID: "abc"}},
			},
			want: "check this out",
		},
		{
			name: "photo with caption and text",
			msg: &Message{
				Text:    "more info",
				Caption: "check this",
				Photo:   []*Photo{{FileID: "abc"}},
			},
			want: "check this\nmore info",
		},
		{
			name: "reply with text",
			msg: &Message{
				Text: "response",
				ReplyToMessage: &Message{
					Text: "original",
				},
			},
			want: "[En réponse à : \"original\"]\nresponse",
		},
		{
			name: "reply with photo caption",
			msg: &Message{
				Caption: "my photo",
				Photo:   []*Photo{{FileID: "abc"}},
				ReplyToMessage: &Message{
					Text: "asked about this",
				},
			},
			want: "[En réponse à : \"asked about this\"]\nmy photo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildMessageContent(tt.msg)
			if got != tt.want {
				t.Errorf("buildMessageContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHasMedia(t *testing.T) {
	tests := []struct {
		name    string
		msg     *Message
		want    bool
	}{
		{
			name: "text only",
			msg: &Message{
				Text: "hello",
			},
			want: false,
		},
		{
			name: "with photo",
			msg: &Message{
				Photo: []*Photo{{FileID: "abc"}},
			},
			want: true,
		},
		{
			name: "with document",
			msg: &Message{
				Document: &Document{FileID: "abc"},
			},
			want: true,
		},
		{
			name: "with voice",
			msg: &Message{
				Voice: &Voice{FileID: "abc"},
			},
			want: true,
		},
		{
			name: "with video",
			msg: &Message{
				Video: &Video{FileID: "abc"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasMedia(tt.msg)
			if got != tt.want {
				t.Errorf("hasMedia() = %v, want %v", got, tt.want)
			}
		})
	}
}
