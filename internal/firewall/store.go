package firewall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// HostStat tracks cumulative stats for a single host.
type HostStat struct {
	Host     string    `json:"host"`
	Count    int       `json:"count"`
	Allowed  int       `json:"allowed"`
	Blocked  int       `json:"blocked"`
	LastSeen time.Time `json:"last_seen"`
}

// Store handles persistence of firewall config and host stats.
type Store struct {
	path      string
	hostsPath string
	mu        sync.Mutex
	hosts     map[string]*HostStat
}

// NewStore creates a Store for the given config directory.
func NewStore(configDir string) *Store {
	s := &Store{
		path:      filepath.Join(configDir, "firewall.json"),
		hostsPath: filepath.Join(configDir, "firewall-hosts.json"),
		hosts:     make(map[string]*HostStat),
	}
	s.loadHosts()
	return s
}

// RecordHost updates the cumulative stats for a host.
func (s *Store) RecordHost(host string, blocked bool) {
	s.mu.Lock()
	h, ok := s.hosts[host]
	if !ok {
		h = &HostStat{Host: host}
		s.hosts[host] = h
	}
	h.Count++
	if blocked {
		h.Blocked++
	} else {
		h.Allowed++
	}
	h.LastSeen = time.Now()
	s.mu.Unlock()
}

// Hosts returns all cumulative host stats sorted by count descending.
func (s *Store) Hosts() []HostStat {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]HostStat, 0, len(s.hosts))
	for _, h := range s.hosts {
		out = append(out, *h)
	}
	return out
}

// SaveHosts persists host stats to disk.
func (s *Store) SaveHosts() {
	s.mu.Lock()
	data, err := json.MarshalIndent(s.hosts, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return
	}
	os.WriteFile(s.hostsPath, data, 0o644)
}

func (s *Store) loadHosts() {
	data, err := os.ReadFile(s.hostsPath)
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.hosts)
}

// Load reads the config from disk; returns default if file doesn't exist.
func (s *Store) Load() (*Config, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save writes the config to disk.
func (s *Store) Save(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o644)
}
