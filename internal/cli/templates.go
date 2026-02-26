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
	CCPort string
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

// RenderConfig writes config.json.
func RenderConfig(dir string) error {
	src, err := templateFS.ReadFile("templates/config.json.tmpl")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), src, 0o644)
}
