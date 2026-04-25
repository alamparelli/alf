package handle

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
)

// stubBus implements EventPublisher + EventSubscriber for handle-level
// tests so we exercise the handle contract without pulling the
// internal/runtime/events package as a test dep.
type stubBus struct {
	published []handle_published
	queue     chan Event // single shared queue for tests that subscribe
}

type handle_published struct {
	From    capability.ID
	Topic   string
	Payload []byte
}

func (b *stubBus) Publish(from capability.ID, topic string, payload []byte, _ time.Time) error {
	b.published = append(b.published, handle_published{From: from, Topic: topic, Payload: payload})
	return nil
}

func (b *stubBus) Subscribe(_, _ capability.ID, _ string) (<-chan Event, func(), error) {
	if b.queue == nil {
		b.queue = make(chan Event, 4)
	}
	return b.queue, func() { close(b.queue); b.queue = nil }, nil
}

func TestEventPub_PublishOnExportedTopic(t *testing.T) {
	bus := &stubBus{}
	h := NewEventPub("cap-a", []string{"chat.log"}, bus)

	if err := h.Publish(context.Background(), "chat.log", []byte("hi")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(bus.published) != 1 || bus.published[0].Topic != "chat.log" || string(bus.published[0].Payload) != "hi" {
		t.Errorf("bus state %+v", bus.published)
	}
}

func TestEventPub_RejectsTopicNotExported(t *testing.T) {
	bus := &stubBus{}
	h := NewEventPub("cap-a", []string{"chat.log"}, bus)

	err := h.Publish(context.Background(), "secrets.dump", []byte("hi"))
	if !errors.Is(err, ErrTopicNotExported) {
		t.Fatalf("want ErrTopicNotExported, got %v", err)
	}
	if len(bus.published) != 0 {
		t.Errorf("bus should not have received publish, got %+v", bus.published)
	}
}

func TestEventPub_RevokedAfterClose(t *testing.T) {
	bus := &stubBus{}
	inst := NewInstance(context.Background(), "cap-a", Grants{
		EventPub: NewEventPub("cap-a", []string{"chat.log"}, bus),
	})
	inst.Close()
	err := inst.EventPub.Publish(context.Background(), "chat.log", []byte("hi"))
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
}

func TestEventPub_NonSerializable(t *testing.T) {
	h := NewEventPub("cap-a", []string{"chat.log"}, nil)
	if _, err := json.Marshal(h); err == nil {
		t.Fatal("EventPub must not be JSON-serializable")
	}
}

func TestEventSub_ReceiveDelivers(t *testing.T) {
	q := make(chan Event, 1)
	sub := NewEventSub("cap-b", "cap-a", "chat.log", q, func() { close(q) })
	q <- Event{From: "cap-a", Topic: "chat.log", Payload: []byte("hi")}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ev, err := sub.Receive(ctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if string(ev.Payload) != "hi" {
		t.Errorf("payload=%q", ev.Payload)
	}
}

func TestEventSub_ReceiveBlocksUntilCtxCancel(t *testing.T) {
	q := make(chan Event)
	sub := NewEventSub("cap-b", "cap-a", "chat.log", q, func() {})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := sub.Receive(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
}

func TestEventSub_RevokedAfterClose(t *testing.T) {
	q := make(chan Event)
	cleanupCalled := false
	sub := NewEventSub("cap-b", "cap-a", "chat.log", q, func() {
		cleanupCalled = true
		close(q)
	})
	inst := NewInstance(context.Background(), "cap-b", Grants{
		EventSubs: []*EventSub{sub},
	})
	inst.Close()
	if !cleanupCalled {
		t.Error("cleanup func should have been called by Instance.Close")
	}
	_, err := sub.Receive(context.Background())
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
}

func TestEventSub_NonSerializable(t *testing.T) {
	q := make(chan Event)
	sub := NewEventSub("cap-b", "cap-a", "chat.log", q, func() {})
	if _, err := json.Marshal(sub); err == nil {
		t.Fatal("EventSub must not be JSON-serializable")
	}
}
