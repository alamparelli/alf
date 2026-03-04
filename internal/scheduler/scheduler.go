package scheduler

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// Config holds dependencies for the scheduler engine.
type Config struct {
	DataDir    string
	ContextDir string
	ChatID     int64
	TG         TelegramSender
	Provider   ProviderInvoker
	TierStore  TierStoreReader
	ChatLogger ChatLogger
	CronPath   string
	Location   *time.Location
}

// Engine is the unified scheduler that manages cron jobs + one-shots.
type Engine struct {
	cfg   Config
	store *Store
	cron  *cron.Cron
	mu    sync.Mutex

	// Map of job ID → cron entry ID for removal.
	entries map[string]cron.EntryID

	server *Server
}

// New creates a scheduler engine.
func New(cfg Config) *Engine {
	loc := cfg.Location
	if loc == nil {
		loc = time.Local
	}
	return &Engine{
		cfg:     cfg,
		store:   NewStore(cfg.CronPath),
		cron:    cron.New(cron.WithLocation(loc), cron.WithSeconds()),
		entries: make(map[string]cron.EntryID),
	}
}

// RegisterSystem adds a system job that isn't persisted to cron.json.
// fn is called on each trigger. The job appears in schedule list.
func (e *Engine) RegisterSystem(id, name, schedule string, fn func() error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	job := &Job{
		ID:        id,
		Name:      name,
		Schedule:  schedule,
		System:    true,
		Enabled:   true,
		Output:    "silent",
		CreatedAt: time.Now(),
	}

	entryID, err := e.cron.AddFunc(schedule, func() {
		if job.running {
			log.Printf("scheduler: skipping %s (still running)", id)
			return
		}
		job.running = true
		start := time.Now()
		defer func() { job.running = false }()

		job.LastRun = &start
		if err := fn(); err != nil {
			job.LastError = err.Error()
			log.Printf("scheduler: [%s] failed (%s): %v", id, time.Since(start).Round(time.Millisecond), err)
		} else {
			job.LastError = ""
			log.Printf("scheduler: [%s] ok (%s)", id, time.Since(start).Round(time.Millisecond))
		}
	})
	if err != nil {
		log.Printf("scheduler: failed to register system job %s: %v", id, err)
		return
	}

	job.cronID = int(entryID)
	e.entries[id] = entryID

	// Add to store (in-memory only, system jobs are registered at startup).
	e.store.mu.Lock()
	e.store.jobs = append(e.store.jobs, job)
	e.store.mu.Unlock()

	log.Printf("scheduler: registered system job %s (%s)", id, schedule)
}

// Start loads persisted jobs, registers them with cron, and starts the socket server.
func (e *Engine) Start(sockPath string) error {
	// Snapshot system jobs registered before Start (RegisterSystem adds them in-memory).
	e.store.mu.RLock()
	systemJobs := make([]*Job, 0)
	for _, j := range e.store.jobs {
		if j.System {
			systemJobs = append(systemJobs, j)
		}
	}
	e.store.mu.RUnlock()

	// Load persisted user jobs from disk.
	if err := e.store.Load(); err != nil {
		log.Printf("scheduler: failed to load cron.json: %v (starting fresh)", err)
	}

	// Re-inject system jobs (Load replaces the jobs slice).
	e.store.mu.Lock()
	e.store.jobs = append(systemJobs, e.store.jobs...)
	e.store.mu.Unlock()

	// Register all enabled persisted user jobs.
	for _, j := range e.store.All() {
		if j.System {
			continue // already registered via RegisterSystem
		}
		if !j.Enabled {
			continue
		}
		if err := e.scheduleJob(j); err != nil {
			log.Printf("scheduler: failed to schedule job %s (%s): %v", j.ID, j.Name, err)
		}
	}

	e.cron.Start()
	log.Printf("scheduler: started with %d entries", len(e.cron.Entries()))

	// Start socket server.
	e.server = NewServer(e, sockPath)
	go func() {
		if err := e.server.Serve(); err != nil {
			log.Printf("scheduler: socket server error: %v", err)
		}
	}()

	return nil
}

// Stop halts the cron engine and socket server.
func (e *Engine) Stop() {
	ctx := e.cron.Stop()
	<-ctx.Done()
	if e.server != nil {
		e.server.Close()
	}
	log.Println("scheduler: stopped")
}

// validOutputs defines acceptable output values.
var validOutputs = map[string]bool{
	"telegram": true,
	"file":     true,
	"both":     true,
	"silent":   true,
}

