package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func alfDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "alf")

	// Check current directory first — if it has a docker-compose.yml, use it.
	// But never use a git repository (source code) as install dir.
	if _, err := os.Stat("docker-compose.yml"); err == nil {
		cwd, _ := os.Getwd()
		if _, gitErr := os.Stat(filepath.Join(cwd, ".git")); gitErr != nil {
			return cwd
		}
	}

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
	PrintInfo("Starting ALF...")
	dockerCompose(dir, "up", "-d")
	PrintCheck("ALF started")
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

	// Remove data directory
	PrintInfo("Removing " + dir + "...")
	if err := os.RemoveAll(dir); err != nil {
		PrintWarning(fmt.Sprintf("Could not remove %s: %v", dir, err))
	}

	// Remove binary
	binPath, _ := os.Executable()
	PrintInfo("Removing " + binPath + "...")
	if err := os.Remove(binPath); err != nil {
		PrintWarning(fmt.Sprintf("Could not remove binary: %v. Run: sudo rm %s", err, binPath))
	}

	PrintCheck("ALF uninstalled")
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
