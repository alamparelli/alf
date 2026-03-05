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
	TiersTimeout            int    `json:"tiers_timeout"`              // seconds for Claude tier invocations, 0 = default (300)
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
		TiersTimeout:            300,
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
// 7-tier layout: instant + read/write pairs per model (haiku, sonnet, opus).
func DefaultTiersConfig() *TiersConfig {
	return &TiersConfig{
		RouterModel:        "haiku",
		DefaultFallback:    "haiku_r",
		RouterInstantLabel: "One-line replies: hi, thanks, ok, bye, thumbs up, simple yes/no answers",
		RouterDistinctions: "haiku vs sonnet: If the answer requires reasoning, analysis, or structured output, use sonnet. If it is conversational, factual recall, or a short answer, use haiku. " +
			"sonnet vs opus: Use opus only when the task involves system-wide thinking, multi-component architecture, or requires holding many constraints simultaneously. Most code tasks are sonnet. " +
			"read vs write (_r vs _rw): Use _rw ONLY when the user explicitly requests to create, edit, delete, or modify files, run a tool, or execute a scheduled action. Questions about code or asking for suggestions stay in _r even if they mention files. " +
			"When in doubt: Default to haiku_r. It is better to respond fast with a simpler model than to over-escalate.",
		Tiers: []Tier{
			{Name: "instant", Model: "haiku", Priority: 0, Enabled: true, Routable: true, Instant: true, RouterLabel: "One-line replies: hi, thanks, ok, bye, thumbs up, simple yes/no answers", Effort: "low"},
			{Name: "haiku_r", Model: "haiku", Priority: 1, Enabled: true, Routable: true, RouterLabel: "Casual conversation, simple factual questions, short summaries, translations, dictionary lookups, weather-style queries, small talk, jokes", Effort: "low"},
			{Name: "haiku_rw", Model: "haiku", Priority: 2, Enabled: true, Routable: true, RouterLabel: "Running scheduled jobs, invoking tools (reminders, timers, web search), simple file creation, quick one-line edits, toggling settings", WriteCapable: true, ForceCommand: true, Effort: "low", MaxTurns: 5},
			{Name: "sonnet_r", Model: "sonnet", Priority: 3, Enabled: true, Routable: true, RouterLabel: "Code review, debugging analysis, explaining complex concepts, comparing options, writing structured content (emails, docs), research synthesis, data interpretation", Effort: "medium"},
			{Name: "sonnet_rw", Model: "sonnet", Priority: 4, Enabled: true, Routable: true, RouterLabel: "Editing code files, implementing features, fixing bugs in code, creating scripts, modifying configurations, writing tests, multi-file text changes", WriteCapable: true, ForceCommand: true, Effort: "medium", MaxTurns: 10},
			{Name: "opus_r", Model: "opus", Priority: 5, Enabled: true, Routable: true, RouterLabel: "Architecture design, system-level reasoning, complex trade-off analysis, long-form strategic planning, reviewing entire codebases, deep technical research", Effort: "medium"},
			{Name: "opus_rw", Model: "opus", Priority: 6, Enabled: true, Routable: true, RouterLabel: "Large-scale refactoring across multiple files, implementing complex features with many moving parts, redesigning system architecture, building new modules from scratch", WriteCapable: true, ForceCommand: true, Effort: "medium", MaxTurns: 20},
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
