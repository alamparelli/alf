package controlcenter

import (
	"bufio"
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
	ch := make(chan EventType, 8)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	b.Emit(EventTasks)

	select {
	case got := <-ch:
		if got != EventTasks {
			t.Fatalf("expected EventTasks, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventBroker_EmitMultipleClients(t *testing.T) {
	b := NewEventBroker()
	channels := make([]chan EventType, 3)
	b.mu.Lock()
	for i := range channels {
		channels[i] = make(chan EventType, 8)
		b.clients[channels[i]] = struct{}{}
	}
	b.mu.Unlock()

	b.Emit(EventFirewall)

	for i, ch := range channels {
		select {
		case got := <-ch:
			if got != EventFirewall {
				t.Fatalf("client %d: expected EventFirewall, got %q", i, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("client %d: timeout", i)
		}
	}
}

func TestEventBroker_EmitDropsSlow(t *testing.T) {
	b := NewEventBroker()
	// Channel with buffer 1 — fill it, then emit should drop without blocking.
	ch := make(chan EventType, 1)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	// Fill the buffer.
	ch <- EventTasks

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
	ch := make(chan EventType, 8)
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
	ch := make(chan EventType, 100)
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
			ch := make(chan EventType, 8)
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
