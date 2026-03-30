package marketplace

import (
	"encoding/json"
	"fmt"
	"os"
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

// UntrustedMaxPermissions are the only permissions allowed for untrusted apps.
var UntrustedMaxPermissions = map[string]bool{
	"storage":   true,
	"events":    true,
	"clipboard": true,
}

// CapPermissionsForUntrusted restricts permissions to the safe set.
// Returns the filtered list. If perms is nil (legacy/no field), returns nil unchanged.
func CapPermissionsForUntrusted(perms []string) []string {
	if perms == nil {
		return nil
	}
	capped := make([]string, 0, len(perms))
	for _, p := range perms {
		if UntrustedMaxPermissions[p] {
			capped = append(capped, p)
		}
	}
	return capped
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

func init() {
	// SEC-006: Compile-time check that UntrustedMaxPermissions is a subset of ValidPermissions.
	for p := range UntrustedMaxPermissions {
		if !ValidPermissions[p] {
			panic("UntrustedMaxPermissions contains unknown permission: " + p)
		}
	}
}
