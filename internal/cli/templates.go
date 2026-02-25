package cli

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed templates/*
var templateFS embed.FS

// RenderDockerCompose writes docker-compose.yml.
func RenderDockerCompose(dir string) error {
	src, err := templateFS.ReadFile("templates/docker-compose.yml.tmpl")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "docker-compose.yml"), src, 0o644)
}

// RenderConfig writes config.yaml.
func RenderConfig(dir string) error {
	src, err := templateFS.ReadFile("templates/config.yaml.tmpl")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.yaml"), src, 0o644)
}
