package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// testSockPath returns a short unix-socket path safe on macOS (SUN_PATH ~104).
// t.TempDir() nests under /var/folders/... which routinely exceeds the limit.
var testSockSeq uint64

func testSockPath(t *testing.T) string {
	t.Helper()
	n := atomic.AddUint64(&testSockSeq, 1)
	p := filepath.Join(os.TempDir(), fmt.Sprintf("alf-sched-%d-%d.sock", os.Getpid(), n))
	t.Cleanup(func() { os.Remove(p) })
	return p
}

func TestStoreLoadSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron.json")

	s := NewStore(path)

	// Load non-existent file - should be fine.
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
	j, err := e.Create("test", "@every 1h", "direct", "", "echo hello", "silent", 0, nil, "")
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

	j, _ := e.Create("test", "@every 1h", "direct", "", "echo hello", "chat", 0, nil, "")

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

func TestEngineRegisterSystemHydratesFromRunLog(t *testing.T) {
	// Regression for #257: long-interval system jobs (@every 360m) appeared
	// idle in the UI after each daemon restart because LastRun/NextRun were
	// not restored from the runlog. RegisterSystem must hydrate them so the
	// display reflects the actual execution history.
	dir := t.TempDir()

	// Pre-populate the runlog as if the job had run yesterday under a
	// previous daemon process.
	logDir := filepath.Join(dir, "logs", "scheduler")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rl := NewRunLog(logDir)
	pastRun := time.Now().Add(-2 * time.Hour)
	rl.Append(RunRecord{
		JobID:     "mem-consolidate",
		JobName:   "Memory Consolidation",
		Tier:      "system",
		StartedAt: pastRun,
		Status:    "ok",
	})

	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})

	e.RegisterSystem("mem-consolidate", "Memory Consolidation", "@every 360m", func() error { return nil })

	// LastRun should already be hydrated from the runlog after RegisterSystem,
	// without needing Start() to run.
	jobs := e.List(false)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	job := jobs[0]
	if job.LastRun == nil {
		t.Fatal("expected LastRun to be hydrated from runlog, got nil")
	}
	if !job.LastRun.Equal(pastRun) {
		t.Errorf("expected LastRun=%v, got %v", pastRun, *job.LastRun)
	}

	// After Start, NextRun should be populated from the cron entry.
	sockPath := testSockPath(t)
	if err := e.Start(sockPath); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer e.Stop()

	jobs = e.List(false)
	job = jobs[0]
	if job.NextRun == nil {
		t.Error("expected NextRun to be populated after Start, got nil")
	}
	// LastRun must survive Start (which reloads the store).
	if job.LastRun == nil || !job.LastRun.Equal(pastRun) {
		t.Errorf("LastRun lost after Start: got %v", job.LastRun)
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
	e.Create("user1", "@every 2h", "direct", "", "echo hello", "silent", 0, nil, "")

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

func TestManagedJobCannotBeDeleted(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})
	e.cron.Start()
	defer e.cron.Stop()

	_, err := e.EnsureManaged("sec-audit", "Security Audit", "@every 1h", "haiku", "audit", "chat", nil, true)
	if err != nil {
		t.Fatalf("EnsureManaged: %v", err)
	}

	if err := e.Delete("sec-audit"); err == nil {
		t.Fatal("expected error deleting managed job")
	}
}

func TestManagedJobCannotBeUpdated(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})
	e.cron.Start()
	defer e.cron.Stop()

	_, err := e.EnsureManaged("sec-audit", "Security Audit", "@every 1h", "haiku", "audit", "chat", nil, true)
	if err != nil {
		t.Fatalf("EnsureManaged: %v", err)
	}

	if _, err := e.Update("sec-audit", map[string]string{"prompt": "new"}); err == nil {
		t.Fatal("expected error updating managed job")
	}
}

func TestEnsureManagedIdempotent(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})
	e.cron.Start()
	defer e.cron.Stop()

	j1, err := e.EnsureManaged("sec-audit", "Security Audit", "@every 1h", "haiku", "audit v1", "chat", nil, true)
	if err != nil {
		t.Fatalf("EnsureManaged first call: %v", err)
	}

	j2, err := e.EnsureManaged("sec-audit", "Security Audit", "@every 2h", "haiku", "audit v2", "chat", nil, true)
	if err != nil {
		t.Fatalf("EnsureManaged second call: %v", err)
	}

	if j1.ID != j2.ID {
		t.Fatalf("expected same job ID, got %s and %s", j1.ID, j2.ID)
	}
	if j2.Prompt != "audit v1" {
		t.Fatalf("expected original prompt preserved, got %s", j2.Prompt)
	}

	// Should be only one job.
	count := 0
	for _, j := range e.List(false) {
		if j.ID == "sec-audit" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 managed job, got %d", count)
	}
}

