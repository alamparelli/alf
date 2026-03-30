package controlcenter

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// EventType identifies what changed. Maps directly to SSE "event:" field.
type EventType string

const (
	EventSchedules  EventType = "schedules"
	EventTasks      EventType = "tasks"
	EventFirewall   EventType = "firewall"
	EventApps       EventType = "apps"
	EventMarketplace EventType = "marketplace"
	EventVault      EventType = "vault"
	EventConfig     EventType = "config"
	EventTiers      EventType = "tiers"
	EventTools      EventType = "tools"
	EventSkills     EventType = "skills"
	EventAgents     EventType = "agents"
	EventNewMessage EventType = "new_message"
)

// sseEvent is an internal message carrying type + optional data payload.
type sseEvent struct {
	Type EventType
	Data string // "reload" if empty
}

// EventBroker broadcasts typed events to all connected SSE clients.
// Replaces ScheduleEventBroker with a multiplexed single-endpoint design.
type EventBroker struct {
	mu      sync.Mutex
	clients map[chan sseEvent]struct{}
}

// NewEventBroker creates a new broker.
func NewEventBroker() *EventBroker {
	return &EventBroker{
		clients: make(map[chan sseEvent]struct{}),
	}
}

// Emit sends a typed event to all connected SSE clients. Non-blocking per client.
func (b *EventBroker) Emit(event EventType) {
	b.EmitWithData(event, "reload")
}

// EmitWithData sends a typed event with a custom data payload.
func (b *EventBroker) EmitWithData(event EventType, data string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	msg := sseEvent{Type: event, Data: data}
	for ch := range b.clients {
		select {
		case ch <- msg:
		default: // drop if client is slow
		}
	}
}

// ClientCount returns the number of connected SSE clients (for testing/metrics).
func (b *EventBroker) ClientCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}

// ServeHTTP handles GET /api/events as a multiplexed SSE stream.
func (b *EventBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	// Connection: keep-alive is HTTP/1.1-only and forbidden in HTTP/2.
	// Only set it when the request is HTTP/1.x to avoid ERR_HTTP2_PROTOCOL_ERROR.
	if !r.ProtoAtLeast(2, 0) {
		w.Header().Set("Connection", "keep-alive")
	}
	w.WriteHeader(http.StatusOK)

	// Initial ping so client knows connection is live.
	fmt.Fprintf(w, "event: ping\ndata: connected\n\n")
	flusher.Flush()

	ch := make(chan sseEvent, 8)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.clients, ch)
		b.mu.Unlock()
		log.Printf("[events] SSE client disconnected (%d remaining)", b.ClientCount())
	}()

	ctx := r.Context()
	// Periodic keepalive to prevent reverse proxies from closing idle connections.
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.Type, msg.Data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, "event: ping\ndata: keepalive\n\n")
			flusher.Flush()
		}
	}
}
