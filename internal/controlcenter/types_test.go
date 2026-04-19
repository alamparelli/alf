package controlcenter

import (
	"strings"
	"testing"
	"time"
)

func TestConfig_EffectiveMemoryEnabled(t *testing.T) {
	c := &Config{}
	if !c.EffectiveMemoryEnabled() {
		t.Error("nil pointer should default to true")
	}
	f := false
	c.MemoryEnabled = &f
	if c.EffectiveMemoryEnabled() {
		t.Error("false pointer should return false")
	}
	tr := true
	c.MemoryEnabled = &tr
	if !c.EffectiveMemoryEnabled() {
		t.Error("true pointer should return true")
	}
}

func TestConfig_EffectiveMemoryExtractInterval(t *testing.T) {
	c := &Config{}
	if got := c.EffectiveMemoryExtractInterval(); got != DefaultMemoryExtractInterval {
		t.Errorf("expected default %d, got %d", DefaultMemoryExtractInterval, got)
	}
	c.MemoryExtractInterval = 42
	if got := c.EffectiveMemoryExtractInterval(); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

func TestConfig_EffectiveMemoryExtractTimeout(t *testing.T) {
	c := &Config{}
	if c.EffectiveMemoryExtractTimeout() != DefaultMemoryExtractTimeout {
		t.Error("expected default")
	}
	c.MemoryExtractTimeout = 99
	if c.EffectiveMemoryExtractTimeout() != 99 {
		t.Error("override not applied")
	}
}

func TestConfig_EffectiveMemoryExtractBootDelay(t *testing.T) {
	c := &Config{}
	if c.EffectiveMemoryExtractBootDelay() != DefaultMemoryExtractBootDelay {
		t.Error("expected default")
	}
	c.MemoryExtractBootDelay = 5
	if c.EffectiveMemoryExtractBootDelay() != 5 {
		t.Error("override not applied")
	}
}

func TestConfig_EffectiveMemoryExtractMinMessages(t *testing.T) {
	c := &Config{}
	if c.EffectiveMemoryExtractMinMessages() != DefaultMemoryExtractMinMessages {
		t.Error("expected default")
	}
	c.MemoryExtractMinMessages = 7
	if c.EffectiveMemoryExtractMinMessages() != 7 {
		t.Error("override not applied")
	}
}

func TestConfig_EffectiveMemoryDedupTextThreshold(t *testing.T) {
	c := &Config{}
	if c.EffectiveMemoryDedupTextThreshold() != DefaultMemoryDedupTextThreshold {
		t.Error("expected default")
	}
	c.MemoryDedupTextThreshold = 0.9
	if c.EffectiveMemoryDedupTextThreshold() != 0.9 {
		t.Error("override not applied")
	}
}

func TestConfig_EffectiveMemoryDedupCosineThreshold(t *testing.T) {
	c := &Config{}
	if c.EffectiveMemoryDedupCosineThreshold() != DefaultMemoryDedupCosineThreshold {
		t.Error("expected default")
	}
	c.MemoryDedupCosineThreshold = 0.25
	if c.EffectiveMemoryDedupCosineThreshold() != 0.25 {
		t.Error("override not applied")
	}
}

func TestConfig_EffectiveSummarizationEnabled(t *testing.T) {
	c := &Config{}
	if !c.EffectiveSummarizationEnabled() {
		t.Error("nil pointer should default to true")
	}
	f := false
	c.SummarizationEnabled = &f
	if c.EffectiveSummarizationEnabled() {
		t.Error("false pointer should return false")
	}
}

func TestConfig_EffectiveSummarizationThreshold(t *testing.T) {
	c := &Config{}
	if c.EffectiveSummarizationThreshold() != DefaultSummarizationThreshold {
		t.Error("expected default")
	}
	c.SummarizationThreshold = 50
	if c.EffectiveSummarizationThreshold() != 50 {
		t.Error("override not applied")
	}
}

func TestConfig_EffectiveSummarizationKeepLast(t *testing.T) {
	c := &Config{}
	if c.EffectiveSummarizationKeepLast() != DefaultSummarizationKeepLast {
		t.Error("expected default")
	}
	c.SummarizationKeepLast = 3
	if c.EffectiveSummarizationKeepLast() != 3 {
		t.Error("override not applied")
	}
}

func TestTier_EffectiveContextWeight(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"light", "light"},
		{"standard", "standard"},
		{"full", "full"},
		{"", "full"},
		{"bogus", "full"},
	}
	for _, tt := range tests {
		tier := Tier{ContextWeight: tt.in}
		if got := tier.EffectiveContextWeight(); got != tt.want {
			t.Errorf("Tier{ContextWeight=%q}.EffectiveContextWeight() = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTier_RouterDescription(t *testing.T) {
	// Description wins when set.
	tier := Tier{Description: "desc", RouterLabel: "label"}
	if got := tier.RouterDescription(); got != "desc" {
		t.Errorf("Description should win, got %q", got)
	}
	// Falls back to RouterLabel.
	tier = Tier{RouterLabel: "label"}
	if got := tier.RouterDescription(); got != "label" {
		t.Errorf("expected label fallback, got %q", got)
	}
	// Both empty.
	tier = Tier{}
	if got := tier.RouterDescription(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestTiersConfig_IsOrchestratorTier(t *testing.T) {
	c := &TiersConfig{
		Tiers: []Tier{
			{Name: "fast", Role: ""},
			{Name: "agent", Role: "orchestrator"},
		},
	}
	if !c.IsOrchestratorTier("agent") {
		t.Error("agent should be orchestrator")
	}
	if c.IsOrchestratorTier("fast") {
		t.Error("fast is not an orchestrator")
	}
	if c.IsOrchestratorTier("missing") {
		t.Error("unknown tier must return false")
	}
}

func TestDefaultTiersJSON_NonEmpty(t *testing.T) {
	raw := DefaultTiersJSON()
	if len(raw) == 0 {
		t.Fatal("DefaultTiersJSON returned empty")
	}
	// Must be valid JSON that the parser accepts.
	cfg := DefaultTiersConfig()
	if cfg == nil || len(cfg.Tiers) == 0 {
		t.Error("DefaultTiersConfig should have tiers")
	}
	// Raw bytes must reference at least one tier name from the parsed struct.
	if !strings.Contains(string(raw), cfg.Tiers[0].Name) {
		t.Errorf("raw JSON missing the parsed tier %q", cfg.Tiers[0].Name)
	}
}

func TestStats_RecordMessage(t *testing.T) {
	s := NewStats()
	if s.MessageCount.Load() != 0 {
		t.Fatalf("expected 0, got %d", s.MessageCount.Load())
	}
	if s.LastMessage.Load() != nil {
		t.Error("LastMessage should be nil initially")
	}

	s.RecordMessage()
	s.RecordMessage()
	s.RecordMessage()

	if got := s.MessageCount.Load(); got != 3 {
		t.Errorf("expected 3 messages, got %d", got)
	}
	last := s.LastMessage.Load()
	if last == nil {
		t.Fatal("LastMessage should be set after RecordMessage")
	}
	if time.Since(*last) > time.Second {
		t.Errorf("LastMessage should be recent, got %s ago", time.Since(*last))
	}
	if s.StartedAt.After(time.Now()) {
		t.Error("StartedAt should be in the past")
	}
}

func TestDefaultConfig_SaneDefaults(t *testing.T) {
	c := DefaultConfig()
	if c.SessionTimeout != 30 {
		t.Errorf("unexpected session timeout: %d", c.SessionTimeout)
	}
	if c.TiersFile != "tiers.json" {
		t.Errorf("unexpected TiersFile: %q", c.TiersFile)
	}
	if c.ShowSkillFooter == nil || !*c.ShowSkillFooter {
		t.Error("ShowSkillFooter should default to true")
	}
	if c.NotificationSound == nil || !*c.NotificationSound {
		t.Error("NotificationSound should default to true")
	}
	if c.LogLevel != "info" {
		t.Errorf("unexpected log level: %q", c.LogLevel)
	}
}