func TestManagedJobPersistedInCronJSON(t *testing.T) {
	dir := t.TempDir()
	cronPath := filepath.Join(dir, "cron.json")

	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   cronPath,
	})
	e.cron.Start()
	defer e.cron.Stop()

	_, err := e.EnsureManaged("sec-audit", "Security Audit", "@every 1h", "haiku", "audit", "chat", nil, true)
	if err != nil {
		t.Fatalf("EnsureManaged: %v", err)
	}

	// Load in a fresh store - managed job should be present.
	s2 := NewStore(cronPath)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	j := s2.Get("sec-audit")
	if j == nil {
		t.Fatal("managed job not found after reload")
	}
	if !j.Managed {
		t.Fatal("expected Managed=true after reload")
	}
}

func TestManagedJobVisibleInList(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})
	e.cron.Start()
	defer e.cron.Stop()

	e.EnsureManaged("sec-audit", "Security Audit", "@every 1h", "haiku", "audit", "chat", nil, true)
	e.Create("user-job", "@every 2h", "direct", "", "echo hello", "silent", 0, nil, "")

	all := e.List(false)
	found := false
	for _, j := range all {
		if j.ID == "sec-audit" && j.Managed {
			found = true
		}
	}
	if !found {
		t.Fatal("managed job not found in List(false)")
	}

	// userOnly=true should still include managed jobs (they're not system).
	userJobs := e.List(true)
	found = false
	for _, j := range userJobs {
		if j.ID == "sec-audit" {
			found = true
		}
	}
	if !found {
		t.Fatal("managed job should appear in List(true) since it's not a system job")
	}
}

func TestSystemJobsSurviveStart(t *testing.T) {
	dir := t.TempDir()
	sockPath := testSockPath(t)

	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})

	// Register system jobs before Start (like daemon does).
	e.RegisterSystem("sys1", "Git Sweep", "@every 5m", func() error { return nil })
	e.RegisterSystem("sys2", "Update Check", "@every 1h", func() error { return nil })

	// Start loads from disk - should not lose system jobs.
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

func TestUpdateSystemJob_AllowedFields(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})

	e.RegisterSystem("sys-test", "System Test", "@every 1m", func() error { return nil })
	e.cron.Start()
	defer e.cron.Stop()

	// Update allowed fields: enabled, output, description.
	updated, err := e.Update("sys-test", map[string]string{
		"enabled":     "false",
		"output":      "chat",
		"description": "updated description",
	})
	if err != nil {
		t.Fatalf("Update allowed fields: %v", err)
	}
	if updated.Enabled {
		t.Error("expected enabled=false after update")
	}
	if updated.Output != "chat" {
		t.Errorf("expected output=chat, got %s", updated.Output)
	}
	if updated.Description != "updated description" {
		t.Errorf("expected description='updated description', got %s", updated.Description)
	}
}

func TestUpdateSystemJob_BlockedFields(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})

	e.RegisterSystem("sys-test", "System Test", "@every 1m", func() error { return nil })
	e.cron.Start()
	defer e.cron.Stop()

	// Attempt to update blocked field: name.
	if _, err := e.Update("sys-test", map[string]string{"name": "hacked"}); err == nil {
		t.Error("expected error when updating 'name' on system job")
	}

	// Attempt to update blocked field: prompt.
	if _, err := e.Update("sys-test", map[string]string{"prompt": "new prompt"}); err == nil {
		t.Error("expected error when updating 'prompt' on system job")
	}

	// Attempt to update blocked field: schedule.
	if _, err := e.Update("sys-test", map[string]string{"schedule": "@every 5m"}); err == nil {
		t.Error("expected error when updating 'schedule' on system job")
	}

	// Verify original values unchanged.
	j := e.store.Get("sys-test")
	if j.Name != "System Test" {
		t.Errorf("name should be unchanged, got %s", j.Name)
	}
}

func TestCreateJobWithReason(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})
	e.cron.Start()
	defer e.cron.Stop()

	j, err := e.Create("api-monitor", "@every 30m", "direct", "", "curl -sf http://localhost/health", "silent", 0, nil, "API has been flaky since the March migration")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if j.Reason != "API has been flaky since the March migration" {
		t.Errorf("expected reason to be set, got %q", j.Reason)
	}

	// Verify persistence.
	got := e.store.Get(j.ID)
	if got.Reason != j.Reason {
		t.Errorf("reason not persisted: got %q", got.Reason)
	}
}

func TestUpdateReason(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})
	e.cron.Start()
	defer e.cron.Stop()

	j, _ := e.Create("test-job", "@every 1h", "direct", "", "echo ok", "silent", 0, nil, "")
	updated, err := e.Update(j.ID, map[string]string{"reason": "now monitoring for compliance"})
	if err != nil {
		t.Fatalf("Update reason: %v", err)
	}
	if updated.Reason != "now monitoring for compliance" {
		t.Errorf("expected updated reason, got %q", updated.Reason)
	}
}

