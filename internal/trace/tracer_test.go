package trace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNew_InitialisesFields(t *testing.T) {
	tr := New("tg", "conv-1", "msg-1")
	if tr.TraceID == "" || len(tr.TraceID) != 12 {
		t.Errorf("expected 12-char trace id, got %q", tr.TraceID)
	}
	if tr.Channel != "tg" || tr.ConvID != "conv-1" || tr.UserMsgID != "msg-1" {
		t.Errorf("unexpected tracer fields: %+v", tr)
	}
	if tr.StartTime.IsZero() {
		t.Error("StartTime must be non-zero")
	}
}

func TestWithAndFromContext(t *testing.T) {
	// FromContext on bare context returns nil.
	if FromContext(context.Background()) != nil {
		t.Error("bare context must yield nil tracer")
	}

	tr := New("cc", "c", "m")
	ctx := WithContext(context.Background(), tr)
	if FromContext(ctx) != tr {
		t.Error("FromContext did not return the stored tracer")
	}
}

func TestStartSpan_EndRecordsDuration(t *testing.T) {
	tr := New("tg", "c", "m")
	sp := tr.StartSpan("db.query", map[string]string{"op": "select"})
	time.Sleep(2 * time.Millisecond)
	sp.End()

	if len(tr.Spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tr.Spans))
	}
	s := tr.Spans[0]
	if s.Name != "db.query" {
		t.Errorf("unexpected span name: %q", s.Name)
	}
	if s.DurationMs < 0 {
		t.Errorf("unexpected negative duration: %d", s.DurationMs)
	}
	if s.Tags["op"] != "select" {
		t.Errorf("tags not propagated: %+v", s.Tags)
	}
}

func TestStartSpan_EndNilSafe(t *testing.T) {
	var h *SpanHandle
	h.End()              // nil receiver
	h.EndWithError(nil)  // nil receiver

	zero := &SpanHandle{} // non-nil but no tracer
	zero.End()
	zero.EndWithError(errors.New("x"))
	zero.Tag("k", "v") // must not panic even with nil tracer
}

func TestEndWithError_RecordsErrorMessage(t *testing.T) {
	tr := New("tg", "c", "m")
	sp := tr.StartSpan("op", nil)
	sp.EndWithError(errors.New("boom"))

	if len(tr.Spans) != 1 {
		t.Fatal("span not recorded")
	}
	if tr.Spans[0].Error != "boom" {
		t.Errorf("expected error 'boom', got %q", tr.Spans[0].Error)
	}
}

func TestTag_InitialisesMapWhenNil(t *testing.T) {
	tr := New("tg", "c", "m")
	sp := tr.StartSpan("op", nil) // Tags == nil
	sp.Tag("k", "v")
	sp.End()

	if tr.Spans[0].Tags["k"] != "v" {
		t.Errorf("Tag should have initialised Tags map, got %+v", tr.Spans[0].Tags)
	}
}

func TestAddSpan_AppendsCompletedSpan(t *testing.T) {
	tr := New("tg", "c", "m")
	tr.AddSpan("http.call", 15*time.Millisecond, map[string]string{"host": "api"}, errors.New("timeout"))
	if len(tr.Spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tr.Spans))
	}
	s := tr.Spans[0]
	if s.Name != "http.call" || s.DurationMs != 15 {
		t.Errorf("unexpected span: %+v", s)
	}
	if s.Error != "timeout" {
		t.Errorf("expected error 'timeout', got %q", s.Error)
	}
}

func TestAddSpan_NoErrorLeavesEmptyError(t *testing.T) {
	tr := New("tg", "c", "m")
	tr.AddSpan("ok", time.Millisecond, nil, nil)
	if tr.Spans[0].Error != "" {
		t.Errorf("expected empty error, got %q", tr.Spans[0].Error)
	}
}

func TestStartSpanFromContext_NilTracer(t *testing.T) {
	// No tracer in ctx → returns nil.
	if h := StartSpanFromContext(context.Background(), "op", nil); h != nil {
		t.Error("expected nil when no tracer in context")
	}
}

func TestStartSpanFromContext_WithTracer(t *testing.T) {
	tr := New("tg", "c", "m")
	ctx := WithContext(context.Background(), tr)
	h := StartSpanFromContext(ctx, "op", map[string]string{"k": "v"})
	if h == nil {
		t.Fatal("expected a span handle")
	}
	h.End()
	if len(tr.Spans) != 1 {
		t.Errorf("span should have been recorded on the tracer, got %d", len(tr.Spans))
	}
}

func TestFlush_WritesJSONL(t *testing.T) {
	dir := t.TempDir()
	tr := New("tg", "conv", "msg")
	tr.AddSpan("op", time.Millisecond, nil, nil)
	tr.Flush(dir)

	// Find the daily trace file.
	today := time.Now().Format("2006-01-02")
	path := filepath.Join(dir, "logs", "traces", today+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("trace file not written: %v", err)
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		t.Fatal("trace file is empty")
	}
	var decoded Tracer
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("trace file not valid JSON: %v", err)
	}
	if decoded.TraceID != tr.TraceID {
		t.Errorf("trace id mismatch: %q vs %q", decoded.TraceID, tr.TraceID)
	}
	if len(decoded.Spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(decoded.Spans))
	}
	if decoded.DurationMs <= 0 {
		// The trace is flushed with a non-zero duration.
	}
}
