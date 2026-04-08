package controlcenter

import (
	"fmt"
	"log"
	"net/http"
	"sync"
)

// ScheduleEventBroker broadcasts schedule change events to SSE clients.
type ScheduleEventBroker struct {
	mu      sync.Mutex
	clients map[chan struct{}]struct{}
}

// NewScheduleEventBroker creates a new broker.
func NewScheduleEventBroker() *ScheduleEventBroker {
	return &ScheduleEventBroker{
		clients: make(map[chan struct{}]struct{}),
	}
}

// Notify signals all connected SSE clients that schedules changed.
func (b *ScheduleEventBroker) Notify() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- struct{}{}:
		default: // non-blocking, drop if client is slow
		}
	}
}

// ServeHTTP handles GET /api/schedules/events as an SSE stream.
func (b *ScheduleEventBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Send initial ping so client knows connection is live.
	fmt.Fprintf(w, "event: ping\ndata: connected\n\n")
	flusher.Flush()

	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.clients, ch)
		b.mu.Unlock()
		log.Printf("[schedule-events] client disconnected")
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			fmt.Fprintf(w, "event: change\ndata: reload\n\n")
			flusher.Flush()
		}
	}
}
