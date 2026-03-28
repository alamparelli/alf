package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	cc "github.com/alamparelli/alf/internal/controlcenter"
)

const githubRepo = "alamparelli/alf"

// RunUpgrade updates the CLI binary and the Docker image.
func RunUpgrade(currentVersion string) {
	// Step 1: Self-update CLI binary
	PrintInfo("Checking for CLI updates...")
	selfUpdate(currentVersion)

	// Step 2: Pull latest Docker image + restart
	dir := alfDir()
	PrintInfo("Pulling latest image...")
	dockerCompose(dir, "pull")
	PrintCheck("Image updated")

	// Migrate config from data/config/ to config.d/ if needed.
	migrateConfigDir(dir)

	// Fix volume ownership - previous versions ran as root, now runs as alf (uid 1000).
	fixVolumePermissions(dir)

	// Fix secret file permissions - previous versions wrote 0o644 (world-readable).
	HardenSecrets(dir)

	// Auto-generate sidecar secrets if missing (existing installs upgrading).
	for _, sidecarSecret := range []string{"whisper_shared_secret", "embed_shared_secret"} {
		secretPath := filepath.Join(secretsDir(dir), sidecarSecret)
		if needsSecret(secretPath) {
			token, err := generateAuthToken()
			if err != nil {
				PrintInfo(fmt.Sprintf("Warning: failed to generate %s: %v", sidecarSecret, err))
				continue
			}
			if err := SetSecret(dir, sidecarSecret, token); err != nil {
				PrintInfo(fmt.Sprintf("Warning: failed to write %s: %v", sidecarSecret, err))
				continue
			}
			PrintCheck("secrets/" + sidecarSecret + " (auto-generated)")
		}
	}

	// Ensure optional secret files exist (even empty) so docker-compose
	// doesn't fail when secrets are always declared in the template.
	ensureOptionalSecrets(dir)

	// Regenerate docker-compose.yml from saved profile so new secrets
	// and template changes are picked up without re-running init.
	regenerateCompose(dir)

	// Seed any new bundled skills.
	if err := SeedBundledSkills(dir); err != nil {
		PrintInfo(fmt.Sprintf("Warning: failed to seed bundled skills: %v", err))
	}

	// Seed any new bundled agent teams.
	if err := SeedBundledAgents(dir); err != nil {
		PrintInfo(fmt.Sprintf("Warning: failed to seed bundled agents: %v", err))
	}

	PrintInfo("Restarting ALF...")
	fixPreStart(dir)
	dockerCompose(dir, "up", "-d")
	PrintCheck("ALF restarted")

	PrintSuccess("ALF upgraded to latest.")
}

func selfUpdate(currentVersion string) bool {
	// Try alpha channel first (private distribution).
	if alphaUpdate(currentVersion) {
		return true
	}

	latest, err := fetchLatestTag()
	if err != nil {
		PrintWarning(fmt.Sprintf("Could not check latest version: %v", err))
		return false
	}

	if latest == currentVersion {
		PrintCheck(fmt.Sprintf("CLI already at %s", currentVersion))
		return false
	}

	PrintInfo(fmt.Sprintf("Updating CLI: %s → %s", currentVersion, latest))

	binaryURL := fmt.Sprintf(
		"https://github.com/%s/releases/download/%s/alf-%s-%s",
		githubRepo, latest, runtime.GOOS, runtime.GOARCH,
	)

	// Download to temp file
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(binaryURL)
	if err != nil {
		PrintWarning(fmt.Sprintf("Download failed: %v", err))
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		PrintWarning(fmt.Sprintf("Download failed: HTTP %d (no binary for %s/%s?)", resp.StatusCode, runtime.GOOS, runtime.GOARCH))
		return false
	}

	tmpFile, err := os.CreateTemp("", "alf-upgrade-*")
	if err != nil {
		PrintWarning(fmt.Sprintf("Failed to create temp file: %v", err))
		return false
	}
	tmpPath := tmpFile.Name()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		PrintWarning(fmt.Sprintf("Download failed: %v", err))
		return false
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		PrintWarning(fmt.Sprintf("Failed to set permissions: %v", err))
		return false
	}

	// Replace current binary
	execPath, err := os.Executable()
	if err != nil {
		os.Remove(tmpPath)
		PrintWarning(fmt.Sprintf("Could not find current binary path: %v", err))
		return false
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		os.Remove(tmpPath)
		PrintWarning(fmt.Sprintf("Failed to replace binary: %v", err))
		return false
	}

	PrintCheck(fmt.Sprintf("CLI updated to %s", latest))
	return true
}

func fixVolumePermissions(dir string) {
	// macOS Docker Desktop uses VirtioFS - handles permission mapping automatically.
	if runtime.GOOS != "linux" {
		return
	}

	chown := func(rel, owner string) {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err != nil {
			return
		}
		cmd := exec.Command("sudo", "chown", "-R", owner, p)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			cmd2 := exec.Command("chown", "-R", owner, p)
			cmd2.Run()
		}
	}

	// Workspace + caches owned by alf (1000:1000).
	chown("data", "1000:1000")
	chown("skills.d", "1000:1000")
	chown("cache", "1000:1000")
	chown("local", "1000:1000")
	// config.d owned by daemon (1001:1000) — entrypoint enforces final ownership.
	chown("config.d", "1001:1000")

	// Secrets: readable by owner only. Docker Compose (non-Swarm) bind-mounts
	// secrets preserving host permissions; container runs as same uid.
	HardenSecrets(dir)
}

