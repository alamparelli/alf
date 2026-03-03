package controlcenter

import (
	"sync/atomic"
	"time"
)

// Config holds non-secret runtime parameters.
type Config struct {
	LogLevel       string     `json:"log_level"`
	AllowedChatIDs []int64    `json:"allowed_chat_ids"`
	SystemPrompt   string     `json:"system_prompt"`
	QuietHours     QuietHours `json:"quiet_hours"`
	SessionTimeout   int        `json:"session_timeout"`    // minutes, 0 = use default (30)
	GitTrack         bool       `json:"git_track"`          // enable git tracking of data dir
	GitSweepInterval int        `json:"git_sweep_interval"` // minutes between auto-commits, 0 = disabled
	AutoUpdateCheck         bool `json:"auto_update_check"`          // check for Docker image updates periodically
	AutoUpdateCheckInterval int  `json:"auto_update_check_interval"` // seconds between update checks, 0 = use default (21600)
	AutoUpdateNotify        bool `json:"auto_update_notify"`         // send Telegram notification when update available
	AuthBanThreshold        int    `json:"auth_ban_threshold"`         // failed /auth attempts before IP ban, 0 = use default (10)
	AuthBanDuration         int    `json:"auth_ban_duration"`          // IP ban duration in minutes, 0 = use default (15)
	Timezone                string `json:"timezone"`                   // IANA timezone (e.g. "Europe/Brussels"), empty = TZ env or UTC
}

// QuietHours defines a time window where the bot won't respond.
type QuietHours struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		LogLevel:         "info",
		AllowedChatIDs:   []int64{},
		SystemPrompt:     "",
		QuietHours:       QuietHours{Start: 0, End: 0},
		SessionTimeout:   30,
		GitTrack:         true,
		GitSweepInterval: 5,
		AutoUpdateCheck:         true,
		AutoUpdateCheckInterval: 21600,
		AutoUpdateNotify:        true,
		AuthBanThreshold:        10,
		AuthBanDuration:         15,
	}
}

// Tier defines a routing tier for message processing.
type Tier struct {
	Name         string   `json:"name"`
	Model        string   `json:"model"`
	Priority     int      `json:"priority"`
	Enabled      bool     `json:"enabled"`
	Routable     bool     `json:"routable"`
	Instant      bool     `json:"instant"`
	RouterLabel  string   `json:"router_label,omitempty"`
	Description  string   `json:"description,omitempty"`
	MaxTurns     int      `json:"max_turns,omitempty"`
	WriteCapable bool     `json:"write_capable"`
	Tools        []string `json:"tools,omitempty"`
	Effort       string   `json:"effort,omitempty"`
	ForceCommand bool     `json:"force_command"`
}

// RouterDescription returns Description if set, otherwise falls back to RouterLabel.
func (t Tier) RouterDescription() string {
	if t.Description != "" {
		return t.Description
	}
	return t.RouterLabel
}

// TiersConfig wraps a list of tiers plus router-level settings.
type TiersConfig struct {
	Tiers              []Tier `json:"tiers"`
	RouterModel        string `json:"router_model,omitempty"`
	DefaultFallback    string `json:"default_fallback,omitempty"`
	RouterInstantLabel string `json:"router_instant_label,omitempty"`
	RouterDistinctions string `json:"router_distinctions,omitempty"`
}

// DefaultTiersConfig returns a TiersConfig with starter tiers.
// 6-tier layout: instant + read/write pairs per model (haiku, sonnet, opus).
func DefaultTiersConfig() *TiersConfig {
	return &TiersConfig{
		RouterModel:        "haiku",
		DefaultFallback:    "sonnet_r",
		RouterInstantLabel: "Quick greetings, thank-yous, acknowledgments, simple yes/no",
		RouterDistinctions: "Read-only tiers (_r) for analysis and questions. Read-write tiers (_rw) ONLY when the user explicitly asks to create, modify, or delete files. Use haiku for simple tasks, sonnet for moderate, opus for complex.",
		Tiers: []Tier{
			{Name: "instant", Model: "haiku", Priority: 0, Enabled: true, Routable: true, Instant: true, RouterLabel: "Quick greetings, acknowledgments, yes/no", Effort: "low"},
			{Name: "haiku_r", Model: "haiku", Priority: 1, Enabled: true, Routable: true, RouterLabel: "Simple file lookups, quick reads, basic questions", Effort: "low"},
			{Name: "sonnet_r", Model: "sonnet", Priority: 2, Enabled: true, Routable: true, RouterLabel: "Analysis, reasoning, explanations, code review", Effort: "medium"},
			{Name: "sonnet_rw", Model: "sonnet", Priority: 3, Enabled: true, Routable: true, RouterLabel: "Code edits, file modifications, small features", WriteCapable: true, ForceCommand: true, Effort: "medium"},
			{Name: "opus_r", Model: "opus", Priority: 4, Enabled: true, Routable: true, RouterLabel: "Deep reasoning, architecture review, complex analysis", Effort: "high"},
			{Name: "opus_rw", Model: "opus", Priority: 5, Enabled: true, Routable: true, RouterLabel: "Complex refactoring, multi-file changes, large features", WriteCapable: true, ForceCommand: true, Effort: "high"},
		},
	}
}

// AllowedModels defines valid model names for tier validation.
var AllowedModels = map[string]bool{
	"haiku":  true,
	"sonnet": true,
	"opus":   true,
}

// AllowedEfforts defines valid effort levels (empty string = unset).
var AllowedEfforts = map[string]bool{
	"":       true,
	"low":    true,
	"medium": true,
	"high":   true,
}

// ReloadEvent signals what changed.
type ReloadEvent int

const (
	ReloadConfig ReloadEvent = iota
	ReloadTiers
	ReloadTools
	ReloadSkills
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
