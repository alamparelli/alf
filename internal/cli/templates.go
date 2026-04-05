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

//go:embed all:bundled_agents
var bundledAgentsFS embed.FS

// ComposeData holds values for the docker-compose template.
type ComposeData struct {
	Image         string   // Docker image (default: ghcr.io/alamparelli/alf:latest)
	WhisperImage  string   // Whisper image (default: ghcr.io/alamparelli/whisper-service:latest)
	EmbedImage    string   // Embed service image (default: ghcr.io/alamparelli/embed-service:latest)
	CCPort        string
	CCBind        string   // Bind address (default: "127.0.0.1", set "0.0.0.0" for LAN access)
	CCExternalURL string
	EnableHTTPS   bool
	Domain        string
	AcmeEmail     string
	Timezone      string   // IANA timezone (e.g. "Europe/Brussels")
	Workspaces    []string // Host paths mounted as workspaces under /workspaces/<basename>
	JSRuntime     string   // "node", "deno", "bun", or "" (none)
	WhisperModel  string   // Whisper model name (default: "small")
}

// RenderDockerCompose writes docker-compose.yml with the given port.
func RenderDockerCompose(dir string, data ComposeData) error {
	if data.WhisperModel == "" {
		data.WhisperModel = "small"
	}
	if data.WhisperImage == "" {
		data.WhisperImage = "ghcr.io/alamparelli/whisper-service:latest"
	}
	if data.EmbedImage == "" {
		data.EmbedImage = "ghcr.io/alamparelli/embed-service:latest"
	}
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

// deprecatedSkills maps old skill names to their replacement (for logging).
var deprecatedSkills = map[string]string{
	"app-builder": "sdk-app-builder",
}

// SeedBundledSkills syncs embedded skills into the skills.d directory.
// Files are overwritten to keep bundled skills up-to-date on upgrade.
// Obsolete files inside bundled skill directories are removed.
// Deprecated skills are removed first.
func SeedBundledSkills(dir string) error {
	skillsDir := filepath.Join(dir, "skills.d")
	// Clean up skills that have been replaced.
	for old := range deprecatedSkills {
		os.RemoveAll(filepath.Join(skillsDir, old))
	}
	return syncEmbedded(bundledSkillsFS, "bundled_skills", skillsDir)
}

// SeedBundledAgents syncs embedded agent teams into the data/agents/teams directory.
// Files are overwritten to keep bundled agents up-to-date on upgrade.
// Obsolete files inside bundled agent directories are removed.
func SeedBundledAgents(dir string) error {
	agentsDir := filepath.Join(dir, "data", "agents", "teams")
	return syncEmbedded(bundledAgentsFS, "bundled_agents", agentsDir)
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

// syncEmbedded writes all files from the embedded FS into destDir,
// overwriting existing files. Then it removes any files/dirs inside
// top-level subdirectories that no longer exist in the embed.
// Only cleans inside known subdirectories (not user-created ones).
func syncEmbedded(fsys embed.FS, root, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	// Collect the set of all embedded relative paths.
	embedded := make(map[string]bool)

	// Phase 1: write all embedded files (overwrite existing).
	if err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		embedded[rel] = true
		dest := filepath.Join(destDir, rel)

		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}

		data, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	}); err != nil {
		return err
	}

	// Phase 2: find top-level dirs that came from the embed (bundled items).
	bundledDirs := make(map[string]bool)
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil // no entries to clean
	}
	for _, e := range entries {
		if e.IsDir() {
			bundledDirs[e.Name()] = true
		}
	}

	// Phase 3: walk each bundled dir on disk and remove files not in embed.
	for dir := range bundledDirs {
		diskDir := filepath.Join(destDir, dir)
		filepath.WalkDir(diskDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip inaccessible
			}
			rel, _ := filepath.Rel(destDir, path)
			if rel == "." || rel == dir {
				return nil
			}
			if !embedded[rel] {
				os.RemoveAll(path)
				if d.IsDir() {
					return filepath.SkipDir
				}
			}
			return nil
		})
	}

	return nil
}
