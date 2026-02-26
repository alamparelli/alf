package controlcenter

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