// migrateConfigDir copies config files from data/config/ to config.d/ if config.d is empty.
// This handles upgrades from versions that stored config inside the data volume.
func migrateConfigDir(dir string) {
	configD := filepath.Join(dir, "config.d")
	oldConfigDir := filepath.Join(dir, "data", "config")

	// Check if config.d already has config.json - no migration needed.
	if _, err := os.Stat(filepath.Join(configD, "config.json")); err == nil {
		return
	}

	// Check if old config directory exists with files to migrate.
	if _, err := os.Stat(oldConfigDir); err != nil {
		return
	}

	os.MkdirAll(configD, 0o755)

	for _, name := range []string{"config.json", "tiers.json", "router-prompt.md"} {
		src := filepath.Join(oldConfigDir, name)
		dst := filepath.Join(configD, name)
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			PrintWarning(fmt.Sprintf("Failed to migrate %s: %v", name, err))
			continue
		}
		PrintCheck(fmt.Sprintf("Migrated %s → config.d/", name))
	}

	// Migrate per-tier directories from data/tiers/ to config.d/tiers/.
	oldTiersDir := filepath.Join(dir, "data", "tiers")
	newTiersDir := filepath.Join(configD, "tiers")
	if _, err := os.Stat(newTiersDir); err == nil {
		return // already migrated
	}
	entries, err := os.ReadDir(oldTiersDir)
	if err != nil {
		return // no old tiers
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		src := filepath.Join(oldTiersDir, e.Name())
		dst := filepath.Join(newTiersDir, e.Name())
		if err := copyDir(src, dst); err != nil {
			PrintWarning(fmt.Sprintf("Failed to migrate tier %s: %v", e.Name(), err))
			continue
		}
		PrintCheck(fmt.Sprintf("Migrated tier %s → config.d/tiers/", e.Name()))
	}
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// regenerateCompose rebuilds docker-compose.yml from the saved setup profile.
func regenerateCompose(dir string) {
	profile := loadSetupProfile()
	if profile.Dir == "" {
		// No saved profile - can't regenerate.
		return
	}

	var data ComposeData
	if profile.HTTPS {
		data = ComposeData{
			EnableHTTPS:   true,
			Domain:        profile.Domain,
			AcmeEmail:     profile.AcmeEmail,
			CCExternalURL: fmt.Sprintf("https://%s", profile.Domain),
		}
	} else {
		port := profile.Port
		if port == "" {
			port = cc.DefaultPort
		}
		host := profile.Host
		if host == "" {
			host = "localhost"
		}
		data = ComposeData{
			CCPort:        port,
			CCExternalURL: fmt.Sprintf("http://%s:%s", host, port),
		}
	}
	data.Timezone = profile.Timezone
	data.Workspaces = profile.Workspaces
	if profile.ImageTag != "" && profile.ImageTag != "latest" {
		data.Image = "ghcr.io/alamparelli/alf:" + profile.ImageTag
	} else {
		data.Image = "ghcr.io/alamparelli/alf:latest"
	}
	if img := os.Getenv("ALF_IMAGE"); img != "" {
		data.Image = img
	}

	if err := RenderDockerCompose(dir, data); err != nil {
		PrintWarning(fmt.Sprintf("Failed to regenerate docker-compose.yml: %v", err))
		return
	}
	PrintCheck("docker-compose.yml regenerated")
}

func fetchLatestTag() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	if release.TagName == "" {
		return "", fmt.Errorf("no release found")
	}
	return release.TagName, nil
}

const alphaBaseURL = "https://cc.lamparelli.eu/alpha"

// alphaUpdate checks the alpha distribution channel for a newer CLI binary.
// It reads credentials from the ALF_TOKEN env var or the saved setup profile.
func alphaUpdate(currentVersion string) bool {
	token := os.Getenv("ALF_TOKEN")
	if token == "" {
		// Try ~/.alf_alpha_token (saved by alpha install script).
		home, _ := os.UserHomeDir()
		data, err := os.ReadFile(filepath.Join(home, ".alf_alpha_token"))
		if err != nil {
			// Fallback: secrets dir.
			dir := alfDir()
			data, err = os.ReadFile(filepath.Join(dir, "secrets", "alpha_token"))
			if err != nil {
				return false
			}
		}
		token = strings.TrimSpace(string(data))
		if token == "" {
			return false
		}
	}

	filename := fmt.Sprintf("alf-%s-%s", runtime.GOOS, runtime.GOARCH)
	binaryURL := fmt.Sprintf("%s/%s", alphaBaseURL, filename)

	client := &http.Client{Timeout: 60 * time.Second}
	req, _ := http.NewRequest("GET", binaryURL, nil)
	req.SetBasicAuth("alpha", token)
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return false
	}
	defer resp.Body.Close()

	tmpFile, err := os.CreateTemp("", "alf-alpha-*")
	if err != nil {
		return false
	}
	tmpPath := tmpFile.Name()
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return false
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return false
	}

	execPath, err := os.Executable()
	if err != nil {
		os.Remove(tmpPath)
		return false
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		os.Remove(tmpPath)
		return false
	}

	PrintCheck("CLI updated from alpha channel")
	return true
}
