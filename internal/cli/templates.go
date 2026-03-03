package cli

import (
	"bytes"
	"embed"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed templates/*
var templateFS embed.FS

// ComposeData holds values for the docker-compose template.
type ComposeData struct {
	Image         string // Docker image (default: ghcr.io/alamparelli/alf:latest)
	CCPort        string
	CCExternalURL string
	EnableHTTPS   bool
	Domain        string
	AcmeEmail     string
}

// RenderDockerCompose writes docker-compose.yml with the given port.
func RenderDockerCompose(dir string, data ComposeData) error {
	src, err := templateFS.ReadFile("templates/docker-compose.yml.tmpl")
	if err != nil {
		return err
	}
	tmpl, err := template.New("compose").Parse(string(src))
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "docker-compose.yml"), buf.Bytes(), 0o644)
}

// ConfigData holds values for the config.json template.
type ConfigData struct {
	ChatID string
}

// RenderConfig writes config.json inside the config.d directory.
func RenderConfig(dir string, data ConfigData) error {
	src, err := templateFS.ReadFile("templates/config.json.tmpl")
	if err != nil {
		return err
	}
	tmpl, err := template.New("config").Parse(string(src))
	if err != nil {
		return err
	}
	configD := filepath.Join(dir, "config.d")
	if err := os.MkdirAll(configD, 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(configD, "config.json"), buf.Bytes(), 0o644)
}
