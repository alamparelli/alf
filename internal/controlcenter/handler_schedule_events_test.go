package controlcenter

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestScheduleEventBroker_NotifyWithNoClientsIsSafe(t *testing.T) {
	b := NewScheduleEventBroker()
	b.Notify() // must not panic
}

func TestScheduleEventBroker_ServeHTTP_RejectsNonGet(t *testing.T) {
	b := NewScheduleEventBroker()
	req := httptest.NewRequest(http.MethodPost, "/api/schedules/events", nil)
	rec := httptest.NewRecorder()
	b.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 on POST, got %d", rec.Code)
	}
}

func TestScheduleEventBroker_StreamsChangeEvents(t *testing.T) {
	b := NewScheduleEventBroker()
	server := httptest.NewServer(b)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %q", ct)
	}

	// Read SSE events line-by-line on a background goroutine so we can
	// wait with a timeout for a "change" event after calling Notify.
	events := make(chan string, 8)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			events <- scanner.Text()
		}
	}()

	// Wait for the initial ping. This confirms the server-side goroutine has
	// registered our channel, so the subsequent Notify won't race the
	// registration.
	pingDeadline := time.After(1 * time.Second)
	for {
		select {
		case line := <-events:
			if strings.Contains(line, "ping") || strings.Contains(line, "data: connected") {
				goto gotPing
			}
		case <-pingDeadline:
			t.Fatal("never received initial ping")
		}
	}
gotPing:

	// A small delay still helps: the goroutine registers itself after
	// Flush, and pipelined reads may arrive slightly before registration.
	time.Sleep(30 * time.Millisecond)
	b.Notify()

	changeDeadline := time.After(2 * time.Second)
	for {
		select {
		case line, ok := <-events:
			if !ok {
				t.Fatal("stream closed before change event")
			}
			if strings.Contains(line, "change") || strings.Contains(line, "data: reload") {
				return
			}
		case <-changeDeadline:
			t.Fatal("change event not received within timeout")
		}
	}
}
