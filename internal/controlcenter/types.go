package controlcenter

import (
	_ "embed"
	"encoding/json"
	"sync/atomic"
	"time"
)

//go:embed defaults/tiers.json
var defaultTiersJSON []byte

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
	ShowSkillFooter         *bool  `json:"show_skill_footer"`          // show active skills in message footer, nil = true (default on)
	MaxSessions             int    `json:"max_sessions"`               // max concurrent sessions per user, 0 = default (2)
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
		GitSweepInterval: 15,
		AutoUpdateCheck:         true,
		AutoUpdateCheckInterval: 21600,
		AutoUpdateNotify:        true,
		AuthBanThreshold:        10,
		AuthBanDuration:         15,
		TiersTimeout:            300,
		ShowSkillFooter:         boolPtr(true),
	}
}

func boolPtr(v bool) *bool { return &v }

// Tier defines a routing tier for message processing.
type Tier struct {
	Name         string   `json:"name"`
	Model        string   `json:"model"`
	Priority     int      `json:"priority"`
	Enabled      bool     `json:"enabled"`
	Routable     bool     `json:"routable"`
	RouterLabel  string   `json:"router_label,omitempty"`
	Description  string   `json:"description,omitempty"`
	MaxTurns              int      `json:"max_turns,omitempty"`
	OrchestratorMaxTurns  int      `json:"orchestrator_max_turns,omitempty"` // turns per orchestrator iteration (agent tier only)
	WriteCapable          bool     `json:"write_capable"`
	Tools                 []string `json:"tools,omitempty"`
	Effort                string   `json:"effort,omitempty"`
	MaxIterations         int      `json:"max_iterations,omitempty"`
	TimeoutMin            int      `json:"timeout_minutes,omitempty"`
	ForceCommand  bool     `json:"force_command"`
	Backend       string   `json:"backend,omitempty"` // "cli" (default), "openrouter"
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
	RouterDistinctions string `json:"router_distinctions,omitempty"`
	RouterBackend      string `json:"router_backend,omitempty"` // "cli" (default), "openrouter"
}

// DefaultTiersConfig returns a TiersConfig parsed from the embedded defaults/tiers.json.
func DefaultTiersConfig() *TiersConfig {
	var cfg TiersConfig
	if err := json.Unmarshal(defaultTiersJSON, &cfg); err != nil {
		// Should never happen — embedded file is validated at build time.
		panic("controlcenter: invalid embedded tiers.json: " + err.Error())
	}
	return &cfg
}

// DefaultTiersJSON returns the raw embedded tiers.json bytes.
func DefaultTiersJSON() []byte {
	return defaultTiersJSON
}

// AllowedModels defines valid model names for tier validation.
var AllowedModels = map[string]bool{
	"haiku":  true,
	"sonnet": true,
	"opus":   true,
}

// AllowedBackends defines valid backend values (empty string = default CLI).
var AllowedBackends = map[string]bool{
	"":           true,
	"cli":        true,
	"openrouter": true,
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
	ReloadAgents
	ReloadFirewall
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
	Status       string         `json:"status"`
	Uptime       string         `json:"uptime"`
	MessageCount int64          `json:"message_count"`
	LastMessage  *string        `json:"last_message"`
	Version      string         `json:"version"`
	Session      *SessionStatus `json:"session,omitempty"`
}

// SessionStatus summarizes the current chat session.
type SessionStatus struct {
	ID           string  `json:"id"`
	MessageCount int     `json:"message_count"`
	CostUSD      float64 `json:"cost_usd"`
}
