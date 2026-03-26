package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	tgclient "github.com/alamparelli/alf/internal/telegram"
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
			want: "[The user is replying to this previous message:\n---\noriginal\n---\n]\nresponse",
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
			want: "[The user is replying to this previous message:\n---\nasked about this\n---\n]\nmy photo",
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

// fakeTGServer captures which TG API method was called.
func fakeTGServer(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var calledMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract method from path: /bot<token>/<method>
		calledMethod = r.URL.Path[len("/bottest-token/"):]
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1}})
	}))
	return srv, &calledMethod
}

func newTestTG(srv *httptest.Server) *tgclient.Client {
	c := tgclient.NewClient("test-token")
	c.HTTP = srv.Client()
	// Override API base by swapping the token to include server URL.
	// The client builds URLs as https://api.telegram.org/bot{token}/{method}
	// We need to redirect to our test server instead.
	return c
}

func TestSendTGNotify_PlainText(t *testing.T) {
	srv, method := fakeTGServer(t)
	defer srv.Close()
	tg := newTestTGWithBase(t, srv)

	err := sendTGNotify(tg, 123, "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *method != "sendMessage" {
		t.Errorf("expected sendMessage, got %s", *method)
	}
}

func TestSendTGNotify_GifURL(t *testing.T) {
	srv, method := fakeTGServer(t)
	defer srv.Close()
	tg := newTestTGWithBase(t, srv)

	err := sendTGNotify(tg, 123, "https://media.example.com/funny.gif")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *method != "sendAnimation" {
		t.Errorf("expected sendAnimation for .gif URL, got %s", *method)
	}
}

func TestSendTGNotify_VideoURL(t *testing.T) {
	srv, method := fakeTGServer(t)
	defer srv.Close()
	tg := newTestTGWithBase(t, srv)

	err := sendTGNotify(tg, 123, "https://media.example.com/clip.mp4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *method != "sendVideo" {
		t.Errorf("expected sendVideo for .mp4 URL, got %s", *method)
	}
}

func TestSendTGNotify_GifWithCaption(t *testing.T) {
	srv, method := fakeTGServer(t)
	defer srv.Close()
	tg := newTestTGWithBase(t, srv)

	err := sendTGNotify(tg, 123, "https://media.example.com/funny.gif\nCheck this out!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *method != "sendAnimation" {
		t.Errorf("expected sendAnimation for .gif URL with caption, got %s", *method)
	}
}

func TestSendTGNotify_TextWithURL(t *testing.T) {
	srv, method := fakeTGServer(t)
	defer srv.Close()
	tg := newTestTGWithBase(t, srv)

	// Text containing a URL but not just a URL — should use sendMessage.
	err := sendTGNotify(tg, 123, "Check this: https://example.com/image.gif is cool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *method != "sendMessage" {
		t.Errorf("expected sendMessage for text with embedded URL, got %s", *method)
	}
}

func TestSendTGNotify_LocalVideoFile(t *testing.T) {
	srv, method := fakeTGServer(t)
	defer srv.Close()
	tg := newTestTGWithBase(t, srv)

	// Create a temp video file.
	tmp := filepath.Join(t.TempDir(), "clip.mp4")
	os.WriteFile(tmp, []byte("fake video"), 0o644)

	err := sendTGNotify(tg, 123, tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *method != "sendVideo" {
		t.Errorf("expected sendVideo for local .mp4, got %s", *method)
	}
}

func TestSendTGNotify_LocalGifFile(t *testing.T) {
	srv, method := fakeTGServer(t)
	defer srv.Close()
	tg := newTestTGWithBase(t, srv)

	tmp := filepath.Join(t.TempDir(), "anim.gif")
	os.WriteFile(tmp, []byte("fake gif"), 0o644)

	err := sendTGNotify(tg, 123, tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *method != "sendAnimation" {
		t.Errorf("expected sendAnimation for local .gif, got %s", *method)
	}
}

func TestSendTGNotify_LocalFileWithCaption(t *testing.T) {
	srv, method := fakeTGServer(t)
	defer srv.Close()
	tg := newTestTGWithBase(t, srv)

	tmp := filepath.Join(t.TempDir(), "demo.mp4")
	os.WriteFile(tmp, []byte("fake video"), 0o644)

	err := sendTGNotify(tg, 123, tmp+"\nHere's the demo!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *method != "sendVideo" {
		t.Errorf("expected sendVideo for local .mp4 with caption, got %s", *method)
	}
}

func TestSendTGNotify_LocalDocumentFile(t *testing.T) {
	srv, method := fakeTGServer(t)
	defer srv.Close()
	tg := newTestTGWithBase(t, srv)

	tmp := filepath.Join(t.TempDir(), "report.pdf")
	os.WriteFile(tmp, []byte("fake pdf"), 0o644)

	err := sendTGNotify(tg, 123, tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *method != "sendDocument" {
		t.Errorf("expected sendDocument for local .pdf, got %s", *method)
	}
}

func TestSendTGNotify_NonexistentFile(t *testing.T) {
	srv, _ := fakeTGServer(t)
	defer srv.Close()
	tg := newTestTGWithBase(t, srv)

	// Path that doesn't exist — should error, not silently send as text.
	err := sendTGNotify(tg, 123, "/tmp/nonexistent-file-12345.mp4")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

// newTestTGWithBase creates a TG client that points to the test server.
func newTestTGWithBase(t *testing.T, srv *httptest.Server) *tgclient.Client {
	t.Helper()
	c := &tgclient.Client{
		Token: fmt.Sprintf("test-token"),
		HTTP:  srv.Client(),
	}
	// Monkey-patch: the client uses https://api.telegram.org/bot{token}/{method}
	// We override by setting the token to route through our server.
	// Since we can't change the base URL, we use a transport redirect.
	c.HTTP.Transport = &urlRewriter{base: srv.URL, inner: http.DefaultTransport}
	return c
}

type urlRewriter struct {
	base  string
	inner http.RoundTripper
}

func (u *urlRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	// Extract path after api.telegram.org
	path := req.URL.Path
	req.URL.Host = u.base[len("http://"):]
	req.URL.Path = path
	return u.inner.RoundTrip(req)
}