// Create adds a new user job.
func (e *Engine) Create(name, schedule, tier, prompt, output string) (*Job, error) {
	if output == "" {
		output = "telegram"
	}
	if !validOutputs[output] {
		return nil, fmt.Errorf("invalid output %q (must be telegram, file, both, or silent)", output)
	}

	// Validate tier: must be "direct" or exist in tier store.
	if tier != "" && tier != "direct" && e.cfg.TierStore != nil {
		snap := e.cfg.TierStore.Current()
		found := false
		if snap != nil {
			for _, t := range snap.Tiers {
				if t.Name == tier {
					found = true
					break
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown tier %q", tier)
		}
	}

	j := &Job{
		ID:        GenerateID(),
		Name:      name,
		Schedule:  schedule,
		Tier:      tier,
		Prompt:    prompt,
		Output:    output,
		Enabled:   true,
		CreatedAt: time.Now(),
	}

	// One-shot jobs (RFC3339) auto-delete after execution by default.
	if _, err := time.Parse(time.RFC3339, schedule); err == nil {
		j.AutoDelete = true
	}

	if err := e.scheduleJob(j); err != nil {
		return nil, fmt.Errorf("invalid schedule: %w", err)
	}

	if err := e.store.Add(j); err != nil {
		return nil, err
	}

	log.Printf("scheduler: created job %s (%s) schedule=%s tier=%s", j.ID, j.Name, j.Schedule, j.Tier)
	return j, nil
}

// Delete removes a user job. System jobs cannot be deleted.
func (e *Engine) Delete(id string) error {
	j := e.store.Get(id)
	if j == nil {
		return fmt.Errorf("job %s not found", id)
	}
	if j.System {
		return fmt.Errorf("cannot delete system job %s", id)
	}

	e.mu.Lock()
	if eid, ok := e.entries[id]; ok {
		e.cron.Remove(eid)
		delete(e.entries, id)
	}
	e.mu.Unlock()

	e.store.Remove(id)
	log.Printf("scheduler: deleted job %s", id)
	return nil
}

// Update modifies a user job's fields.
func (e *Engine) Update(id string, fields map[string]string) (*Job, error) {
	j := e.store.Get(id)
	if j == nil {
		return nil, fmt.Errorf("job %s not found", id)
	}
	if j.System {
		return nil, fmt.Errorf("cannot modify system job %s", id)
	}

	// Validate before applying.
	if v, ok := fields["output"]; ok && !validOutputs[v] {
		return nil, fmt.Errorf("invalid output %q (must be telegram, file, both, or silent)", v)
	}
	if v, ok := fields["tier"]; ok && v != "direct" {
		if e.cfg.TierStore != nil {
			snap := e.cfg.TierStore.Current()
			found := false
			if snap != nil {
				for _, t := range snap.Tiers {
					if t.Name == v {
						found = true
						break
					}
				}
			}
			if !found {
				return nil, fmt.Errorf("unknown tier %q", v)
			}
		}
	}

	reschedule := false
	for k, v := range fields {
		switch k {
		case "name":
			j.Name = v
		case "schedule":
			j.Schedule = v
			reschedule = true
		case "tier":
			j.Tier = v
		case "prompt":
			j.Prompt = v
		case "output":
			j.Output = v
		case "enabled":
			j.Enabled = v == "true"
			reschedule = true
		}
	}

	if reschedule {
		// Remove old entry.
		e.mu.Lock()
		if eid, ok := e.entries[id]; ok {
			e.cron.Remove(eid)
			delete(e.entries, id)
		}
		e.mu.Unlock()

		if j.Enabled {
			if err := e.scheduleJob(j); err != nil {
				return nil, fmt.Errorf("invalid schedule: %w", err)
			}
		}
	}

	if err := e.store.Update(j); err != nil {
		return nil, err
	}

	log.Printf("scheduler: updated job %s", id)
	return j, nil
}

// List returns all jobs (system + user).
func (e *Engine) List(userOnly bool) []*Job {
	all := e.store.All()
	if !userOnly {
		return all
	}
	var out []*Job
	for _, j := range all {
		if !j.System {
			out = append(out, j)
		}
	}
	return out
}

// scheduleJob registers a job with the cron engine.
func (e *Engine) scheduleJob(j *Job) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Try as RFC3339 one-shot first.
	if at, err := time.Parse(time.RFC3339, j.Schedule); err == nil {
		sched := &onceSchedule{at: at}
		entryID := e.cron.Schedule(sched, cron.FuncJob(func() {
			e.executeJob(j)
			// Disable after run.
			j.Enabled = false
			e.store.Update(j)
			if j.AutoDelete {
				e.store.Remove(j.ID)
			}
		}))
		e.entries[j.ID] = entryID
		next := sched.Next(time.Now())
		if !next.IsZero() {
			j.NextRun = &next
		}
		return nil
	}

	// Standard cron expression.
	entryID, err := e.cron.AddFunc(j.Schedule, func() {
		e.executeJob(j)
	})
	if err != nil {
		return err
	}
	e.entries[j.ID] = entryID

	// Set next run from cron entry.
	entry := e.cron.Entry(entryID)
	if !entry.Next.IsZero() {
		next := entry.Next
		j.NextRun = &next
	}

	return nil
}

// onceSchedule fires exactly once at the given time.
// After the target time passes, Next returns zero — cron considers it expired.
type onceSchedule struct {
	at time.Time
}

func (o *onceSchedule) Next(t time.Time) time.Time {
	if t.Before(o.at) {
		return o.at
	}
	return time.Time{}
}
