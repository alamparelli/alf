package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"
)

const githubRepo = "alamparelli/alf"

// RunUpgrade updates the CLI binary and the Docker image.
func RunUpgrade(currentVersion string) {
	// Step 1: Self-update CLI binary
	PrintInfo("Checking for CLI updates...")
	upgraded := selfUpdate(currentVersion)

	if !upgraded {
		PrintSuccess("Already up to date.")
		return
	}

	// Step 2: Pull latest Docker image + restart
	dir := alfDir()
	PrintInfo("Pulling latest image...")
	dockerCompose(dir, "pull")
	PrintCheck("Image updated")

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
