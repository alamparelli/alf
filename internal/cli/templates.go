package cli

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"

	"github.com/alamparelli/alf/internal/controlcenter"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed bundled_skills/*
var bundledSkillsFS embed.FS

//go:embed bundled_agents/*
var bundledAgentsFS embed.FS

// ComposeData holds values for the docker-compose template.
type ComposeData struct {
	Image         string   // Docker image (default: ghcr.io/alamparelli/alf:latest)
	CCPort        string
	CCExternalURL string
	EnableHTTPS   bool
	Domain        string
	AcmeEmail     string
	Timezone      string   // IANA timezone (e.g. "Europe/Brussels")
	Workspaces    []string // Host paths mounted as workspaces under /workspaces/<basename>
	JSRuntime     string   // "node", "deno", "bun", or "" (none)
}

// RenderDockerCompose writes docker-compose.yml with the given port.
func RenderDockerCompose(dir string, data ComposeData) error {
	src, err := templateFS.ReadFile("templates/docker-compose.yml.tmpl")
	if err != nil {
		return err
	}
	funcs := template.FuncMap{"base": filepath.Base}
	tmpl, err := template.New("compose").Funcs(funcs).Parse(string(src))
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
	ChatID   string
	Timezone string
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

// SeedBundledSkills copies embedded skills into the skills.d directory.
// Existing files are not overwritten (preserves user modifications).
func SeedBundledSkills(dir string) error {
	skillsDir := filepath.Join(dir, "skills.d")
	return seedEmbedded(bundledSkillsFS, "bundled_skills", skillsDir)
}

// SeedBundledAgents copies embedded agent teams into the agents directory.
// Existing files are not overwritten (preserves user modifications).
func SeedBundledAgents(dir string) error {
	// Agents live inside config.d/ which is mounted as /opt/alf/config.d in the container.
	agentsDir := filepath.Join(dir, "config.d", "agents")
	return seedEmbedded(bundledAgentsFS, "bundled_agents", agentsDir)
}

// SeedTiersConfig writes the default tiers.json if it doesn't already exist.
func SeedTiersConfig(dir string) error {
	configD := filepath.Join(dir, "config.d")
	if err := os.MkdirAll(configD, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(configD, "tiers.json")
	if _, err := os.Stat(dest); err == nil {
		return nil // already exists, don't overwrite
	}
	return os.WriteFile(dest, controlcenter.DefaultTiersJSON(), 0o644)
}

// SeedBootstrapScript writes data/bootstrap.sh if it doesn't already exist.
// This script runs automatically at daemon startup when modified.
func SeedBootstrapScript(dir string) error {
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(dataDir, "bootstrap.sh")
	if _, err := os.Stat(dest); err == nil {
		return nil // already exists, don't overwrite
	}
	content := `#!/usr/bin/env bash
# ─── ALF Bootstrap Script ───────────────────────────────────────────
# This script runs automatically at daemon startup when its content changes.
# Use it to install packages, configure tools, or set up your environment.
#
# Examples:
#   apt-get update && apt-get install -y jq
#   pip install faster-whisper
#   npm install -g @anthropic-ai/claude-code
#
# Notes:
#   - Runs as root inside the container
#   - Only re-runs when the file content changes (SHA-256 hash check)
#   - stdout/stderr are logged to the daemon log
#   - The working directory is /home/alf/data
# ─────────────────────────────────────────────────────────────────────
set -e

`
	return os.WriteFile(dest, []byte(content), 0o755)
}

func seedEmbedded(fsys embed.FS, root, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		dest := filepath.Join(destDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}

		// Skip if file already exists.
		if _, err := os.Stat(dest); err == nil {
			return nil
		}

		data, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
}
