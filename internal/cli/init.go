package cli

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var tokenRegex = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]+$`)
var chatIDRegex = regexp.MustCompile(`^-?\d+$`)

func RunInit() {
	reader := bufio.NewReader(os.Stdin)

	// Step 0: Welcome
	PrintBanner()
	fmt.Println("  This wizard will set up ALF on your machine.")
	fmt.Println("  It takes about 2 minutes.")

	// Step 1: Prerequisites
	PrintStep(1, "Checking prerequisites")
	checkPrerequisites()

	// Step 2: Install directory
	PrintStep(2, "Choose install directory")
	dir := promptDirectory(reader)

	// Step 3: Telegram Bot Token
	PrintStep(3, "Telegram Bot Token")
	botToken, botName := promptBotToken(reader)

	// Step 4: Telegram Chat ID
	PrintStep(4, "Telegram Chat ID")
	chatID := promptChatID(reader, botToken)

	// Step 5: Dashboard port
	PrintStep(5, "Dashboard port")
	ccPort := promptPort(reader)

	// Step 6: Generate files
	PrintStep(6, "Generating configuration files")
	generateFiles(dir, botToken, chatID, ccPort)

	// Step 7: Pull & Start
	PrintStep(7, "Starting ALF")
	pullAndStart(dir, botName)

	// Step 8: Claude authentication
	PrintStep(8, "Claude authentication")
	claudeLogin(dir)
}

func checkPrerequisites() {
	if _, err := exec.LookPath("docker"); err != nil {
		PrintError("Docker is not installed.")
		fmt.Println("\n  Install Docker:")
		fmt.Println("    Linux:  curl -fsSL https://get.docker.com | sh")
		fmt.Println("    macOS:  brew install --cask docker")
		fmt.Println("    Windows: https://docs.docker.com/desktop/install/windows-install/")
		os.Exit(1)
	}
	PrintCheck("Docker found")

	cmd := exec.Command("docker", "compose", "version")
	if err := cmd.Run(); err != nil {
		PrintError("'docker compose' is not available.")
		fmt.Println("\n  Docker Compose v2 is required. It comes bundled with Docker Desktop.")
		fmt.Println("  If using Docker Engine on Linux, install the compose plugin:")
		fmt.Println("    sudo apt install docker-compose-plugin")
		os.Exit(1)
	}
	PrintCheck("Docker Compose found")
}

func promptDirectory(reader *bufio.Reader) string {
	home, _ := os.UserHomeDir()
	defaultDir := filepath.Join(home, "alf")

	fmt.Printf("\n  Install directory [%s]: ", defaultDir)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	dir := defaultDir
	if input != "" {
		if strings.HasPrefix(input, "~") {
			input = filepath.Join(home, input[1:])
		}
		dir = input
	}

	subdirs := []string{"tools", "skills", "data/logs", "data/memory", "data/state"}
	for _, sub := range subdirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			Fatal(fmt.Sprintf("Failed to create %s: %v", sub, err))
		}
	}
	PrintCheck(fmt.Sprintf("Directory ready: %s", dir))
	return dir
}

func promptBotToken(reader *bufio.Reader) (string, string) {
	fmt.Println("\n  To create a Telegram bot:")
	fmt.Println("    1. Open Telegram, search @BotFather")
	fmt.Println("    2. Send /newbot")
	fmt.Println("    3. Choose a name (e.g. \"My ALF\")")
	fmt.Println("    4. Choose a username (must end in \"bot\")")
	fmt.Println("    5. Copy the token BotFather gives you")

	for {
		fmt.Print("\n  Paste your bot token: ")
		token, _ := reader.ReadString('\n')
		token = strings.TrimSpace(token)

		if !tokenRegex.MatchString(token) {
			PrintError("Invalid token format. Expected: 123456789:ABCdef...")
			continue
		}

		name, err := validateBotToken(token)
		if err != nil {
			PrintError(fmt.Sprintf("Token validation failed: %v", err))
			continue
		}

		PrintCheck(fmt.Sprintf("Bot verified: @%s", name))
		return token, name
	}
}

func validateBotToken(token string) (string, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("invalid response from Telegram")
	}

	if !result.OK {
		return "", fmt.Errorf("invalid token")
	}

	return result.Result.Username, nil
}

func promptChatID(reader *bufio.Reader, botToken string) string {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates", botToken)
	fmt.Println("\n  To find your chat ID:")
	fmt.Println("    1. Send any message to your bot on Telegram")
	fmt.Printf("    2. Open: %s\n", url)
	fmt.Println("    3. Look for \"chat\":{\"id\":YOUR_CHAT_ID}")

	for {
		fmt.Print("\n  Paste your chat ID: ")
		id, _ := reader.ReadString('\n')
		id = strings.TrimSpace(id)

		if !chatIDRegex.MatchString(id) {
			PrintError("Invalid chat ID. Expected a number (e.g. 123456789)")
			continue
		}

		PrintCheck(fmt.Sprintf("Chat ID: %s", id))
		return id
	}
}

func isPortAvailable(port string) bool {
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func promptPort(reader *bufio.Reader) string {
	defaultPort := "8080"

	if isPortAvailable(defaultPort) {
		fmt.Printf("\n  Dashboard port [%s]: ", defaultPort)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			PrintCheck(fmt.Sprintf("Port: %s", defaultPort))
			return defaultPort
		}
		if !isPortAvailable(input) {
			PrintWarning(fmt.Sprintf("Port %s is already in use.", input))
		} else {
			PrintCheck(fmt.Sprintf("Port: %s", input))
			return input
		}
	} else {
		PrintWarning(fmt.Sprintf("Port %s is already in use.", defaultPort))
	}

	// Port unavailable — ask for alternative
	for {
		fmt.Print("  Enter an available port: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if !isPortAvailable(input) {
			PrintWarning(fmt.Sprintf("Port %s is also in use. Try another.", input))
			continue
		}
		PrintCheck(fmt.Sprintf("Port: %s", input))
		return input
	}
}

func generateFiles(dir, botToken, chatID, ccPort string) {
	// Store secrets as files (chmod 600, used via Docker Compose secrets)
	if err := SetSecret(dir, "telegram_bot_token", botToken); err != nil {
		Fatal(fmt.Sprintf("Failed to write secret: %v", err))
	}
	PrintCheck("secrets/telegram_bot_token")

	if err := SetSecret(dir, "telegram_chat_id", chatID); err != nil {
		Fatal(fmt.Sprintf("Failed to write secret: %v", err))
	}
	PrintCheck("secrets/telegram_chat_id")

	// Generate Control Center auth token.
	ccToken, err := generateAuthToken()
	if err != nil {
		Fatal(fmt.Sprintf("Failed to generate auth token: %v", err))
	}
	if err := SetSecret(dir, "cc_auth_token", ccToken); err != nil {
		Fatal(fmt.Sprintf("Failed to write secret: %v", err))
	}
	PrintCheck("secrets/cc_auth_token")

	if err := RenderDockerCompose(dir, ComposeData{CCPort: ccPort}); err != nil {
		Fatal(fmt.Sprintf("Failed to write docker-compose.yml: %v", err))
	}
	PrintCheck("docker-compose.yml")

	if err := RenderConfig(dir); err != nil {
		Fatal(fmt.Sprintf("Failed to write config.json: %v", err))
	}
	PrintCheck("config.json")
}

func generateAuthToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func pullAndStart(dir, botName string) {
	fmt.Println()
	PrintInfo("Pulling ALF image...")
	pull := exec.Command("docker", "compose", "pull")
	pull.Dir = dir
	pull.Stdout = os.Stdout
	pull.Stderr = os.Stderr
	if err := pull.Run(); err != nil {
		Fatal(fmt.Sprintf("Failed to pull image: %v", err))
	}
	PrintCheck("Image pulled")

	PrintInfo("Starting ALF...")
	up := exec.Command("docker", "compose", "up", "-d")
	up.Dir = dir
	up.Stdout = os.Stdout
	up.Stderr = os.Stderr
	if err := up.Run(); err != nil {
		Fatal(fmt.Sprintf("Failed to start: %v", err))
	}

	// Health check with retries
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Second)
		check := exec.Command("docker", "compose", "ps", "--format", "{{.Status}}")
		check.Dir = dir
		out, err := check.Output()
		if err == nil && strings.Contains(string(out), "Up") {
			PrintCheck("ALF is running")
			PrintSuccess(fmt.Sprintf("Setup complete! Send a message to @%s on Telegram.", botName))
			return
		}
	}

	PrintWarning("ALF started but health check inconclusive. Check with: alf status")
}

func claudeLogin(dir string) {
	fmt.Println("\n  ALF uses Claude Code inside the container.")
	fmt.Println("  You need to authenticate with your Anthropic account.")
	fmt.Println()
	fmt.Println("  Launching Claude Code... Authenticate, then choose '"+colorBold+"Exit"+colorReset+"' to continue.")
	fmt.Println()

	// Try launching the full claude TUI via docker exec.
	// The TUI handles auth inline during first launch.
	cmd := exec.Command("docker", "exec", "-it", "alf", "claude")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		PrintWarning("Could not launch Claude directly. Try via SSH:")
		fmt.Println()
		fmt.Println("    ssh node@localhost -p 2222")
		fmt.Println("    Password: alf2026")
		fmt.Println("    Then run: claude")
		fmt.Println("    After authenticating, choose '"+colorBold+"Exit"+colorReset+"' and disconnect.")
		fmt.Println()
		return
	}
	PrintCheck("Claude authenticated")
}

// RunLogin allows re-authenticating Claude from the CLI.
func RunLogin() {
	PrintInfo("Launching Claude Code for authentication...")
	fmt.Println()
	cmd := exec.Command("docker", "exec", "-it", "alf", "claude")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		PrintWarning("Could not launch Claude directly. Try via SSH:")
		fmt.Println()
		fmt.Println("    ssh node@localhost -p 2222")
		fmt.Println("    Password: alf2026")
		fmt.Println("    Then run: claude")
		fmt.Println()
	}
}
