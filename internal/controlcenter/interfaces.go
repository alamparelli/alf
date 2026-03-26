package controlcenter

import "time"

// ConfigStore loads and saves runtime configuration.
type ConfigStore interface {
	Load() (*Config, error)
	Save(cfg *Config) error
}

// TierStore manages routing tiers with hot-reload support.
type TierStore interface {
	Load() (*TiersConfig, error)
	Save(cfg *TiersConfig) error
	Current() *TiersConfig // in-memory snapshot, no disk I/O
	Reload() error
	// SetPath changes the backing file and reloads tiers from it.
	// Used when tiers_file in config.json is updated at runtime.
	SetPath(path string) error
	// Path returns the current backing file path.
	Path() string
}

// LogReader reads log files by name.
type LogReader interface {
	Tail(name string, n int) ([]string, error)
	Available() []string
}

// StatusProvider returns the current daemon status.
type StatusProvider interface {
	Status() DaemonStatus
}

// Notifier sends reload events from CC to the daemon.
type Notifier interface {
	Notify(event ReloadEvent) // non-blocking, best-effort
}

// ScheduleJob is the CC-facing view of a scheduled job.
type ScheduleJob struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Schedule    string  `json:"schedule"`
	Tier       string  `json:"tier"`
	Prompt     string  `json:"prompt"`
	Command    string  `json:"command,omitempty"`
	Message    string  `json:"message,omitempty"`
	Output     string  `json:"output"`
	Enabled    bool    `json:"enabled"`
	System     bool    `json:"system"`
	Managed    bool    `json:"managed,omitempty"`
	AutoDelete bool    `json:"auto_delete"`
	Timeout    string   `json:"timeout,omitempty"` // Go duration string
	Skills     []string `json:"skills,omitempty"`
	CreatedAt  string  `json:"created_at"`
	LastRun    string  `json:"last_run,omitempty"`
	NextRun    string  `json:"next_run,omitempty"`
	LastError  string  `json:"last_error,omitempty"`
	Running    bool    `json:"running,omitempty"`
}

// ScheduleEngine is the subset of scheduler.Engine used by the CC schedules tab.
type ScheduleEngine interface {
	List(userOnly bool) []ScheduleJob
	Create(name, schedule, tier, prompt, command, output string, timeout time.Duration, skills []string) (*ScheduleJob, error)
	CreateReminder(name, schedule, message, output string, timeout time.Duration) (*ScheduleJob, error)
	Delete(id string) error
	Update(id string, fields map[string]string) (*ScheduleJob, error)
	RunNow(id string) error
}