func TestUpdateSystemJob_ReasonAllowed(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})
	e.RegisterSystem("sys-reason", "System Reason Test", "@every 1m", func() error { return nil })
	e.cron.Start()
	defer e.cron.Stop()

	updated, err := e.Update("sys-reason", map[string]string{"reason": "added for compliance audit"})
	if err != nil {
		t.Fatalf("Update reason on system job should be allowed: %v", err)
	}
	if updated.Reason != "added for compliance audit" {
		t.Errorf("expected reason on system job, got %q", updated.Reason)
	}
}

func TestCreateReminderWithReason(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})
	e.cron.Start()
	defer e.cron.Stop()

	j, err := e.CreateReminder("standup", "@every 24h", "Daily standup in 5 minutes", "chat", 0, "team requested daily reminder")
	if err != nil {
		t.Fatalf("CreateReminder: %v", err)
	}
	if j.Reason != "team requested daily reminder" {
		t.Errorf("expected reason on reminder, got %q", j.Reason)
	}
}

func TestWarnFixedDayMonthCron(t *testing.T) {
	// Should reject: fixed day + fixed month = likely one-shot intent.
	bad := []string{
		"0 0 9 23 3 *",  // March 23 at 9am
		"0 0 9 31 3 *",  // March 31
		"0 2 0 3 4 *",   // April 3
	}
	for _, s := range bad {
		if err := warnFixedDayMonthCron(s); err == nil {
			t.Errorf("expected error for %q, got nil", s)
		}
	}

	// Should allow: recurring patterns.
	good := []string{
		"0 0 9 * * 1-5",   // weekdays at 9am
		"0 0 */6 * * *",   // every 6 hours
		"0 0 9 1 * *",     // 1st of every month (wildcard month)
		"0 0 9 * 3 *",     // every day in March (wildcard day)
		"0 0 9 1-15 3 *",  // range day
		"0 0 9 1,15 3 *",  // list day
		"@every 5m",       // non-standard (not 6 fields)
	}
	for _, s := range good {
		if err := warnFixedDayMonthCron(s); err != nil {
			t.Errorf("unexpected error for %q: %v", s, err)
		}
	}
}

func TestCreateRejectsFixedDayMonthCron(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})
	e.cron.Start()
	defer e.cron.Stop()

	// Should reject cron with fixed day+month.
	_, err := e.Create("bad", "0 0 9 23 3 *", "direct", "", "echo hi", "silent", 0, nil, "")
	if err == nil {
		t.Fatal("expected error for fixed day+month cron")
	}

	// Should reject reminder too.
	_, err = e.CreateReminder("bad reminder", "0 0 9 23 3 *", "hello", "silent", 0, "")
	if err == nil {
		t.Fatal("expected error for fixed day+month cron in reminder")
	}

	// RFC3339 should still work.
	_, err = e.CreateReminder("good", "2099-03-23T09:00:00+02:00", "hello", "silent", 0, "")
	if err != nil {
		t.Fatalf("RFC3339 reminder should work: %v", err)
	}
}

func TestStartCleansExpiredOneShots(t *testing.T) {
	dir := t.TempDir()
	cronPath := filepath.Join(dir, "cron.json")

	// Seed cron.json with an expired one-shot and a valid cron job.
	past := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	jobs := []map[string]any{
		{"id": "expired1", "name": "Past Job", "schedule": past, "enabled": true, "output": "silent", "auto_delete": true},
		{"id": "recurring1", "name": "Recurring", "schedule": "0 0 9 * * *", "enabled": true, "output": "silent"},
	}
	data, _ := json.Marshal(map[string]any{"jobs": jobs})
	os.WriteFile(cronPath, data, 0o644)

	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   cronPath,
	})

	sockPath := testSockPath(t)
	if err := e.Start(sockPath); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer e.Stop()

	// Expired one-shot should be cleaned up.
	if e.store.Get("expired1") != nil {
		t.Error("expired one-shot should have been removed on startup")
	}
	// Recurring job should survive.
	if e.store.Get("recurring1") == nil {
		t.Error("recurring job should still exist")
	}
}

// Regression for #284: a failed socket bind (e.g. parent dir unwritable inside
// a container) must surface from Start() so operators see the failure at boot
// instead of discovering it later when schedule-tools gets "no such file".
func TestStartSurfacesSocketBindError(t *testing.T) {
	dir := t.TempDir()
	e := New(Config{
		DataDir:    dir,
		ContextDir: dir,
		CronPath:   filepath.Join(dir, "cron.json"),
	})

	// Socket path under a file (not a directory) → bind must fail.
	blocker := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	bad := filepath.Join(blocker, "scheduler.sock")

	err := e.Start(bad)
	if err == nil {
		e.Stop()
		t.Fatal("expected Start to return an error when the socket path is unbindable")
	}
}
