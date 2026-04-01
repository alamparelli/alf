package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	cc "github.com/alamparelli/alf/internal/controlcenter"
)

// daemonHasTLS checks if the install has self-signed TLS certs.
func daemonHasTLS(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "config.d", "tls", "cert.pem"))
	return err == nil
}

// daemonCurlURL returns the base URL for curl calls to the daemon inside the container.
// Uses https:// with -k (insecure) when TLS is enabled, http:// otherwise.
func daemonCurlURL(dir string) string {
	if daemonHasTLS(dir) {
		return "https://127.0.0.1:" + cc.DefaultPort
	}
	return "http://127.0.0.1:" + cc.DefaultPort
}

// daemonCurlFlags returns extra curl flags needed (e.g. -k for self-signed TLS).
func daemonCurlFlags(dir string) []string {
	if daemonHasTLS(dir) {
		return []string{"-k"}
	}
	return nil
}

// savedInstallPath returns the path to the file that stores the install directory.
func savedInstallPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "alf", "path")
}

// SaveInstallDir persists the install directory so all CLI commands can find it.
func SaveInstallDir(dir string) {
	p := savedInstallPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(dir+"\n"), 0o644)
}

func alfDir() string {
	// 1. Check current directory - if it has a docker-compose.yml, use it.
	// But never use a git repository (source code) as install dir.
	if _, err := os.Stat("docker-compose.yml"); err == nil {
		cwd, _ := os.Getwd()
		if _, gitErr := os.Stat(filepath.Join(cwd, ".git")); gitErr != nil {
			return cwd
		}
	}

	// 2. Check saved install path from alf init.
	if data, err := os.ReadFile(savedInstallPath()); err == nil {
		dir := strings.TrimSpace(string(data))
		if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err == nil {
			return dir
		}
	}

	// 3. Default to ~/alf.
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "alf")
	if _, err := os.Stat(filepath.Join(dir, "docker-compose.yml")); err != nil {
		Fatal("ALF is not installed. Run 'alf init' first.")
	}
	return dir
}

func dockerCompose(dir string, args ...string) {
	cmd := exec.Command("docker", append([]string{"compose"}, args...)...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		Fatal(fmt.Sprintf("Command failed: docker compose %s", strings.Join(args, " ")))
	}
}

func RunStart() {
	dir := alfDir()
	fixPreStart(dir)
	PrintInfo("Starting ALF...")
	dockerCompose(dir, "up", "-d")
	PrintCheck("ALF started")
}

// fixPreStart repairs common issues before docker compose up.
func fixPreStart(dir string) {
	// Fix resolv.conf: Docker auto-creates a directory placeholder when the
	// bind-mount target doesn't exist, causing "not a directory" errors.
	p := filepath.Join(dir, "resolv.conf")
	info, err := os.Stat(p)
	if err == nil && info.IsDir() {
		os.Remove(p)
	}
	if err != nil || (err == nil && info.IsDir()) {
		os.WriteFile(p, []byte("nameserver 8.8.8.8\nnameserver 1.1.1.1\n"), 0o644)
	}

	// Fix secret permissions (must be 644 for container uid 1000 to read).
	HardenSecrets(dir)

	// Fix empty secrets: auto-generate if missing or empty.
	for _, name := range []string{"whisper_shared_secret", "cc_auth_token"} {
		p := filepath.Join(secretsDir(dir), name)
		if needsSecret(p) {
			if token, err := generateAuthToken(); err == nil {
				if err := SetSecret(dir, name, token); err != nil {
					fmt.Fprintf(os.Stderr, "  warning: failed to write %s: %v\n", name, err)
				} else {
					PrintCheck(fmt.Sprintf("secrets/%s (auto-generated)", name))
				}
			}
		}
	}
}

func RunStop() {
	dir := alfDir()
	PrintInfo("Stopping ALF...")
	dockerCompose(dir, "down")
	PrintCheck("ALF stopped")
}

func RunRestart() {
	dir := alfDir()
	PrintInfo("Restarting ALF...")
	dockerCompose(dir, "restart")
	PrintCheck("ALF restarted")
}

