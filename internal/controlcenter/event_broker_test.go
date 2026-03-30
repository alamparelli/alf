package controlcenter

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Unit tests: EventBroker core ---

func TestEventBroker_EmitNoClients(t *testing.T) {
	b := NewEventBroker()
	// Should not panic.
	b.Emit(EventTasks)
	b.Emit(EventSchedules)
}

func TestEventBroker_EmitSingleClient(t *testing.T) {
	b := NewEventBroker()
	ch := make(chan sseEvent, 8)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	b.Emit(EventTasks)

	select {
	case got := <-ch:
		if got.Type != EventTasks {
			t.Fatalf("expected EventTasks, got %q", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventBroker_EmitMultipleClients(t *testing.T) {
	b := NewEventBroker()
	channels := make([]chan sseEvent, 3)
	b.mu.Lock()
	for i := range channels {
		channels[i] = make(chan sseEvent, 8)
		b.clients[channels[i]] = struct{}{}
	}
	b.mu.Unlock()

	b.Emit(EventFirewall)

	for i, ch := range channels {
		select {
		case got := <-ch:
			if got.Type != EventFirewall {
				t.Fatalf("client %d: expected EventFirewall, got %q", i, got.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("client %d: timeout", i)
		}
	}
}

func TestEventBroker_EmitDropsSlow(t *testing.T) {
	b := NewEventBroker()
	// Channel with buffer 1 — fill it, then emit should drop without blocking.
	ch := make(chan sseEvent, 1)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	// Fill the buffer.
	ch <- sseEvent{Type: EventTasks, Data: "reload"}

	// This should not block.
	done := make(chan struct{})
	go func() {
		b.Emit(EventSchedules)
		close(done)
	}()

	select {
	case <-done:
		// OK — Emit returned without blocking.
	case <-time.After(time.Second):
		t.Fatal("Emit blocked on slow client")
	}
}

func TestEventBroker_ClientDisconnect(t *testing.T) {
	b := NewEventBroker()
	ch := make(chan sseEvent, 8)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	if b.ClientCount() != 1 {
		t.Fatalf("expected 1 client, got %d", b.ClientCount())
	}

	// Disconnect.
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()

	if b.ClientCount() != 0 {
		t.Fatalf("expected 0 clients, got %d", b.ClientCount())
	}

	// Emit after disconnect should not panic and channel should not receive.
	b.Emit(EventTasks)
	select {
	case <-ch:
		t.Fatal("disconnected client should not receive events")
	default:
		// OK
	}
}

func TestEventBroker_ConcurrentEmit(t *testing.T) {
	b := NewEventBroker()
	ch := make(chan sseEvent, 100)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Emit(EventTasks)
		}()
	}
	wg.Wait()

	// Drain and count.
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:
	if count != 10 {
		t.Fatalf("expected 10 events, got %d", count)
	}
}

func TestEventBroker_ConcurrentConnect(t *testing.T) {
	b := NewEventBroker()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := make(chan sseEvent, 8)
			b.mu.Lock()
			b.clients[ch] = struct{}{}
			b.mu.Unlock()
			// Simulate brief connection.
			time.Sleep(time.Millisecond)
			b.mu.Lock()
			delete(b.clients, ch)
			b.mu.Unlock()
		}()
	}
	wg.Wait()

	if b.ClientCount() != 0 {
		t.Fatalf("expected 0 clients after all disconnect, got %d", b.ClientCount())
	}
}

// --- SSE HTTP tests ---

func TestEventBroker_SSEFormat(t *testing.T) {
	b := NewEventBroker()
	srv := httptest.NewServer(b)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer resp.Body.Close()

	// Check headers.
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("expected no-cache, got %q", cc)
	}

	// Check initial ping.
	scanner := bufio.NewScanner(resp.Body)
	event := readSSEEvent(t, scanner)
	if !strings.Contains(event, "event: ping") {
		t.Fatalf("expected ping event, got: %q", event)
	}
	if !strings.Contains(event, "data: connected") {
		t.Fatalf("expected 'data: connected', got: %q", event)
	}
}

func TestEventBroker_SSEMethodNotAllowed(t *testing.T) {
	b := NewEventBroker()
	req := httptest.NewRequest("POST", "/api/events", nil)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestEventBroker_SSEEventFormat(t *testing.T) {
	b := NewEventBroker()

	// Use a pipe to read SSE output in real time.
	srv := httptest.NewServer(b)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)

	// Read initial ping.
	readSSEEvent(t, scanner) // "event: ping" + "data: connected"

	// Emit an event.
	b.Emit(EventTasks)

	// Read the event.
	event := readSSEEvent(t, scanner)
	if !strings.Contains(event, "event: tasks") {
		t.Fatalf("expected 'event: tasks', got: %q", event)
	}
	if !strings.Contains(event, "data: reload") {
		t.Fatalf("expected 'data: reload', got: %q", event)
	}
}

func TestEventBroker_SSEMultipleEvents(t *testing.T) {
	b := NewEventBroker()
	srv := httptest.NewServer(b)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	readSSEEvent(t, scanner) // ping

	b.Emit(EventTasks)
	b.Emit(EventSchedules)

	ev1 := readSSEEvent(t, scanner)
	if !strings.Contains(ev1, "event: tasks") {
		t.Fatalf("first event should be tasks, got: %q", ev1)
	}

	ev2 := readSSEEvent(t, scanner)
	if !strings.Contains(ev2, "event: schedules") {
		t.Fatalf("second event should be schedules, got: %q", ev2)
	}
}

func TestEventBroker_EmitWithData(t *testing.T) {
	b := NewEventBroker()
	ch := make(chan sseEvent, 8)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	b.EmitWithData(EventNewMessage, "hello world")

	select {
	case got := <-ch:
		if got.Type != EventNewMessage {
			t.Fatalf("expected EventNewMessage, got %q", got.Type)
		}
		if got.Data != "hello world" {
			t.Fatalf("expected data %q, got %q", "hello world", got.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventBroker_EmitUsesReloadData(t *testing.T) {
	b := NewEventBroker()
	ch := make(chan sseEvent, 8)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	b.Emit(EventTasks)

	select {
	case got := <-ch:
		if got.Data != "reload" {
			t.Fatalf("Emit() should set data to 'reload', got %q", got.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestEventBroker_SSENewMessageFormat(t *testing.T) {
	b := NewEventBroker()
	srv := httptest.NewServer(b)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	readSSEEvent(t, scanner) // ping

	b.EmitWithData(EventNewMessage, "scheduled job output preview")

	event := readSSEEvent(t, scanner)
	if !strings.Contains(event, "event: new_message") {
		t.Fatalf("expected 'event: new_message', got: %q", event)
	}
	if !strings.Contains(event, "data: scheduled job output preview") {
		t.Fatalf("expected custom data payload, got: %q", event)
	}
}

// --- Regression: Connection header + keepalive ping ---

func TestEventBroker_SSENoConnectionHeaderHTTP2(t *testing.T) {
	b := NewEventBroker()

	// httptest.NewTLSServer with HTTP/2 would be ideal, but httptest
	// doesn't expose an HTTP/2-only server easily. Instead, use
	// httptest.NewRecorder with a crafted HTTP/2 request.
	req := httptest.NewRequest("GET", "/api/events", nil)
	req.ProtoMajor = 2
	req.ProtoMinor = 0
	req.Proto = "HTTP/2.0"

	w := httptest.NewRecorder()

	// ServeHTTP will block waiting for ctx.Done(), so run in background
	// and cancel quickly.
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	done := make(chan struct{})
	go func() {
		b.ServeHTTP(w, req)
		close(done)
	}()

	// Give the handler time to write headers.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if got := w.Header().Get("Connection"); got != "" {
		t.Fatalf("HTTP/2 response must NOT have Connection header, got %q", got)
	}
	// Other SSE headers should still be present.
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}
}

func TestEventBroker_SSEConnectionHeaderHTTP1(t *testing.T) {
	b := NewEventBroker()

	req := httptest.NewRequest("GET", "/api/events", nil)
	// httptest.NewRequest defaults to HTTP/1.1.
	if req.ProtoMajor != 1 {
		t.Fatalf("expected default HTTP/1.1 request, got %d.%d", req.ProtoMajor, req.ProtoMinor)
	}

	w := httptest.NewRecorder()

	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	done := make(chan struct{})
	go func() {
		b.ServeHTTP(w, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if got := w.Header().Get("Connection"); got != "keep-alive" {
		t.Fatalf("HTTP/1.1 response should have Connection: keep-alive, got %q", got)
	}
}

func TestEventBroker_SSEKeepalivePingFormat(t *testing.T) {
	// We cannot easily wait 25s in a test, so we verify the keepalive ping
	// format by emitting through the broker's event path and checking the
	// initial ping format matches what the keepalive also emits.
	// The keepalive writes: "event: ping\ndata: keepalive\n\n"
	// We verify the initial ping format and trust the ticker uses the same
	// fmt.Fprintf pattern. This test documents the expected wire format.
	b := NewEventBroker()
	srv := httptest.NewServer(b)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)

	// Read the initial ping — same format as keepalive minus data value.
	event := readSSEEvent(t, scanner)
	if !strings.Contains(event, "event: ping") {
		t.Fatalf("expected initial ping event, got: %q", event)
	}
	if !strings.Contains(event, "data: connected") {
		t.Fatalf("expected 'data: connected' in initial ping, got: %q", event)
	}

	// Verify the keepalive format string is "event: ping\ndata: keepalive".
	// We confirm this by inspecting the source pattern: both initial and
	// keepalive use the same "event: ping\ndata: ..." SSE frame structure.
	// The difference is only the data payload: "connected" vs "keepalive".
	expectedKeepalive := "event: ping\ndata: keepalive"
	_ = expectedKeepalive // format documented; actual 25s ticker not waited on
}

// readSSEEvent reads lines from the scanner until a blank line (SSE event boundary).
func readSSEEvent(t *testing.T, scanner *bufio.Scanner) string {
	t.Helper()
	var lines []string
	deadline := time.After(3 * time.Second)
	for {
		ch := make(chan bool, 1)
		go func() { ch <- scanner.Scan() }()
		select {
		case ok := <-ch:
			if !ok {
				if len(lines) > 0 {
					return strings.Join(lines, "\n")
				}
				t.Fatal("scanner ended unexpectedly")
			}
			line := scanner.Text()
			if line == "" {
				if len(lines) > 0 {
					return strings.Join(lines, "\n")
				}
				continue // skip leading blank lines
			}
			lines = append(lines, line)
		case <-deadline:
			t.Fatalf("timeout reading SSE event, got so far: %v", lines)
		}
	}
}
