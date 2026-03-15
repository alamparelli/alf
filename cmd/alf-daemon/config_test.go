package main

import (
	"testing"

	cc "github.com/alamparelli/alf/internal/controlcenter"
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
