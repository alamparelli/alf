package marketplace

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/alamparelli/alf/internal/sandbox"
)

type Manifest struct {
	Name        string     `json:"name"`
	Slug        string     `json:"slug"`
	Version     string     `json:"version"`
	Description string     `json:"description"`
	Author      string     `json:"author"`
	Category    string     `json:"category"`
	Icon        string     `json:"icon"`
	Tools       []ToolDecl `json:"tools"`
	Permissions []string   `json:"permissions,omitempty"`
	Services    []string   `json:"services,omitempty"` // vault services this app needs (e.g. ["openrouter"])
	Trusted     bool       `json:"trusted,omitempty"`  // only settable by marketplace registry
}

// UntrustedMaxPermissions re-exports the sandbox-owned allow-list.
var UntrustedMaxPermissions = sandbox.UntrustedMaxPermissions

// CapPermissionsForUntrusted re-exports sandbox.CapPermissionsForUntrusted.
func CapPermissionsForUntrusted(perms []string) []string {
	return sandbox.CapPermissionsForUntrusted(perms)
}

type ToolDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Action      string         `json:"action"`
	Parameters  map[string]any `json:"parameters"`
}

func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	// SEC-001: Strip Trusted from file — trust is determined by the registry,
	// not by the manifest on disk (which the LLM subprocess can edit).
	m.Trusted = false

	return &m, nil
}

