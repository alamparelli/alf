package comms

import (
	"fmt"
	"testing"
)

// mockRecaller implements MemoryRecaller for testing.
type mockRecaller struct {
	results []MemoryResult
	err     error
}

func (m *mockRecaller) Search(query string, limit int) ([]MemoryResult, error) {
	return m.results, m.err
}

func TestRecall_WithHits(t *testing.T) {
	recaller := &mockRecaller{
		results: []MemoryResult{
			{Text: "User prefers Go", Type: "preference", Distance: 0.3},
			{Text: "User is Italian", Type: "fact", Distance: 0.5},
		},
	}

	result := Recall(recaller, "hello world testing query")
	if result.Block == "" {
		t.Error("expected non-empty block")
	}
	if result.BestDist != 0.3 {
		t.Errorf("BestDist = %.2f, want 0.30", result.BestDist)
	}
	if result.Count != 2 {
		t.Errorf("Count = %d, want 2", result.Count)
	}
}

func TestRecall_NoHits(t *testing.T) {
	recaller := &mockRecaller{results: nil}

	result := Recall(recaller, "hello world testing query")
	if result.Block != "" {
		t.Errorf("expected empty block, got %q", result.Block)
	}
	if result.BestDist != 2.0 {
		t.Errorf("BestDist = %.2f, want 2.0", result.BestDist)
	}
}

func TestRecall_NilRecaller(t *testing.T) {
	result := Recall(nil, "hello world testing query")
	if result.Block != "" {
		t.Error("expected empty block for nil recaller")
	}
	if result.BestDist != 2.0 {
		t.Errorf("BestDist = %.2f, want 2.0", result.BestDist)
	}
}

func TestRecall_ShortMessage(t *testing.T) {
	recaller := &mockRecaller{
		results: []MemoryResult{{Text: "test", Type: "fact", Distance: 0.1}},
	}

	result := Recall(recaller, "hi") // too short
	if result.Block != "" {
		t.Error("expected empty block for short message")
	}
}

func TestRecall_AllFilteredByDistance(t *testing.T) {
	recaller := &mockRecaller{
		results: []MemoryResult{
			{Text: "irrelevant", Type: "fact", Distance: 1.5},
			{Text: "also irrelevant", Type: "fact", Distance: 1.8},
		},
	}

	result := Recall(recaller, "hello world testing query")
	if result.Block != "" {
		t.Error("expected empty block when all filtered")
	}
	if result.Count != 0 {
		t.Errorf("Count = %d, want 0", result.Count)
	}
}

func TestRecall_SearchError(t *testing.T) {
	recaller := &mockRecaller{err: fmt.Errorf("db error")}

	result := Recall(recaller, "hello world testing query")
	if result.Block != "" {
		t.Error("expected empty block on error")
	}
	if result.BestDist != 2.0 {
		t.Errorf("BestDist = %.2f, want 2.0", result.BestDist)
	}
}
