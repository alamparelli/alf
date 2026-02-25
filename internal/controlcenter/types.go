package controlcenter

import (
	"sync/atomic"
	"time"
)

// Config holds non-secret runtime parameters.
type Config struct {
	LogLevel       string     `json:"log_level"`
	Model          string     `json:"model"`
	AllowedChatIDs []int64    `json:"allowed_chat_ids"`
	SystemPrompt   string     `json:"system_prompt"`
	QuietHours     QuietHours `json:"quiet_hours"`
}

// QuietHours defines a time window where the bot won't respond.
type QuietHours struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		LogLevel:       "info",
		Model:          "sonnet",
		AllowedChatIDs: []int64{},
		SystemPrompt:   "",
		QuietHours:     QuietHours{Start: 0, End: 0},
	}
}

// Tier defines a routing tier for message processing.
type Tier struct {
	Name     string `json:"name"`
	Model    string `json:"model"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}

// TiersConfig wraps a list of tiers.
type TiersConfig struct {
	Tiers []Tier `json:"tiers"`
}

// DefaultTiersConfig returns a TiersConfig with one default tier.
func DefaultTiersConfig() *TiersConfig {
	return &TiersConfig{
		Tiers: []Tier{
			{Name: "default", Model: "sonnet", Priority: 0, Enabled: true},
		},
	}
}

// AllowedModels defines valid model names.
var AllowedModels = map[string]bool{
	"haiku":  true,
	"sonnet": true,
	"opus":   true,
}

// ReloadEvent signals what changed.
type ReloadEvent int

const (
	ReloadConfig ReloadEvent = iota
	ReloadTiers
)

// Stats tracks daemon runtime metrics. Safe for concurrent use.
type Stats struct {
	StartedAt    time.Time
	MessageCount atomic.Int64
	LastMessage  atomic.Pointer[time.Time]
}

// NewStats creates a Stats starting from now.
func NewStats() *Stats {
	return &Stats{StartedAt: time.Now()}
}

// RecordMessage increments count and updates last message time.
func (s *Stats) RecordMessage() {
	s.MessageCount.Add(1)
	now := time.Now()
	s.LastMessage.Store(&now)
}

// DaemonStatus is a snapshot of daemon state for the API.
type DaemonStatus struct {
	Status       string  `json:"status"`
	Uptime       string  `json:"uptime"`
	MessageCount int64   `json:"message_count"`
	LastMessage  *string `json:"last_message"`
	Version      string  `json:"version"`
}
