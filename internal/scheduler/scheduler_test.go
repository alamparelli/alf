package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreLoadSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron.json")

	s := NewStore(path)

	// Load non-existent file — should be fine.
	if err := s.Load(); err != nil {
		t.Fatalf("Load non-existent: %v", err)
	}
	if len(s.All()) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(s.All()))
	}

	// Add a job.
	j := &Job{
		ID:        "test1",
		Name:      "Test Job",
		Schedule:  "@every 5m",
		Tier:      "direct",
		Prompt:    "hello",
		Output:    "silent",
		Enabled:   true,
		CreatedAt: time.Now(),
	}
	if err := s.Add(j); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Verify file exists.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var file struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(file.Jobs) != 1 || file.Jobs[0].ID != "test1" {
		t.Fatalf("unexpected file content: %s", data)
	}

	// Load from file in a new store.
	s2 := NewStore(path)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s2.All()) != 1 {
		t.Fatalf("expected 1 job after load, got %d", len(s2.All()))
	}
	if s2.Get("test1") == nil {
		t.Fatal("job test1 not found after load")
	}
}

func TestStoreRemove(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "cron.json"))

	s.Add(&Job{ID: "a", Name: "A"})
	s.Add(&Job{ID: "b", Name: "B"})

	if !s.Remove("a") {
		t.Fatal("expected Remove to return true")
	}
	if s.Remove("nonexistent") {
		t.Fatal("expected Remove of nonexistent to return false")
	}
	if len(s.All()) != 1 {
		t.Fatalf("expected 1 job, got %d", len(s.All()))
	}
	if s.Get("a") != nil {
		t.Fatal("job a should be removed")
	}
}

func TestStoreUpdate(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "cron.json"))

	s.Add(&Job{ID: "u1", Name: "Original"})

	j := s.Get("u1")
	j.Name = "Updated"
	if err := s.Update(j); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := s.Get("u1")
	if got.Name != "Updated" {
		t.Fatalf("expected Updated, got %s", got.Name)
	}
}

func TestOnceSchedule(t *testing.T) {
	future := time.Now().Add(1 * time.Hour)
	past := time.Now().Add(-1 * time.Hour)

	s := &onceSchedule{at: future}

	// Before target: always returns the target time (called multiple times).
	for i := 0; i < 5; i++ {
		next := s.Next(time.Now())
		if next.IsZero() {
			t.Fatalf("call %d: expected non-zero next for future one-shot", i)
		}
		if !next.Equal(future) {
			t.Fatalf("call %d: expected %v, got %v", i, future, next)
		}
	}

	// After target time passes: returns zero.
	next := s.Next(future.Add(1 * time.Second))
	if !next.IsZero() {
		t.Fatal("expected zero next after target time passed")
	}

	// Past time should return zero immediately.
	sp := &onceSchedule{at: past}
	if !sp.Next(time.Now()).IsZero() {
		t.Fatal("expected zero next for past one-shot")
	}
}

func TestGenerateID(t *testing.T) {
	id1 := GenerateID()
	id2 := GenerateID()
	if len(id1) != 8 {
		t.Fatalf("expected 8 chars, got %d", len(id1))
	}
	if id1 == id2 {
		t.Fatal("expected unique IDs")
	}
}

func TestEngineCreateDelete(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})

	// Start without socket (we don't need it for this test).
	e.cron.Start()
	defer e.cron.Stop()

	// Create a job.
	j, err := e.Create("test", "@every 1h", "direct", "hello", "silent")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if j.ID == "" {
		t.Fatal("expected non-empty ID")
	}

	// List should show the job.
	jobs := e.List(false)
	found := false
	for _, jj := range jobs {
		if jj.ID == j.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("created job not found in list")
	}

	// Delete.
	if err := e.Delete(j.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Should be gone.
	if e.store.Get(j.ID) != nil {
		t.Fatal("job should be deleted")
	}
}

func TestEngineUpdate(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})

	e.cron.Start()
	defer e.cron.Stop()

	j, _ := e.Create("test", "@every 1h", "direct", "hello", "telegram")

	updated, err := e.Update(j.ID, map[string]string{
		"name":   "renamed",
		"output": "silent",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "renamed" {
		t.Fatalf("expected renamed, got %s", updated.Name)
	}
	if updated.Output != "silent" {
		t.Fatalf("expected silent, got %s", updated.Output)
	}
}

func TestEngineSystemJobCannotBeDeleted(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})

	e.RegisterSystem("sys1", "System Job", "@every 1m", func() error { return nil })

	if err := e.Delete("sys1"); err == nil {
		t.Fatal("expected error deleting system job")
	}
}

func TestEngineListUserOnly(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})

	e.cron.Start()
	defer e.cron.Stop()

	e.RegisterSystem("sys1", "System", "@every 1m", func() error { return nil })
	e.Create("user1", "@every 2h", "direct", "hello", "silent")

	all := e.List(false)
	if len(all) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(all))
	}

	userOnly := e.List(true)
	if len(userOnly) != 1 {
		t.Fatalf("expected 1 user job, got %d", len(userOnly))
	}
	if userOnly[0].System {
		t.Fatal("user-only list returned a system job")
	}
}

func TestSystemJobsSurviveStart(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})

	// Register system jobs before Start (like daemon does).
	e.RegisterSystem("sys1", "Git Sweep", "@every 5m", func() error { return nil })
	e.RegisterSystem("sys2", "Update Check", "@every 1h", func() error { return nil })

	// Start loads from disk — should not lose system jobs.
	if err := e.Start(sockPath); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer e.Stop()

	all := e.List(false)
	sysCount := 0
	for _, j := range all {
		if j.System {
			sysCount++
		}
	}
	if sysCount != 2 {
		t.Fatalf("expected 2 system jobs after Start, got %d (total=%d)", sysCount, len(all))
	}
}
