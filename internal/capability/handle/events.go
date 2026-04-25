package handle

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/alamparelli/alf/internal/capability"
)

// Event is a single message delivered through the cross-capability bus.
// Carries the publisher identity (for audit + sub-side routing), the
// topic, and the payload bytes. Timestamp is set by the bus at publish
// time so subscribers always see a monotonic-ish view.
type Event struct {
	From      capability.ID
	Topic     string
	Payload   []byte
	Timestamp time.Time
}

// ErrTopicNotExported is returned by EventPub.Publish when the topic
// does not match the handle's exported scope. Mirrors the §3.3 promise
// that a publisher can only publish on topics its manifest declared.
var ErrTopicNotExported = errors.New("handle: topic not in events.exports scope")

// EventPublisher is the narrow surface EventPub uses to reach the bus.
// The production implementation is internal/runtime/events.Bus, wrapped
// at forge time. Tests can supply an in-memory stub.
type EventPublisher interface {
	Publish(from capability.ID, topic string, payload []byte, ts time.Time) error
}

// EventSubscriber is the narrow surface EventSub uses to reach the bus.
// Subscribe registers a queue at forge time; the returned cleanup func
// runs when the handle is revoked, closing the queue and unregistering
// it from bus routing.
type EventSubscriber interface {
	Subscribe(subscriber capability.ID, from capability.ID, topic string) (<-chan Event, func(), error)
}

// EventPub grants publish access on one or more topics that the
// capability's signed manifest declared in [[events.exports]]. Forged
// only by Runtime.Instantiate after envelope.Verify confirmed the
// declarations and the cross-flow loader registered the topics.
//
// One EventPub per capability covers all its exported topics — keeps
// the API surface small. Topic filtering happens at Publish time
// against the baked-in `topics` slice.
type EventPub struct {
	_ [0]noSerialize

	owner        capability.ID
	topics       map[string]struct{}
	bus          EventPublisher
	lifecycleCtx context.Context
	revoked      atomic.Bool
}

// NewEventPub constructs a publisher handle scoped to the given topics.
// An empty topics slice means "no publish authority" — the handle
// rejects every Publish call. Exported for the runtime forge; tests
// can construct directly.
func NewEventPub(owner capability.ID, topics []string, bus EventPublisher) *EventPub {
	set := make(map[string]struct{}, len(topics))
	for _, t := range topics {
		if t != "" {
			set[t] = struct{}{}
		}
	}
	return &EventPub{owner: owner, topics: set, bus: bus}
}

// Publish sends an event on topic to every subscriber the bus has
// routed for (owner, topic). Returns ErrRevoked if the handle was
// revoked, ErrTopicNotExported if topic is outside the manifest's
// declared exports, or whatever the bus surfaces.
func (h *EventPub) Publish(ctx context.Context, topic string, payload []byte) error {
	if h.revoked.Load() {
		return ErrRevoked
	}
	if _, ok := h.topics[topic]; !ok {
		return ErrTopicNotExported
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.lifecycleCtx != nil {
		if err := h.lifecycleCtx.Err(); err != nil {
			return ErrRevoked
		}
	}
	if h.bus == nil {
		return nil
	}
	return h.bus.Publish(h.owner, topic, payload, time.Now())
}

// Owner returns the capability ID this handle was forged for.
func (h *EventPub) Owner() capability.ID { return h.owner }

// Topics returns a sorted view of the topics this publisher is
// authorised to publish on. Useful for boot-time logging + audit
// snapshots; not a security gate (Publish enforces the same set).
func (h *EventPub) Topics() []string {
	out := make([]string, 0, len(h.topics))
	for t := range h.topics {
		out = append(out, t)
	}
	return out
}

// MarshalJSON implements §4.2 invariant 1.
func (h *EventPub) MarshalJSON() ([]byte, error) {
	return nil, ErrHandleNonSerializable
}

// attachLifecycle binds the handle to the Instance lifecycle context.
func (h *EventPub) attachLifecycle(ctx context.Context) { h.lifecycleCtx = ctx }

// EventSub grants receive access for events from a single named
// publisher on a single topic. Per §3.3, every cross-flow is one
// (from, topic) pair; a capability that wants to subscribe to two
// topics from the same publisher gets two EventSub handles.
//
// Forged only when the cross-flow loader confirmed: (a) the named
// publisher is installed, (b) its signed manifest declares topic in
// events.exports, (c) this capability's signed manifest declares
// (from, topic) in events.subscribes. Without all three, no handle
// is forged — the subscriber has no way to reach the bus.
type EventSub struct {
	_ [0]noSerialize

	owner        capability.ID
	from         capability.ID
	topic        string
	queue        <-chan Event
	cleanup      func()
	lifecycleCtx context.Context
	revoked      atomic.Bool
	closeOnce    atomicOnce
}

// NewEventSub constructs a subscriber handle. Caller passes the queue
// + cleanup func returned by the bus's Subscribe. The cleanup func
// runs on revocation; it must be idempotent.
func NewEventSub(owner, from capability.ID, topic string, queue <-chan Event, cleanup func()) *EventSub {
	return &EventSub{
		owner:   owner,
		from:    from,
		topic:   topic,
		queue:   queue,
		cleanup: cleanup,
	}
}

// Receive blocks until an event is available, the ctx is cancelled,
// or the handle is revoked. Returns ErrRevoked after revocation.
//
// Receive on a revoked handle returns immediately. Receive on a
// closed bus queue (publisher uninstalled mid-flight) returns the
// zero Event with ErrRevoked — the bus closes the channel on cleanup.
func (h *EventSub) Receive(ctx context.Context) (Event, error) {
	if h.revoked.Load() {
		return Event{}, ErrRevoked
	}
	if h.queue == nil {
		return Event{}, ErrRevoked
	}
	var lcDone <-chan struct{}
	if h.lifecycleCtx != nil {
		lcDone = h.lifecycleCtx.Done()
	}
	select {
	case ev, ok := <-h.queue:
		if !ok {
			return Event{}, ErrRevoked
		}
		return ev, nil
	case <-ctx.Done():
		return Event{}, ctx.Err()
	case <-lcDone:
		return Event{}, ErrRevoked
	}
}

// Owner returns the capability ID this handle was forged for.
func (h *EventSub) Owner() capability.ID { return h.owner }

// From returns the publisher capability ID this handle is bound to.
func (h *EventSub) From() capability.ID { return h.from }

// Topic returns the topic this handle is bound to.
func (h *EventSub) Topic() string { return h.topic }

// MarshalJSON implements §4.2 invariant 1.
func (h *EventSub) MarshalJSON() ([]byte, error) {
	return nil, ErrHandleNonSerializable
}

// attachLifecycle binds the handle to the Instance lifecycle context.
func (h *EventSub) attachLifecycle(ctx context.Context) { h.lifecycleCtx = ctx }

// revoke marks the handle as revoked and runs the bus cleanup. Called
// by Instance.Close as part of the cascade.
func (h *EventSub) revoke() {
	h.closeOnce.Do(func() {
		h.revoked.Store(true)
		if h.cleanup != nil {
			h.cleanup()
		}
	})
}

// atomicOnce is a sync.Once equivalent built on atomic.Bool to avoid
// the sync import in this file (handle package keeps imports tight).
// Behaviour: Do runs f at most once across concurrent callers.
type atomicOnce struct {
	done atomic.Bool
}

func (o *atomicOnce) Do(f func()) {
	if o.done.CompareAndSwap(false, true) {
		f()
	}
}
