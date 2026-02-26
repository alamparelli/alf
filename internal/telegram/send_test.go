package telegram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendMessage_HTMLParseMode(t *testing.T) {
	var receivedPayloads []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		receivedPayloads = append(receivedPayloads, payload)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := &Client{
		Token: "test-token",
		HTTP:  srv.Client(),
	}
	// Override the URL by using a custom transport
	client.HTTP.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	err := client.SendMessage(123, "**bold** text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(receivedPayloads) == 0 {
		t.Fatal("no payloads received")
	}

	payload := receivedPayloads[0]
	if payload["parse_mode"] != "HTML" {
		t.Errorf("expected parse_mode=HTML, got %v", payload["parse_mode"])
	}
	text := payload["text"].(string)
	if !strings.Contains(text, "<b>bold</b>") {
		t.Errorf("expected HTML formatting in text, got: %s", text)
	}
}

func TestSendMessage_FallbackOnError(t *testing.T) {
	callCount := 0
	var payloads []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		payloads = append(payloads, payload)
		callCount++
		if callCount == 1 {
			// First call fails (simulating HTML parse error)
			w.Write([]byte(`{"ok":false,"description":"Bad Request: can't parse entities"}`))
		} else {
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()

	client := &Client{
		Token: "test-token",
		HTTP:  srv.Client(),
	}
	client.HTTP.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	err := client.SendMessage(123, "**bold**")
	if err != nil {
		t.Fatalf("unexpected error after fallback: %v", err)
	}

	if len(payloads) < 2 {
		t.Fatalf("expected at least 2 calls (original + fallback), got %d", len(payloads))
	}

	// Second call should not have parse_mode (plain text fallback)
	if payloads[1]["parse_mode"] != nil {
		t.Errorf("fallback should not have parse_mode, got %v", payloads[1]["parse_mode"])
	}
}

func TestSendHTML(t *testing.T) {
	var payload map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&payload)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := &Client{
		Token: "test-token",
		HTTP:  srv.Client(),
	}
	client.HTTP.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	err := client.SendHTML(123, "<b>hello</b>")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if payload["parse_mode"] != "HTML" {
		t.Errorf("expected parse_mode=HTML, got %v", payload["parse_mode"])
	}
}

func TestSendKeyboard(t *testing.T) {
	var payload map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&payload)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := &Client{
		Token: "test-token",
		HTTP:  srv.Client(),
	}
	client.HTTP.Transport = &rewriteTransport{base: srv.Client().Transport, target: srv.URL}

	kb := map[string]any{
		"inline_keyboard": [][]map[string]string{
			{{"text": "Option", "callback_data": "opt1"}},
		},
	}
	err := client.SendKeyboard(123, "Choose:", kb)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if payload["reply_markup"] == nil {
		t.Error("expected reply_markup in payload")
	}
}

// rewriteTransport redirects all requests to the test server.
type rewriteTransport struct {
	base   http.RoundTripper
	target string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.target, "http://")
	if t.base != nil {
		return t.base.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}
