package cli

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed bundled_skills/*
var bundledSkillsFS embed.FS

//go:embed bundled_agents/*
var bundledAgentsFS embed.FS

// ComposeData holds values for the docker-compose template.
type ComposeData struct {
	Image         string // Docker image (default: ghcr.io/alamparelli/alf:latest)
	CCPort        string
	CCExternalURL string
	EnableHTTPS   bool
	Domain        string
	AcmeEmail     string
	Timezone      string // IANA timezone (e.g. "Europe/Brussels")
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
	// Agents live inside config.d/ which is mounted as /opt/alf/config in the container.
	agentsDir := filepath.Join(dir, "config.d", "agents")
	return seedEmbedded(bundledAgentsFS, "bundled_agents", agentsDir)
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
