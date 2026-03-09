package scheduler

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Job represents a scheduled task (user-created or system).
type Job struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Schedule   string     `json:"schedule"`
	Tier       string     `json:"tier"`
	Prompt     string     `json:"prompt"`
	Command    string     `json:"command,omitempty"`
	Output     string     `json:"output"`
	Enabled    bool       `json:"enabled"`
	System     bool       `json:"system"`
	Managed    bool       `json:"managed,omitempty"`
	AutoDelete bool          `json:"auto_delete"`
	Timeout    time.Duration `json:"timeout,omitempty"` // 0 = use default per tier
	Skills     []string      `json:"skills,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastRun    *time.Time `json:"last_run"`
	NextRun    *time.Time `json:"next_run"`
	LastError  string     `json:"last_error"`

	// Runtime state (not persisted).
	running bool
	cronID  int // robfig/cron entry ID
}

// Store persists jobs to a JSON file with atomic rename.
type Store struct {
	path string
	mu   sync.RWMutex
	jobs []*Job
}

// NewStore creates a Store backed by the given file path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Load reads jobs from disk. If the file doesn't exist, starts empty.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.jobs = nil
			return nil
		}
		return fmt.Errorf("read cron.json: %w", err)
	}

	var file struct {
		Jobs []*Job `json:"jobs"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse cron.json: %w", err)
	}
	// Filter out any system jobs that leaked into cron.json (legacy bug).
	var userJobs []*Job
	for _, j := range file.Jobs {
		if !j.System {
			userJobs = append(userJobs, j)
		}
	}
	s.jobs = userJobs
	return nil
}

// Save writes non-system jobs to disk using atomic rename.
// System jobs are transient — re-registered at every boot via RegisterSystem.
func (s *Store) Save() error {
	s.mu.RLock()
	var userJobs []*Job
	for _, j := range s.jobs {
		if !j.System {
			userJobs = append(userJobs, j)
		}
	}
	data, err := json.MarshalIndent(struct {
		Jobs []*Job `json:"jobs"`
	}{Jobs: userJobs}, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshal cron.json: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create cron dir: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write cron.json.tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename cron.json: %w", err)
	}
	return nil
}

// All returns a copy of all jobs.
func (s *Store) All() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Job, len(s.jobs))
	copy(out, s.jobs)
	return out
}

// Get returns a job by ID, or nil.
func (s *Store) Get(id string) *Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, j := range s.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// Add appends a job and persists.
func (s *Store) Add(j *Job) error {
	s.mu.Lock()
	s.jobs = append(s.jobs, j)
	s.mu.Unlock()
	return s.Save()
}

// Remove deletes a job by ID and persists. Returns false if not found.
func (s *Store) Remove(id string) bool {
	s.mu.Lock()
	found := false
	for i, j := range s.jobs {
		if j.ID == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			found = true
			break
		}
	}
	s.mu.Unlock()
	if found {
		s.Save()
	}
	return found
}

// Update modifies a job in place and persists.
func (s *Store) Update(j *Job) error {
	s.mu.Lock()
	for i, existing := range s.jobs {
		if existing.ID == j.ID {
			s.jobs[i] = j
			break
		}
	}
	s.mu.Unlock()
	return s.Save()
}

// GenerateID creates a short random hex ID.
func GenerateID() string {
	const chars = "0123456789abcdef"
	b := make([]byte, 8)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}
