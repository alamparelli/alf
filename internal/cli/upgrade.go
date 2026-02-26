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
	"time"
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

	// Fix volume ownership — previous versions ran as root, now runs as node (uid 1000).
	fixVolumePermissions(dir)

	PrintInfo("Restarting ALF...")
	dockerCompose(dir, "up", "-d")
	PrintCheck("ALF restarted")

	PrintSuccess("ALF upgraded to latest.")
}

func selfUpdate(currentVersion string) bool {
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
	// Map directories to their container UIDs.
	// data/ → node (1000), claude-session/ → claude (1001).
	ownership := map[string]string{
		"data":           "1000:1000",
		"claude-session": "1001:1001",
	}
	for d, uid := range ownership {
		p := filepath.Join(dir, d)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		cmd := exec.Command("sudo", "chown", "-R", uid, p)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// Try without sudo (might already be owned correctly).
			cmd2 := exec.Command("chown", "-R", uid, p)
			cmd2.Run()
		}
	}
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
