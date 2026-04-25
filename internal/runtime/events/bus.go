// Package events implements the in-memory cross-capability bus that
// backs §3.3 of docs/ARCHITECTURE-SECURITY.md (events private-by-default).
// The package exports a Bus type that satisfies handle.EventPublisher
// and handle.EventSubscriber; it is the only consumer of the handle
// package's narrow interfaces.
//
// One Bus per daemon. Routes are keyed by (publisher, topic). A queue
// is registered when a subscriber handle is forged and removed when
// the handle is revoked. Bounded queues + non-blocking publish keep a
// slow subscriber from stalling the publisher.
package events

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// defaultQueueSize is the buffered-channel depth assigned to every
// subscriber queue. Picked to absorb short bursts without blocking
// publishers; configurable per-cap quota lands in a follow-up.
const defaultQueueSize = 1024

// ErrSlowSubscriber is returned by Publish when a subscriber's queue
// is full. The publisher is not blocked; the event is dropped for
// that subscriber and surfaced via the audit log (when #396 lands).
// Other subscribers on the same topic still receive the event.
var ErrSlowSubscriber = errors.New("events: subscriber queue full, event dropped")

// routeKey identifies a publisher-topic pair. Used as a map key so
// the bus can fan out to all subscribers of (publisher, topic) in
// O(N) where N is the number of subscribers for that route.
type routeKey struct {
	publisher capability.ID
	topic     string
}

// subscription holds one subscriber's queue + its identity. The
// bus keeps a slice per routeKey so a single publisher-topic can
// fan out to multiple subscribers (covers the legitimate case where
// two consumer caps subscribe to the same source).
type subscription struct {
	subscriber capability.ID
	queue      chan handle.Event
}

// Bus is the in-memory event router. Construct via New. Safe for
// concurrent use; routes are protected by an RWMutex (subscribe and
// revoke take a write lock; publish takes a read lock).
type Bus struct {
	mu     sync.RWMutex
	routes map[routeKey][]*subscription
}

// New constructs an empty bus.
func New() *Bus {
	return &Bus{routes: make(map[routeKey][]*subscription)}
}

// Publish implements handle.EventPublisher. Sends ev to every
// subscription registered for (from, topic). Drops events for
// subscribers whose queue is full and accumulates a single
// ErrSlowSubscriber error mentioning every dropped target. Other
// subscribers still receive normally.
//
// Non-blocking by design: a publisher must never wait on a slow
// consumer. The audit + rate-limit layer (post-MVP) hooks here.
func (b *Bus) Publish(from capability.ID, topic string, payload []byte, ts time.Time) error {
	if topic == "" {
		return fmt.Errorf("events: empty topic")
	}
	b.mu.RLock()
	subs := append([]*subscription(nil), b.routes[routeKey{publisher: from, topic: topic}]...)
	b.mu.RUnlock()

	if len(subs) == 0 {
		// No subscriber routed for this (from, topic). Per §3.3
		// "private-by-default", this is the normal case for a
		// publisher with no declared cross-flow consumers.
		return nil
	}

	ev := handle.Event{From: from, Topic: topic, Payload: payload, Timestamp: ts}
	var dropped []capability.ID
	for _, s := range subs {
		select {
		case s.queue <- ev:
		default:
			dropped = append(dropped, s.subscriber)
		}
	}
	if len(dropped) > 0 {
		return fmt.Errorf("%w: dropped for %v", ErrSlowSubscriber, dropped)
	}
	return nil
}

// Subscribe implements handle.EventSubscriber. Registers a queue for
// (from, topic) on behalf of subscriber, returns the receive-only
// channel and a cleanup func. The cleanup unregisters and closes the
// queue; it is idempotent so the handle revoke path can call it
// without coordinating with the bus.
func (b *Bus) Subscribe(subscriber capability.ID, from capability.ID, topic string) (<-chan handle.Event, func(), error) {
	if topic == "" {
		return nil, nil, fmt.Errorf("events: empty topic")
	}
	q := make(chan handle.Event, defaultQueueSize)
	sub := &subscription{subscriber: subscriber, queue: q}
	key := routeKey{publisher: from, topic: topic}

	b.mu.Lock()
	b.routes[key] = append(b.routes[key], sub)
	b.mu.Unlock()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			list := b.routes[key]
			out := list[:0]
			for _, s := range list {
				if s == sub {
					continue
				}
				out = append(out, s)
			}
			if len(out) == 0 {
				delete(b.routes, key)
			} else {
				b.routes[key] = out
			}
			close(q)
		})
	}
	return q, cleanup, nil
}

// SubscriberCount returns the number of active subscriptions for a
// (publisher, topic) pair. Exported for tests + the snapshot writer.
func (b *Bus) SubscriberCount(publisher capability.ID, topic string) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.routes[routeKey{publisher: publisher, topic: topic}])
}
