package controlcenter

import (
	"context"
	"sync"
)

// chatJob tracks an in-flight chat request that runs independently of HTTP connections.
// Events are buffered so clients can disconnect and reconnect without losing data.
type chatJob struct {
	ID     string
	cancel context.CancelFunc

	mu     sync.Mutex
	events []ChatEvent
	done   bool
	err    error
	notify chan struct{} // replaced on each push; closed to wake readers
}

func newChatJob(cancel context.CancelFunc) *chatJob {
	return &chatJob{
		ID:     NewMessageID(),
		cancel: cancel,
		notify: make(chan struct{}),
	}
}

func (j *chatJob) push(evt ChatEvent) {
	j.mu.Lock()
	j.events = append(j.events, evt)
	ch := j.notify
	j.notify = make(chan struct{})
	j.mu.Unlock()
	close(ch)
}

func (j *chatJob) finish(err error) {
	j.mu.Lock()
	j.done = true
	j.err = err
	ch := j.notify
	j.mu.Unlock()
	close(ch)
}

func (j *chatJob) isDone() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done
}

func (j *chatJob) eventCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.events)
}

// snapshot returns events from offset, completion status, and a channel to wait for more.
func (j *chatJob) snapshot(offset int) (events []ChatEvent, isDone bool, jobErr error, wait <-chan struct{}) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if offset < len(j.events) {
		events = make([]ChatEvent, len(j.events)-offset)
		copy(events, j.events[offset:])
	}
	return events, j.done, j.err, j.notify
}
