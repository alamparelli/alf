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

	return &m, nil
}
