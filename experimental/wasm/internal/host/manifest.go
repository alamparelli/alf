package host

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Kind distinguishes a Tool (one-shot, CGI-style) from an App (HTTP handler).
type Kind string

const (
	KindTool Kind = "tool"
	KindApp  Kind = "app"
)

// Manifest is the single source of truth for what a capability can do.
// Every host-exposed capability (log, storage, vault, http, memory, events)
// must be declared here; otherwise it is absent from the instantiated module.
type Manifest struct {
	Name        string      `toml:"name"`
	Version     string      `toml:"version"`
	Kind        Kind        `toml:"kind"`
	Entry       string      `toml:"entry"`       // path to .wasm, relative to manifest
	Runtime     string      `toml:"runtime"`     // "go-wasip1" for this spike
	Description string      `toml:"description"`
	Permissions Permissions `toml:"permissions"`
}

// Permissions lists every host capability the guest may invoke.
// Absence = denied by construction (the import is not linked into the module).
type Permissions struct {
	Log     bool     `toml:"log"`     // log.info / log.error
	Storage bool     `toml:"storage"` // scoped KV store for this capability
	Vault   []string `toml:"vault"`   // allowed vault services, e.g. ["coingecko"]
	HTTP    []string `toml:"http"`    // allowed hostnames for raw http fetch, e.g. ["api.example.com"]
	Memory  bool     `toml:"memory"`  // access to long-term memory store (stubbed)
	Events  bool     `toml:"events"`  // emit inter-capability events (stubbed)
}

// Load parses a manifest.toml file and returns a validated Manifest.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest %s: %w", path, err)
	}
	return &m, nil
}

// Validate enforces minimum invariants on a manifest.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("name is required")
	}
	if m.Version == "" {
		return fmt.Errorf("version is required")
	}
	if m.Kind != KindTool && m.Kind != KindApp {
		return fmt.Errorf(`kind must be "tool" or "app", got %q`, m.Kind)
	}
	if m.Entry == "" {
		return fmt.Errorf("entry (.wasm path) is required")
	}
	if m.Runtime == "" {
		m.Runtime = "go-wasip1"
	}
	return nil
}
