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

const configReadme = `# ALF Configuration

Edit these files via the Control Center workspace explorer.
Changes are applied immediately after saving.

## config.json

Main daemon configuration.

| Field | Description |
|-------|-------------|
| log_level | "info", "debug", or "warn" |
| quiet_hours | { "start": 22, "end": 7 } — suppress notifications |
| session_timeout | Minutes of inactivity before session reset |
| system_prompt | Custom system prompt injected into all Claude calls |
| git_track | Enable git tracking of data directory |

## tiers.json

Routing tier configuration. The router classifies each message and routes it to the appropriate tier.

| Field | Type | Description |
|-------|------|-------------|
| router_model | string | Model for classification: "haiku", "sonnet", "opus" |
| default_fallback | string | Tier name to use when classification fails |
| tiers[] | array | List of tier definitions |

### Tier fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| name | string | required | Unique identifier (alphanumeric, dashes, underscores) |
| model | string | required | "haiku", "sonnet", or "opus" |
| priority | int | 0 | Lower = tried first by router |
| enabled | bool | true | Whether the tier is active |
| routable | bool | true | Whether the router can select this tier |
| router_label | string | "" | Short label shown to router for classification |
| description | string | "" | Rich description for router (overrides label) |
| max_turns | int | 0 | Max agentic turns (0 = default 3) |
| effort | string | "" | "low", "medium", or "high" |
| write_capable | bool | false | Whether this tier can write files |
| instant | bool | false | Router responds directly, skips second LLM call |
| tools | []string | [] | Allowed tools (empty = all) |

## sessions.json

Active Control Center login sessions. Managed automatically.
`
var chatIDRegex = regexp.MustCompile(`^-?\d+$`)
var domainRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+$`)
var emailRegex = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

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

	// Step 5: Dashboard access
	PrintStep(5, "Dashboard access")
	var composeData ComposeData
	if promptHTTPS(reader) {
		domain := promptDomain(reader)
		acmeEmail := promptAcmeEmail(reader)
		checkPortsForHTTPS()
		composeData = ComposeData{
			EnableHTTPS:   true,
			Domain:        domain,
			AcmeEmail:     acmeEmail,
			CCExternalURL: fmt.Sprintf("https://%s", domain),
		}
		// Create letsencrypt directory for ACME certificates
		if err := os.MkdirAll(filepath.Join(dir, "letsencrypt"), 0o755); err != nil {
			Fatal(fmt.Sprintf("Failed to create letsencrypt/: %v", err))
		}
	} else {
		ccPort := promptPort(reader)
		ccHost := promptHost(reader)
		composeData = ComposeData{
			CCPort:        ccPort,
			CCExternalURL: fmt.Sprintf("http://%s:%s", ccHost, ccPort),
		}
	}

	// Step 6: Generate files
	PrintStep(6, "Generating configuration files")
	generateFiles(dir, botToken, chatID, composeData)

	// Step 7: Pull & Start
	PrintStep(7, "Starting ALF")
	pullAndStart(dir, botName, composeData.EnableHTTPS)

	// Step 8: Claude authentication
	PrintStep(8, "Claude authentication")
	fmt.Println("\n  ALF needs Claude Code authentication.")
	fmt.Println("  Run " + colorBold + "alf login" + colorReset + " to authenticate.")

	// Summary
	fmt.Println()
	PrintBanner()
	fmt.Println("  Setup complete!")
	fmt.Println()
	PrintCheck(fmt.Sprintf("Install directory: %s", dir))
	PrintCheck(fmt.Sprintf("Bot: @%s", botName))
	PrintCheck(fmt.Sprintf("Dashboard: %s", composeData.CCExternalURL))
	fmt.Println()
	PrintSuccess("Run " + colorBold + "alf login" + colorReset + " to authenticate Claude, then message @" + botName + " on Telegram.")
	fmt.Println()
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

	subdirs := []string{
		"config.d", "skills.d",
		"data/.claude", "data/tools", "data/skills",
		"data/logs", "data/memories",
	}
	for _, sub := range subdirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			Fatal(fmt.Sprintf("Failed to create %s: %v", sub, err))
		}
	}

	// Ensure volume directories are owned by uid 1000 (node user inside container).
	fixVolumePermissions(dir)
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

func promptHost(reader *bufio.Reader) string {
	// Try to detect the hostname.
	hostname, _ := os.Hostname()
	defaultHost := "localhost"
	if hostname != "" {
		defaultHost = hostname
	}

	fmt.Printf("  Server address [%s]: ", defaultHost)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		PrintCheck(fmt.Sprintf("Host: %s", defaultHost))
		return defaultHost
	}
	PrintCheck(fmt.Sprintf("Host: %s", input))
	return input
}

func promptHTTPS(reader *bufio.Reader) bool {
	fmt.Println("\n  If you have a public domain pointing to this server,")
	fmt.Println("  ALF can set up automatic HTTPS with Let's Encrypt.")
	fmt.Print("\n  Enable HTTPS with Let's Encrypt? [y/N]: ")
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}

func promptDomain(reader *bufio.Reader) string {
	for {
		fmt.Print("\n  Domain name (e.g. alf.example.com): ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if !domainRegex.MatchString(input) {
			PrintError("Invalid domain format. Expected: alf.example.com")
			continue
		}

		PrintCheck(fmt.Sprintf("Domain: %s", input))
		return input
	}
}

func promptAcmeEmail(reader *bufio.Reader) string {
	for {
		fmt.Print("  Email for Let's Encrypt notifications: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if !emailRegex.MatchString(input) {
			PrintError("Invalid email format.")
			continue
		}

		PrintCheck(fmt.Sprintf("Email: %s", input))
		return input
	}
}

func checkPortsForHTTPS() {
	for _, port := range []string{"80", "443"} {
		if !isPortAvailable(port) {
			PrintWarning(fmt.Sprintf("Port %s is in use. Traefik needs ports 80 and 443 to be free.", port))
		}
	}
}

func generateFiles(dir, botToken, chatID string, compose ComposeData) {
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

	if err := RenderDockerCompose(dir, compose); err != nil {
		Fatal(fmt.Sprintf("Failed to write docker-compose.yml: %v", err))
	}
	PrintCheck("docker-compose.yml")

	if err := RenderConfig(dir, ConfigData{ChatID: chatID}); err != nil {
		Fatal(fmt.Sprintf("Failed to write config.json: %v", err))
	}
	PrintCheck("config.json")

	// Write config README if it doesn't exist.
	readmePath := filepath.Join(dir, "config.d", "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		os.WriteFile(readmePath, []byte(configReadme), 0o644)
	}
}

func generateAuthToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func pullAndStart(dir, botName string, httpsEnabled bool) {
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
	expectedContainers := 1
	if httpsEnabled {
		expectedContainers = 2
	}
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Second)
		check := exec.Command("docker", "compose", "ps", "--format", "{{.Status}}")
		check.Dir = dir
		out, err := check.Output()
		if err == nil {
			upCount := strings.Count(string(out), "Up")
			if upCount >= expectedContainers {
				PrintCheck("ALF is running")
				if httpsEnabled {
					PrintCheck("Traefik is running")
				}
				PrintSuccess(fmt.Sprintf("Setup complete! Send a message to @%s on Telegram.", botName))
				return
			}
		}
	}

	PrintWarning("ALF started but health check inconclusive. Check with: alf status")
}

func fixClaudeOwnership() {
	fix := exec.Command("docker", "exec", "alf",
		"sh", "-c", "chown -R 1000:1000 /home/node/data/.claude /home/node/data/.claude.json 2>/dev/null; true")
	fix.Run()
}

func verifyClaudeAuth() {
	verify := exec.Command("docker", "exec", "-e", "HOME=/home/node/data",
		"alf", "claude", "-p", "ping", "--output-format", "json", "--max-turns", "1")
	out, _ := verify.Output()
	if len(out) > 0 && strings.Contains(string(out), `"is_error":false`) {
		PrintCheck("Claude authenticated")
	} else {
		PrintWarning("Claude not authenticated yet. Run:")
		fmt.Println()
		fmt.Println("    docker exec -it -e HOME=/home/node/data alf claude")
		fmt.Println("    Then: alf login")
		fmt.Println()
	}
}

// RunLogin allows re-authenticating Claude from the CLI.
func RunLogin() {
	PrintInfo("Launching Claude Code for authentication...")
	fmt.Println("  Type " + colorBold + "/login" + colorReset + " inside Claude to authenticate, then " + colorBold + "/exit" + colorReset + " when done.")
	fmt.Println()
	cmd := exec.Command("docker", "exec", "-it", "-e", "HOME=/home/node/data", "alf", "claude")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()

	fixClaudeOwnership()
	verifyClaudeAuth()
}
