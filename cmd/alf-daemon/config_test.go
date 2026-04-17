package main

import (
	"testing"

	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/scheduler"
)

// staticTierStore is a minimal cc.TierStore backed by a fixed TiersConfig.
type staticTierStore struct{ cfg *cc.TiersConfig }

func (s *staticTierStore) Load() (*cc.TiersConfig, error)  { return s.cfg, nil }
func (s *staticTierStore) Save(_ *cc.TiersConfig) error    { return nil }
func (s *staticTierStore) Current() *cc.TiersConfig        { return s.cfg }
func (s *staticTierStore) Reload() error                   { return nil }
func (s *staticTierStore) SetPath(_ string) error          { return nil }
func (s *staticTierStore) Path() string                    { return "" }

func TestSchedulerTierStore_PreservesNonClaudeModel(t *testing.T) {
	ts := &schedulerTierStore{
		ts: &staticTierStore{cfg: &cc.TiersConfig{
			Tiers: []cc.Tier{
				{Name: "codex-dev", Backend: "codex", Model: "gpt-5.4"},
				{Name: "haiku", Backend: "", Model: "haiku"},
			},
		}},
	}

	snap := ts.Current()
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}

	cases := []struct {
		name      string
		wantModel string
	}{
		// gpt-5.4 is not a Claude alias — must pass through unchanged.
		{"codex-dev", "gpt-5.4"},
		// "haiku" is a Claude alias — must be expanded.
		{"haiku", "claude-haiku-4-5"},
	}

	byName := make(map[string]scheduler.TierInfo)
	for _, t := range snap.Tiers {
		byName[t.Name] = t
	}

	for _, tc := range cases {
		ti, ok := byName[tc.name]
		if !ok {
			t.Fatalf("tier %q not found in snapshot", tc.name)
		}
		if ti.Model != tc.wantModel {
			t.Errorf("tier %q: got model %q, want %q", tc.name, ti.Model, tc.wantModel)
		}
	}
}

func TestResolveBackendAPIKey_AuthNone(t *testing.T) {
	bcfg := cc.BackendConfig{Auth: "none"}
	got := resolveBackendAPIKey("ollama", bcfg, nil)
	if got != "" {
		t.Errorf("auth=none: want empty, got %q", got)
	}
}

func TestResolveBackendAPIKey_NilVault(t *testing.T) {
	bcfg := cc.BackendConfig{Auth: "bearer"}
	got := resolveBackendAPIKey("openai", bcfg, nil)
	if got != "" {
		t.Errorf("nil vault: want empty, got %q", got)
	}
}

func TestResolveBackendAPIKey_DefaultAuthNilVault(t *testing.T) {
	// Auth field empty (defaults to bearer at registration time) — still needs vault.
	bcfg := cc.BackendConfig{}
	got := resolveBackendAPIKey("anthropic", bcfg, nil)
	if got != "" {
		t.Errorf("default auth + nil vault: want empty, got %q", got)
	}
}

func TestRegisterBackends_OpenRouterDefaultHeaders(t *testing.T) {
	// When BaseURL contains "openrouter.ai" and no headers configured,
	// registerBackends should inject default app identification headers.
	registry := provider.NewRegistry(nil)
	cfg := &cc.Config{
		Backends: map[string]cc.BackendConfig{
			"openrouter": {
				BaseURL: "https://openrouter.ai/api/v1",
				Auth:    "none", // bypass vault requirement
			},
		},
	}

	registerBackends(registry, cfg, nil, nil)

	p := registry.GetAPIBackend("openrouter")
	if p == nil {
		t.Fatal("expected openrouter backend to be registered")
	}
	headers := p.Headers()
	if headers["HTTP-Referer"] != "https://alfos.ai" {
		t.Errorf("expected HTTP-Referer 'https://alfos.ai', got %q", headers["HTTP-Referer"])
	}
	if headers["X-Title"] != "Alf" {
		t.Errorf("expected X-Title 'Alf', got %q", headers["X-Title"])
	}
}

func TestRegisterBackends_OpenRouterCustomHeadersPreserved(t *testing.T) {
	// When headers are explicitly configured, they should NOT be overridden.
	registry := provider.NewRegistry(nil)
	cfg := &cc.Config{
		Backends: map[string]cc.BackendConfig{
			"openrouter": {
				BaseURL: "https://openrouter.ai/api/v1",
				Auth:    "none",
				Headers: map[string]string{"HTTP-Referer": "https://custom.dev", "X-Title": "Custom"},
			},
		},
	}

	registerBackends(registry, cfg, nil, nil)

	p := registry.GetAPIBackend("openrouter")
	if p == nil {
		t.Fatal("expected openrouter backend to be registered")
	}
	headers := p.Headers()
	if headers["HTTP-Referer"] != "https://custom.dev" {
		t.Errorf("expected custom HTTP-Referer, got %q", headers["HTTP-Referer"])
	}
	if headers["X-Title"] != "Custom" {
		t.Errorf("expected custom X-Title, got %q", headers["X-Title"])
	}
}

func TestRegisterBackends_NonOpenRouterNoDefaultHeaders(t *testing.T) {
	// Non-OpenRouter backends should NOT get default headers.
	registry := provider.NewRegistry(nil)
	cfg := &cc.Config{
		Backends: map[string]cc.BackendConfig{
			"ollama": {
				BaseURL: "http://localhost:11434/v1",
				Auth:    "none",
			},
		},
	}

	registerBackends(registry, cfg, nil, nil)

	p := registry.GetAPIBackend("ollama")
	if p == nil {
		t.Fatal("expected ollama backend to be registered")
	}
	headers := p.Headers()
	if len(headers) != 0 {
		t.Errorf("expected no headers for non-OpenRouter backend, got %v", headers)
	}
}
