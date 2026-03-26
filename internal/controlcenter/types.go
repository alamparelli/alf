package controlcenter

import (
	_ "embed"
	"encoding/json"
	"sync/atomic"
	"time"
)

//go:embed defaults/tiers.json
var defaultTiersJSON []byte

// BackendConfig defines an OpenAI-compatible LLM API endpoint.
type BackendConfig struct {
	BaseURL      string            `json:"base_url"`
	VaultService string            `json:"vault_service,omitempty"` // vault service name for API key
	Headers      map[string]string `json:"headers,omitempty"`       // custom headers (e.g. HTTP-Referer)
	Auth         string            `json:"auth,omitempty"`          // "bearer" (default), "none" (Ollama)
	DefaultModel string            `json:"default_model,omitempty"` // model if tier doesn't specify one
	MaxTokens    int               `json:"max_tokens,omitempty"`    // 0 = 4096
	InputPrice   float64           `json:"input_price,omitempty"`   // cost per 1M input tokens (USD)
	OutputPrice  float64           `json:"output_price,omitempty"`  // cost per 1M output tokens (USD)
}

// Config holds non-secret runtime parameters.
type Config struct {
	LogLevel       string     `json:"log_level"`
	AllowedChatIDs []int64    `json:"allowed_chat_ids"`
	SystemPrompt   string     `json:"system_prompt"`
	QuietHours     QuietHours `json:"quiet_hours"`
	SessionTimeout   int        `json:"session_timeout"`    // minutes, 0 = no timeout
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
	Backends                map[string]BackendConfig `json:"backends,omitempty"` // named API backends
	// Memory extraction settings.
	MemoryEnabled              *bool   `json:"memory_enabled"`                // enable memory system, nil = true
	MemoryExtractInterval      int     `json:"memory_extract_interval"`       // minutes between extractions, 0 = 180
	MemoryExtractTimeout       int     `json:"memory_extract_timeout"`        // seconds for extraction call, 0 = 300
	MemoryExtractBootDelay     int     `json:"memory_extract_boot_delay"`     // seconds before first extraction, 0 = 180
	MemoryExtractMinMessages   int     `json:"memory_extract_min_messages"`   // min message pairs to trigger, 0 = 3
	MemoryDedupTextThreshold   float64 `json:"memory_dedup_text_threshold"`   // Jaccard similarity threshold, 0 = 0.7
	MemoryDedupCosineThreshold float64 `json:"memory_dedup_cosine_threshold"` // cosine distance threshold, 0 = 0.15
	// BroadcastChannel sets the default channel for system alerts (e.g., vault token expiry).
	// Values: "all" (default, sends to all adapters), "tg", "cc".
	BroadcastChannel string `json:"broadcast_channel,omitempty"`
	// TiersFile overrides the default tiers.json filename. Relative paths are
	// resolved against config.d/. Absolute paths are used as-is.
	// Empty (default) means tiers.json.
	TiersFile string `json:"tiers_file,omitempty"`
	// DNSServers overrides /etc/resolv.conf nameservers. Required for gVisor
	// which cannot use Docker's internal DNS (127.0.0.11).
	// Empty = ["8.8.8.8", "1.1.1.1"].
	DNSServers []string `json:"dns_servers,omitempty"`
	// NotificationSound enables audio alerts for CC chat messages, nil = true.
	NotificationSound *bool `json:"notification_sound"`
	// RecallLimit is the max number of memories injected per message, 0 = default (5).
	RecallLimit int `json:"recall_limit,omitempty"`
	// RecallDistance is the max cosine distance for recall results, 0 = default (1.0).
	RecallDistance float64 `json:"recall_distance,omitempty"`
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
		TiersFile:               "tiers.json",
		NotificationSound:       boolPtr(true),
	}
}

func boolPtr(v bool) *bool { return &v }

// EffectiveMemoryEnabled returns whether the memory system is enabled.
func (c *Config) EffectiveMemoryEnabled() bool {
	if c.MemoryEnabled != nil {
		return *c.MemoryEnabled
	}
	return true
}

// EffectiveMemoryExtractInterval returns the extraction interval in minutes.
func (c *Config) EffectiveMemoryExtractInterval() int {
	if c.MemoryExtractInterval > 0 {
		return c.MemoryExtractInterval
	}
	return 180
}

// EffectiveMemoryExtractTimeout returns the extraction timeout in seconds.
func (c *Config) EffectiveMemoryExtractTimeout() int {
	if c.MemoryExtractTimeout > 0 {
		return c.MemoryExtractTimeout
	}
	return 300
}

// EffectiveMemoryExtractBootDelay returns the boot delay in seconds.
func (c *Config) EffectiveMemoryExtractBootDelay() int {
	if c.MemoryExtractBootDelay > 0 {
		return c.MemoryExtractBootDelay
	}
	return 180
}

