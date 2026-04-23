package provider

import (
	"testing"
	"time"
)

func TestNewRegistry(t *testing.T) {
	cli := &CLIProvider{}
	r := NewRegistry(cli)
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
	if len(r.BackendNames()) != 0 {
		t.Errorf("expected 0 backends, got %d", len(r.BackendNames()))
	}
}

func TestRegistry_RegisterAndForBackend(t *testing.T) {
	cli := &CLIProvider{}
	r := NewRegistry(cli)

	// ForBackend returns CLI for unknown backends.
	prov := r.ForBackend("nonexistent")
	if prov != cli {
		t.Error("expected CLI fallback for unknown backend")
	}

	// Register an API backend.
	api := NewAPIProviderFromConfig(APIProviderConfig{
		Name:    "test-backend",
		BaseURL: "https://example.com/v1",
		APIKey:  "key123",
		Auth:    "bearer",
	}, nil)
	r.Register("test-backend", api)

	// ForBackend returns the registered provider.
	prov = r.ForBackend("test-backend")
	if prov != api {
		t.Error("expected registered API provider")
	}

	// HasBackend.
	if !r.HasBackend("test-backend") {
		t.Error("expected HasBackend to return true")
	}
	if r.HasBackend("other") {
		t.Error("expected HasBackend to return false for unregistered")
	}

	// BackendNames.
	names := r.BackendNames()
	if len(names) != 1 || names[0] != "test-backend" {
		t.Errorf("expected [test-backend], got %v", names)
	}
}

func TestRegistry_ForBackendCLI(t *testing.T) {
	cli := &CLIProvider{}
	r := NewRegistry(cli)

	// Empty string → CLI.
	if r.ForBackend("") != cli {
		t.Error("expected CLI for empty backend")
	}
	// "cli" → CLI.
	if r.ForBackend("cli") != cli {
		t.Error("expected CLI for 'cli' backend")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	cli := &CLIProvider{}
	r := NewRegistry(cli)
	api := NewAPIProviderFromConfig(APIProviderConfig{
		Name:    "temp",
		BaseURL: "http://localhost",
		Auth:    "none",
	}, nil)
	r.Register("temp", api)
	if !r.HasBackend("temp") {
		t.Fatal("expected backend to be registered")
	}
	r.Unregister("temp")
	if r.HasBackend("temp") {
		t.Error("expected backend to be unregistered")
	}
}

func TestRegistry_GetAPIBackend(t *testing.T) {
	cli := &CLIProvider{}
	r := NewRegistry(cli)

	if r.GetAPIBackend("missing") != nil {
		t.Error("expected nil for missing backend")
	}

	api := NewAPIProviderFromConfig(APIProviderConfig{
		Name:    "ollama",
		BaseURL: "http://localhost:11434/v1",
		Auth:    "none",
	}, nil)
	r.Register("ollama", api)

	got := r.GetAPIBackend("ollama")
	if got != api {
		t.Error("expected registered APIProvider")
	}
}

func TestRegistry_HasOpenRouter_BackwardCompat(t *testing.T) {
	cli := &CLIProvider{}
	r := NewRegistry(cli)
	if r.HasOpenRouter() {
		t.Error("expected false when no openrouter registered")
	}
	api := NewAPIProvider("key", NewHistory(t.TempDir(), 10, time.Hour))
	r.Register("openrouter", api)
	if !r.HasOpenRouter() {
		t.Error("expected true when openrouter registered")
	}
}
