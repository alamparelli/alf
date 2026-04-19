package memory_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/memory/memtest"
)

func testingContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// TestSQLite_StoreContract runs the shared memtest harness against a file-
// backed SQLiteStore. Each sub-test gets an isolated DB via t.TempDir().
func TestSQLite_StoreContract(t *testing.T) {
	memtest.RunStoreContract(t, func() memory.Store {
		dir := filepath.Join(t.TempDir(), "store")
		s, err := memory.NewSQLiteStore(dir)
		if err != nil {
			t.Fatalf("NewSQLiteStore: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

// TestSQLite_ReopenPreservesState verifies that closing and reopening the
// DB does not lose messages, summaries, or prefs. The memtest harness
// creates a fresh DB per sub-test so this is a separate check.
func TestSQLite_ReopenPreservesState(t *testing.T) {
	ctx := testingContext(t)
	dir := t.TempDir()

	s1, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore (first): %v", err)
	}
	if err := s1.EnsureConv(ctx, "c", "hello", memory.ChannelCC); err != nil {
		t.Fatalf("EnsureConv: %v", err)
	}
	if err := s1.AppendMessage(ctx, "c", memory.Message{Role: "user", Content: "persisted"}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s1.SetPref(ctx, "theme", "dark"); err != nil {
		t.Fatalf("SetPref: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := memory.NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore (reopen): %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	msgs, err := s2.ListMessages(ctx, "c", memory.ListOpts{})
	if err != nil {
		t.Fatalf("ListMessages after reopen: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "persisted" {
		t.Errorf("reopen lost messages: %+v", msgs)
	}
	// New append must not collide with the persisted msg ID.
	if err := s2.AppendMessage(ctx, "c", memory.Message{Role: "assistant", Content: "after-reopen"}); err != nil {
		t.Fatalf("AppendMessage after reopen: %v", err)
	}
	msgs, _ = s2.ListMessages(ctx, "c", memory.ListOpts{})
	if len(msgs) != 2 {
		t.Fatalf("want 2 msgs after reopen+append, got %d", len(msgs))
	}
	if msgs[0].ID == msgs[1].ID {
		t.Errorf("ID collision on reopen: %v", msgs[0].ID)
	}

	v, err := s2.GetPref(ctx, "theme")
	if err != nil {
		t.Fatalf("GetPref: %v", err)
	}
	if v != "dark" {
		t.Errorf("pref not preserved: got %v", v)
	}
}

// TestSQLite_ConcurrentAppend_NoLockedErrors pins the #346 regression on the
// new store: concurrent writers across distinct conversations must return
// zero errors. No retry loop — SetMaxOpenConns(1) serialises the writers
// at the pool layer.
func TestSQLite_ConcurrentAppend_NoLockedErrors(t *testing.T) {
	s, err := memory.NewSQLiteStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := testingContext(t)
	const nConvs = 4
	const nMsgs = 25

	for i := 0; i < nConvs; i++ {
		if err := s.EnsureConv(ctx, memory.ConvID(fmt.Sprintf("c%d", i)), "", memory.ChannelCC); err != nil {
			t.Fatalf("EnsureConv: %v", err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, nConvs*nMsgs)
	for c := 0; c < nConvs; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			convID := memory.ConvID(fmt.Sprintf("c%d", c))
			for i := 0; i < nMsgs; i++ {
				if err := s.AppendMessage(ctx, convID, memory.Message{
					Role:    "user",
					Content: fmt.Sprintf("payload-%d-%d", c, i),
				}); err != nil {
					errs <- fmt.Errorf("%s-%d: %w", convID, i, err)
				}
			}
		}(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent AppendMessage failed: %v", err)
	}

	for c := 0; c < nConvs; c++ {
		convID := memory.ConvID(fmt.Sprintf("c%d", c))
		msgs, err := s.ListMessages(ctx, convID, memory.ListOpts{})
		if err != nil {
			t.Fatalf("ListMessages(%s): %v", convID, err)
		}
		if len(msgs) != nMsgs {
			t.Errorf("conv %s: expected %d msgs, got %d", convID, nMsgs, len(msgs))
		}
		seen := map[int64]bool{}
		for _, m := range msgs {
			if m.Seq < 1 || m.Seq > int64(nMsgs) {
				t.Errorf("conv %s: out-of-range seq %d", convID, m.Seq)
			}
			if seen[m.Seq] {
				t.Errorf("conv %s: duplicate seq %d", convID, m.Seq)
			}
			seen[m.Seq] = true
		}
	}
}