// EffectiveMemoryExtractMinMessages returns the minimum messages to trigger extraction.
func (c *Config) EffectiveMemoryExtractMinMessages() int {
	if c.MemoryExtractMinMessages > 0 {
		return c.MemoryExtractMinMessages
	}
	return 3
}

// EffectiveMemoryDedupTextThreshold returns the Jaccard similarity threshold.
func (c *Config) EffectiveMemoryDedupTextThreshold() float64 {
	if c.MemoryDedupTextThreshold > 0 {
		return c.MemoryDedupTextThreshold
	}
	return 0.7
}

// EffectiveMemoryDedupCosineThreshold returns the cosine distance threshold.
func (c *Config) EffectiveMemoryDedupCosineThreshold() float64 {
	if c.MemoryDedupCosineThreshold > 0 {
		return c.MemoryDedupCosineThreshold
	}
	return 0.15
}

// DefaultDNSServers are used when Config.DNSServers is empty.
var DefaultDNSServers = []string{"8.8.8.8", "1.1.1.1"}

// EffectiveDNS returns the DNS servers to use, falling back to defaults.
func (c *Config) EffectiveDNS() []string {
	if len(c.DNSServers) > 0 {
		return c.DNSServers
	}
	return DefaultDNSServers
}

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
	ForceCommand   bool     `json:"force_command"`
	Backend        string   `json:"backend,omitempty"`        // "cli" (default), or registered backend name
	SystemPrompt   string   `json:"system_prompt,omitempty"`  // extra system prompt prepended for this tier
	ContextWeight  string   `json:"context_weight,omitempty"` // "light", "standard", "full" (default)
}

// EffectiveContextWeight returns the tier's context weight, defaulting to "full".
func (t Tier) EffectiveContextWeight() string {
	switch t.ContextWeight {
	case "light", "standard", "full":
		return t.ContextWeight
	default:
		return "full"
	}
}

// RouterDescription returns Description if set, otherwise falls back to RouterLabel.
func (t Tier) RouterDescription() string {
	if t.Description != "" {
		return t.Description
	}
	return t.RouterLabel
}

// EmbeddingConfig defines the embedding service for semantic memory search.
type EmbeddingConfig struct {
	URL   string `json:"url,omitempty"`   // e.g. "http://embed:8090" or vault-proxy URL
	Model string `json:"model,omitempty"` // for external APIs: "text-embedding-3-small"
}

// MemoryConfig defines memory extraction and embedding settings at the profile level.
type MemoryConfig struct {
	// ExtractBackend selects which backend to use for LLM-based fact extraction.
	// Empty = auto (prefer authenticated API, fallback to CLI).
	// "cli" = always use Claude CLI. Any registered backend name = use that.
	ExtractBackend string `json:"extract_backend,omitempty"`
	// ExtractModel overrides the model used for extraction (e.g. "anthropic/claude-haiku-4-5").
	ExtractModel string `json:"extract_model,omitempty"`
	// Embedding configures the vector embedding service.
	Embedding *EmbeddingConfig `json:"embedding,omitempty"`
}

// TiersConfig wraps a list of tiers plus router-level settings.
type TiersConfig struct {
	Tiers              []Tier           `json:"tiers"`
	RouterModel        string           `json:"router_model,omitempty"`
	DefaultFallback    string           `json:"default_fallback,omitempty"`
	RouterDistinctions string           `json:"router_distinctions,omitempty"`
	RouterBackend      string           `json:"router_backend,omitempty"` // "cli" (default), or registered backend name
	Embedding          *EmbeddingConfig `json:"embedding,omitempty"`      // deprecated: use Memory.Embedding
	Memory             *MemoryConfig    `json:"memory,omitempty"`
}

// DefaultTiersConfig returns a TiersConfig parsed from the embedded defaults/tiers.json.
func DefaultTiersConfig() *TiersConfig {
	var cfg TiersConfig
	if err := json.Unmarshal(defaultTiersJSON, &cfg); err != nil {
		// Should never happen - embedded file is validated at build time.
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

// AllowedBackends is populated at runtime from registered backends.
// "" and "cli" are always valid; additional backends come from config.
var AllowedBackends = map[string]bool{
	"":    true,
	"cli": true,
}

// SetAllowedBackends updates the allowed backends set with registered backend names.
func SetAllowedBackends(names []string) {
	m := map[string]bool{"": true, "cli": true}
	for _, n := range names {
		m[n] = true
	}
	AllowedBackends = m
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
	Status          string         `json:"status"`
	Uptime          string         `json:"uptime"`
	MessageCount    int64          `json:"message_count"`
	LastMessage     *string        `json:"last_message"`
	Version         string         `json:"version"`
	UpdateAvailable string         `json:"update_available,omitempty"` // latest version if newer release detected
	Session         *SessionStatus `json:"session,omitempty"`
}

// SessionStatus summarizes the current chat session.
type SessionStatus struct {
	ID           string  `json:"id"`
	MessageCount int     `json:"message_count"`
	CostUSD      float64 `json:"cost_usd"`
}
