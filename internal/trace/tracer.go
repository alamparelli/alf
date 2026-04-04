package trace

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type contextKey struct{}

// Tracer collects spans for a single request (user message → response).
type Tracer struct {
	TraceID   string `json:"trace_id"`
	Channel   string `json:"channel"`
	ConvID    string `json:"conv_id"`
	UserMsgID string `json:"user_msg_id"`
	StartTime time.Time `json:"start"`
	DurationMs int64  `json:"duration_ms"`
	Spans     []Span `json:"spans"`

	mu sync.Mutex
}

// Span is a timed operation within a trace.
type Span struct {
	Name       string            `json:"name"`
	Start      time.Time         `json:"start"`
	DurationMs int64             `json:"duration_ms"`
	Tags       map[string]string `json:"tags,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// SpanHandle is returned by StartSpan and used to end the span.
type SpanHandle struct {
	tracer *Tracer
	span   Span
}

// New creates a new Tracer for a request.
func New(channel, convID, userMsgID string) *Tracer {
	return &Tracer{
		TraceID:   generateID(),
		Channel:   channel,
		ConvID:    convID,
		UserMsgID: userMsgID,
		StartTime: time.Now(),
	}
}

// WithContext injects the tracer into a context.
func WithContext(ctx context.Context, t *Tracer) context.Context {
	return context.WithValue(ctx, contextKey{}, t)
}

// FromContext extracts the tracer from a context. Returns nil if none.
func FromContext(ctx context.Context) *Tracer {
	t, _ := ctx.Value(contextKey{}).(*Tracer)
	return t
}

// StartSpan begins a new span. Call End() on the returned handle when done.
func (t *Tracer) StartSpan(name string, tags map[string]string) *SpanHandle {
	return &SpanHandle{
		tracer: t,
		span: Span{
			Name:  name,
			Start: time.Now(),
			Tags:  tags,
		},
	}
}

// End completes a span and adds it to the trace.
func (h *SpanHandle) End() {
	if h == nil || h.tracer == nil {
		return
	}
	h.span.DurationMs = time.Since(h.span.Start).Milliseconds()
	h.tracer.mu.Lock()
	h.tracer.Spans = append(h.tracer.Spans, h.span)
	h.tracer.mu.Unlock()
}

// EndWithError completes a span with an error.
func (h *SpanHandle) EndWithError(err error) {
	if h == nil || h.tracer == nil {
		return
	}
	if err != nil {
		h.span.Error = err.Error()
	}
	h.End()
}

// Tag adds a tag to an in-progress span.
func (h *SpanHandle) Tag(key, value string) {
	if h == nil {
		return
	}
	if h.span.Tags == nil {
		h.span.Tags = make(map[string]string)
	}
	h.span.Tags[key] = value
}

// AddSpan adds a completed span directly (for cases where start/end wrapping isn't convenient).
func (t *Tracer) AddSpan(name string, duration time.Duration, tags map[string]string, err error) {
	s := Span{
		Name:       name,
		Start:      time.Now().Add(-duration),
		DurationMs: duration.Milliseconds(),
		Tags:       tags,
	}
	if err != nil {
		s.Error = err.Error()
	}
	t.mu.Lock()
	t.Spans = append(t.Spans, s)
	t.mu.Unlock()
}

// Flush writes the trace to a daily JSONL file.
func (t *Tracer) Flush(dataDir string) {
	t.DurationMs = time.Since(t.StartTime).Milliseconds()

	dir := filepath.Join(dataDir, "logs", "traces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[trace] mkdir %s: %v", dir, err)
		return
	}

	filename := filepath.Join(dir, time.Now().Format("2006-01-02")+".jsonl")
	data, err := json.Marshal(t)
	if err != nil {
		log.Printf("[trace] marshal: %v", err)
		return
	}

	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[trace] open %s: %v", filename, err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", data)
	log.Printf("[trace] flushed %s (%d spans, %dms)", t.TraceID, len(t.Spans), t.DurationMs)
}

// StartSpanFromContext is a convenience to start a span if a tracer exists in ctx.
func StartSpanFromContext(ctx context.Context, name string, tags map[string]string) *SpanHandle {
	t := FromContext(ctx)
	if t == nil {
		return nil
	}
	return t.StartSpan(name, tags)
}

func generateID() string {
	const chars = "0123456789abcdef"
	b := make([]byte, 12)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}
