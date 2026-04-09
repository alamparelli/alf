package main

import (
	"testing"

	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/provider"
)

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
