package controlcenter

import (
	"encoding/json"
	"testing"
)

func TestBackendConfig_JSON(t *testing.T) {
	cfg := BackendConfig{
		BaseURL:      "https://openrouter.ai/api/v1",
		VaultService: "openrouter",
		Headers:      map[string]string{"HTTP-Referer": "https://alf.dev"},
		DefaultModel: "anthropic/claude-haiku-4-5",
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var decoded BackendConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.BaseURL != cfg.BaseURL {
		t.Errorf("expected base_url %q, got %q", cfg.BaseURL, decoded.BaseURL)
	}
	if decoded.VaultService != cfg.VaultService {
		t.Errorf("expected vault_service %q, got %q", cfg.VaultService, decoded.VaultService)
	}
	if decoded.Headers["HTTP-Referer"] != "https://alf.dev" {
		t.Error("expected custom header")
	}
}

func TestBackendConfig_AuthNone(t *testing.T) {
	cfg := BackendConfig{
		BaseURL: "http://localhost:11434/v1",
		Auth:    "none",
	}
	data, _ := json.Marshal(cfg)
	var decoded BackendConfig
	json.Unmarshal(data, &decoded)
	if decoded.Auth != "none" {
		t.Errorf("expected auth 'none', got %q", decoded.Auth)
	}
}

func TestConfig_BackendsOmittedWhenEmpty(t *testing.T) {
	cfg := DefaultConfig()
	data, _ := json.MarshalIndent(cfg, "", "  ")
	// Backends field should be omitted (omitempty).
	if json.Valid(data) {
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, exists := m["backends"]; exists {
			t.Error("expected backends to be omitted when nil")
		}
	}
}

func TestConfig_BackendsRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Backends = map[string]BackendConfig{
		"openrouter": {
			BaseURL:      "https://openrouter.ai/api/v1",
			VaultService: "openrouter",
			DefaultModel: "anthropic/claude-haiku-4-5",
		},
		"ollama": {
			BaseURL:      "http://host.docker.internal:11434/v1",
			Auth:         "none",
			DefaultModel: "llama3.2",
		},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(decoded.Backends) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(decoded.Backends))
	}
	if decoded.Backends["ollama"].Auth != "none" {
		t.Error("expected ollama auth=none")
	}
}

func TestSetAllowedBackends(t *testing.T) {
	// Reset to base.
	SetAllowedBackends(nil)
	if !AllowedBackends[""] {
		t.Error("empty string should always be allowed")
	}
	if !AllowedBackends["cli"] {
		t.Error("cli should always be allowed")
	}
	if AllowedBackends["openrouter"] {
		t.Error("openrouter should not be allowed before registration")
	}

	// Register backends.
	SetAllowedBackends([]string{"openrouter", "ollama"})
	if !AllowedBackends["openrouter"] {
		t.Error("openrouter should be allowed after registration")
	}
	if !AllowedBackends["ollama"] {
		t.Error("ollama should be allowed after registration")
	}
	if !AllowedBackends["cli"] {
		t.Error("cli should still be allowed")
	}
}

func TestValidateTiersConfig_APIBackend(t *testing.T) {
	// Register a backend to make it valid.
	SetAllowedBackends([]string{"openrouter", "ollama"})
	defer SetAllowedBackends(nil)

	cfg := &TiersConfig{
		Tiers: []Tier{
			{Name: "fast", Model: "anthropic/claude-haiku-4-5", Backend: "openrouter", Enabled: true},
			{Name: "local", Model: "llama3.2", Backend: "ollama", Enabled: true},
			{Name: "cli-tier", Model: "haiku", Backend: "", Enabled: true},
		},
	}
	if err := validateTiersConfig(cfg); err != nil {
		t.Errorf("expected valid config, got: %v", err)
	}
}

func TestValidateTiersConfig_UnknownBackend(t *testing.T) {
	SetAllowedBackends(nil) // Only "" and "cli"
	defer SetAllowedBackends(nil)

	cfg := &TiersConfig{
		Tiers: []Tier{
			{Name: "test", Model: "m", Backend: "unknown"},
		},
	}
	if err := validateTiersConfig(cfg); err == nil {
		t.Error("expected validation error for unknown backend")
	}
}

func TestValidateTiersConfig_APIModelSkipsValidation(t *testing.T) {
	SetAllowedBackends([]string{"openrouter"})
	defer SetAllowedBackends(nil)

	// API backends can use any model string.
	cfg := &TiersConfig{
		Tiers: []Tier{
			{Name: "test", Model: "google/gemini-2.0-flash", Backend: "openrouter", Enabled: true},
		},
	}
	if err := validateTiersConfig(cfg); err != nil {
		t.Errorf("API backend model should skip validation, got: %v", err)
	}
}

func TestValidateTiersConfig_CLIModelValidation(t *testing.T) {
	SetAllowedBackends(nil)

	// CLI backend requires known model names.
	cfg := &TiersConfig{
		Tiers: []Tier{
			{Name: "test", Model: "unknown-model", Backend: "", Enabled: true},
		},
	}
	if err := validateTiersConfig(cfg); err == nil {
		t.Error("expected validation error for unknown CLI model")
	}

	// Valid CLI model.
	cfg.Tiers[0].Model = "haiku"
	if err := validateTiersConfig(cfg); err != nil {
		t.Errorf("expected valid CLI model, got: %v", err)
	}
}
