package controlcenter

import (
	"fmt"
	"log"
	"net/http"
	"sync"
)

// EventType identifies what changed. Maps directly to SSE "event:" field.
type EventType string

const (
	EventSchedules   EventType = "schedules"
	EventTasks       EventType = "tasks"
	EventFirewall    EventType = "firewall"
	EventApps        EventType = "apps"
	EventMarketplace EventType = "marketplace"
	EventVault       EventType = "vault"
	EventConfig      EventType = "config"
	EventTiers       EventType = "tiers"
	EventTools       EventType = "tools"
	EventSkills      EventType = "skills"
	EventAgents      EventType = "agents"
)

// EventBroker broadcasts typed events to all connected SSE clients.
// Replaces ScheduleEventBroker with a multiplexed single-endpoint design.
type EventBroker struct {
	mu      sync.Mutex
	clients map[chan EventType]struct{}
}

// NewEventBroker creates a new broker.
func NewEventBroker() *EventBroker {
	return &EventBroker{
		clients: make(map[chan EventType]struct{}),
	}
}

// Emit sends a typed event to all connected SSE clients. Non-blocking per client.
func (b *EventBroker) Emit(event EventType) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- event:
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
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Initial ping so client knows connection is live.
	fmt.Fprintf(w, "event: ping\ndata: connected\n\n")
	flusher.Flush()

	ch := make(chan EventType, 8)
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
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: reload\n\n", event)
			flusher.Flush()
		}
	}
}