// RunCompose regenerates docker-compose.yml from the saved setup profile
// and current secrets. Use after adding secrets or upgrading the CLI.
// RunMagicLink generates a CC login link via the daemon API.
func RunMagicLink() {
	// Read the auth token from secrets.
	dir := alfDir()
	tokenFile := filepath.Join(dir, "secrets", "cc_auth_token")
	tokenBytes, err := os.ReadFile(tokenFile)
	if err != nil || strings.TrimSpace(string(tokenBytes)) == "" {
		Fatal("cc_auth_token secret not set. Run: alf secret set cc_auth_token <token>")
	}
	token := strings.TrimSpace(string(tokenBytes))

	// Call the daemon's magic-link API via docker exec + curl.
	baseURL := daemonCurlURL(dir)
	args := append([]string{"exec", "alf", "curl"}, daemonCurlFlags(dir)...)
	args = append(args, "-sf", "-X", "POST",
		"-H", "Authorization: Bearer "+token,
		baseURL+"/api/magic-link")
	cmd := exec.Command("docker", args...)
	out, err := cmd.Output()
	if err != nil {
		Fatal("Failed to generate magic link. Is ALF running?")
	}

	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || resp.URL == "" {
		Fatal("Invalid response from daemon")
	}

	fmt.Println()
	PrintCheck("Magic link generated:")
	fmt.Println()
	fmt.Println("  " + resp.URL)
	fmt.Println()
}

func RunCompose() {
	dir := alfDir()
	ensureOptionalSecrets(dir)
	regenerateCompose(dir)
}

func RunLogs() {
	dir := alfDir()
	dockerCompose(dir, "logs", "-f")
}

func RunUninstall() {
	dir := alfDir()

	// Safety: never delete a git repository or the source code directory.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		Fatal("Refusing to uninstall: " + dir + " is a git repository.")
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		Fatal("Refusing to uninstall: " + dir + " appears to be a source code directory.")
	}

	fmt.Println()
	PrintWarning("This will remove ALF completely:")
	fmt.Println("  - Stop and remove containers")
	fmt.Println("  - Delete all data in " + dir)
	fmt.Println("  - Remove the alf binary")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("  Type 'yes' to confirm: ")
	answer, _ := reader.ReadString('\n')
	if strings.TrimSpace(answer) != "yes" {
		PrintInfo("Uninstall cancelled.")
		return
	}

	// Stop and remove containers + volumes
	PrintInfo("Stopping containers...")
	cmd := exec.Command("docker", "compose", "down", "-v")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()

	// Remove data directory, preserving letsencrypt certs to avoid rate limits.
	letsencryptDir := filepath.Join(dir, "letsencrypt")
	hasLE := false
	if _, err := os.Stat(letsencryptDir); err == nil {
		tmpLE := filepath.Join(os.TempDir(), "alf-letsencrypt-backup")
		os.RemoveAll(tmpLE)
		if err := os.Rename(letsencryptDir, tmpLE); err == nil {
			hasLE = true
		}
	}

	PrintInfo("Removing " + dir + "...")
	if err := os.RemoveAll(dir); err != nil {
		PrintWarning(fmt.Sprintf("Could not remove %s: %v", dir, err))
	}

	// Restore letsencrypt certs so next alf init reuses them.
	if hasLE {
		tmpLE := filepath.Join(os.TempDir(), "alf-letsencrypt-backup")
		os.MkdirAll(dir, 0o755)
		if err := os.Rename(tmpLE, letsencryptDir); err != nil {
			PrintWarning(fmt.Sprintf("Could not restore letsencrypt certs: %v", err))
		} else {
			PrintInfo("Preserved letsencrypt/ certificates for reuse.")
		}
	}

	// Remove binary
	binPath, _ := os.Executable()
	PrintInfo("Removing " + binPath + "...")
	if err := os.Remove(binPath); err != nil {
		PrintWarning(fmt.Sprintf("Could not remove binary: %v. Run: sudo rm %s", err, binPath))
	}

	PrintCheck("ALF uninstalled")
}

// PrintDockerVersion prints the running Docker image version.
func PrintDockerVersion() {
	out, err := exec.Command("docker", "ps", "--filter", "name=alf", "--format", "{{.Image}}").Output()
	if err != nil {
		return
	}
	img := strings.TrimSpace(string(out))
	if img != "" {
		fmt.Printf("image %s\n", img)
	}
}

func RunStatus() {
	dir := alfDir()

	fmt.Println()
	cmd := exec.Command("docker", "compose", "ps")
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		PrintError("Could not get container status")
		return
	}

	fmt.Println()
	imgCmd := exec.Command("docker", "compose", "images")
	imgCmd.Dir = dir
	imgCmd.Stdout = os.Stdout
	imgCmd.Stderr = os.Stderr
	imgCmd.Run()
}
