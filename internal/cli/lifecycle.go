package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func alfDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "alf")

	// Check current directory first — if it has a docker-compose.yml, use it
	if _, err := os.Stat("docker-compose.yml"); err == nil {
		dir, _ = os.Getwd()
		return dir
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

func RunUpdate() {
	dir := alfDir()
	PrintInfo("Pulling latest image...")
	dockerCompose(dir, "pull")
	PrintCheck("Image updated")

	PrintInfo("Restarting ALF...")
	dockerCompose(dir, "up", "-d")
	PrintCheck("ALF updated and running")
}

func RunLogs() {
	dir := alfDir()
	dockerCompose(dir, "logs", "-f")
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
