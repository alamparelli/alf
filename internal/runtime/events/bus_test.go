package events

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability/handle"
)

func TestBus_PublishWithoutSubscribers(t *testing.T) {
	b := New()
	if err := b.Publish("cap-a", "chat.log", []byte("hi"), time.Now()); err != nil {
		t.Errorf("publish to no subscribers should be a no-op, got %v", err)
	}
}

func TestBus_RoundTrip(t *testing.T) {
	b := New()
	q, cleanup, err := b.Subscribe("cap-b", "cap-a", "chat.log")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := b.Publish("cap-a", "chat.log", []byte("hi"), time.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-q:
		if ev.From != "cap-a" || ev.Topic != "chat.log" || string(ev.Payload) != "hi" {
			t.Errorf("event=%+v", ev)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("event not delivered within 200ms")
	}
}

func TestBus_PrivateByDefault_NoSubscribeForUndeclaredFlow(t *testing.T) {
	b := New()
	// cap-c subscribes to cap-a's chat.log — but no one publishes ON
	// behalf of cap-c, so cap-c should get nothing from a publish that
	// originates from cap-d (different publisher).
	q, cleanup, err := b.Subscribe("cap-c", "cap-a", "chat.log")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := b.Publish("cap-d", "chat.log", []byte("hi"), time.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-q:
		t.Errorf("subscriber should not receive events from a different publisher, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// Expected
	}
}

func TestBus_MultipleSubscribersFanOut(t *testing.T) {
	b := New()
	q1, c1, _ := b.Subscribe("cap-b", "cap-a", "chat.log")
	q2, c2, _ := b.Subscribe("cap-c", "cap-a", "chat.log")
	defer c1()
	defer c2()

	if err := b.Publish("cap-a", "chat.log", []byte("hi"), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, q := range []<-chan handle.Event{q1, q2} {
		select {
		case ev := <-q:
			if string(ev.Payload) != "hi" {
				t.Errorf("payload=%q", ev.Payload)
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("missed delivery")
		}
	}
}

func TestBus_CleanupRemovesSubscription(t *testing.T) {
	b := New()
	_, cleanup, _ := b.Subscribe("cap-b", "cap-a", "chat.log")
	if got := b.SubscriberCount("cap-a", "chat.log"); got != 1 {
		t.Fatalf("count=%d, want 1", got)
	}
	cleanup()
	if got := b.SubscriberCount("cap-a", "chat.log"); got != 0 {
		t.Errorf("count after cleanup=%d, want 0", got)
	}
	// Idempotent cleanup
	cleanup()
}

func TestBus_SlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	b := New()
	// We don't drain the queue. Fill it to capacity + 1 to trigger drop.
	q, cleanup, _ := b.Subscribe("cap-b", "cap-a", "chat.log")
	defer cleanup()
	_ = q // intentionally unread

	// Fill the buffer.
	for i := 0; i < defaultQueueSize; i++ {
		if err := b.Publish("cap-a", "chat.log", []byte{byte(i)}, time.Now()); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	// One more — should be dropped, not blocked.
	done := make(chan error, 1)
	go func() {
		done <- b.Publish("cap-a", "chat.log", []byte("overflow"), time.Now())
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSlowSubscriber) {
			t.Errorf("want ErrSlowSubscriber on overflow, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("publisher blocked on slow subscriber")
	}
}

func TestBus_EmptyTopicRejected(t *testing.T) {
	b := New()
	if err := b.Publish("cap-a", "", []byte("hi"), time.Now()); err == nil {
		t.Error("publish with empty topic should fail")
	}
	if _, _, err := b.Subscribe("cap-b", "cap-a", ""); err == nil {
		t.Error("subscribe with empty topic should fail")
	}
}

func TestBus_RevocationClosesQueue(t *testing.T) {
	b := New()
	q, cleanup, _ := b.Subscribe("cap-b", "cap-a", "chat.log")
	cleanup()
	// Reading from a closed channel returns the zero value with ok=false.
	if ev, ok := <-q; ok {
		t.Errorf("queue should be closed after cleanup, got %+v", ev)
	}
}

// TestBus_PublishCleanupRace pins SEC-003: a publisher snapshotting
// the route slice could send on a queue that cleanup had just closed,
// triggering "send on closed channel" panic and crashing the daemon.
// The fix holds the bus RLock across fan-out so cleanup (which acquires
// the write Lock) is strictly serialised after any in-flight Publish.
//
// This test must be run under -race to maximise scheduling pressure.
// Without the fix, it panics within milliseconds on every modern CPU.
func TestBus_PublishCleanupRace(t *testing.T) {
	const (
		subs       = 32
		duration   = 200 * time.Millisecond
		publishers = 4
	)
	b := New()

	// Spawn N subscribers. Each one repeatedly cleans up + re-subscribes
	// to keep the route table churning while publishers fire.
	stop := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < subs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, cleanup, err := b.Subscribe("cap-b", "cap-a", "chat.log")
				if err != nil {
					t.Errorf("subscribe: %v", err)
					return
				}
				cleanup()
			}
		}()
	}

	// Publishers fire as fast as they can. If the race is reachable,
	// one of them panics on a closed channel send.
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = b.Publish("cap-a", "chat.log", []byte("ping"), time.Now())
			}
		}()
	}

	time.Sleep(duration)
	close(stop)
	wg.Wait()
}
