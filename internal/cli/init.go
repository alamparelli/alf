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
	"runtime"
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

// setupProfile stores previous init values so re-running `alf init` pre-fills them.
type setupProfile struct {
	Dir       string `json:"dir,omitempty"`
	BotToken  string `json:"bot_token,omitempty"`
	ChatID    string `json:"chat_id,omitempty"`
	HTTPS     bool   `json:"https,omitempty"`
	Domain    string `json:"domain,omitempty"`
	AcmeEmail string `json:"acme_email,omitempty"`
	Port      string `json:"port,omitempty"`
	Host      string `json:"host,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
}

func setupProfilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".alf-setup.json")
}

func loadSetupProfile() setupProfile {
	var p setupProfile
	data, err := os.ReadFile(setupProfilePath())
	if err != nil {
		return p
	}
	json.Unmarshal(data, &p)
	return p
}

func saveSetupProfile(p setupProfile) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(setupProfilePath(), data, 0o600)
}

func RunInit() {
	reader := bufio.NewReader(os.Stdin)
	prev := loadSetupProfile()

	// Step 0: Welcome
	PrintBanner()
	fmt.Println("  This wizard will set up ALF on your machine.")
	if prev.Dir != "" {
		fmt.Println("  Previous setup detected — press Enter to keep existing values.")
	} else {
		fmt.Println("  It takes about 2 minutes.")
	}

	// Step 1: Prerequisites
	PrintStep(1, "Checking prerequisites")
	checkPrerequisites()

	// Step 2: Install directory
	PrintStep(2, "Choose install directory")
	dir := promptDirectory(reader, prev.Dir)

	// Step 3: Telegram Bot Token
	PrintStep(3, "Telegram Bot Token")
	botToken, botName := promptBotToken(reader, prev.BotToken)

	// Step 4: Telegram Chat ID
	PrintStep(4, "Telegram Chat ID")
	chatID := promptChatID(reader, botToken, prev.ChatID)

	// Step 5: Dashboard access
	PrintStep(5, "Dashboard access")
	var composeData ComposeData
	if promptHTTPS(reader, prev.HTTPS) {
		domain := promptDomain(reader, prev.Domain)
		acmeEmail := promptAcmeEmail(reader, prev.AcmeEmail)
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

		// Save profile
		saveSetupProfile(setupProfile{
			Dir: dir, BotToken: botToken, ChatID: chatID,
			HTTPS: true, Domain: domain, AcmeEmail: acmeEmail,
		})
	} else {
		ccPort := promptPort(reader, prev.Port)
		ccHost := promptHost(reader, prev.Host)
		composeData = ComposeData{
			CCPort:        ccPort,
			CCExternalURL: fmt.Sprintf("http://%s:%s", ccHost, ccPort),
		}

		// Save profile
		saveSetupProfile(setupProfile{
			Dir: dir, BotToken: botToken, ChatID: chatID,
			Port: ccPort, Host: ccHost,
		})
	}

	// Step 5b: Timezone
	tz := promptTimezone(reader, prev.Timezone)
	composeData.Timezone = tz

	// Update saved profile with timezone.
	profile := loadSetupProfile()
	profile.Timezone = tz
	saveSetupProfile(profile)

	// Set default image, allow override via ALF_IMAGE env var.
	composeData.Image = "ghcr.io/alamparelli/alf:latest"
	if img := os.Getenv("ALF_IMAGE"); img != "" {
		composeData.Image = img
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
	reader := bufio.NewReader(os.Stdin)
	isLinux := strings.Contains(strings.ToLower(runtime.GOOS), "linux")

	if _, err := exec.LookPath("docker"); err != nil {
		if isLinux {
			fmt.Println("\n  Docker is not installed.")
			fmt.Print("  Install Docker now? [Y/n]: ")
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(strings.ToLower(input))
			if input == "" || input == "y" || input == "yes" {
				PrintInfo("Installing Docker...")
				install := exec.Command("sh", "-c", "curl -fsSL https://get.docker.com | sh")
				install.Stdout = os.Stdout
				install.Stderr = os.Stderr
				if err := install.Run(); err != nil {
					Fatal(fmt.Sprintf("Docker installation failed: %v", err))
				}
				PrintCheck("Docker installed")
			} else {
				Fatal("Docker is required. Install it and re-run alf init.")
			}
		} else {
			PrintError("Docker is not installed.")
			fmt.Println("\n  Install Docker:")
			fmt.Println("    macOS:  brew install --cask docker")
			fmt.Println("    Windows: https://docs.docker.com/desktop/install/windows-install/")
			os.Exit(1)
		}
	} else {
		PrintCheck("Docker found")
	}

	// Check Docker Compose plugin.
	cmd := exec.Command("docker", "compose", "version")
	if err := cmd.Run(); err != nil {
		if isLinux {
			fmt.Println("\n  Docker Compose plugin is not installed.")
			fmt.Print("  Install it now? [Y/n]: ")
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(strings.ToLower(input))
			if input == "" || input == "y" || input == "yes" {
				PrintInfo("Installing Docker Compose plugin...")
				install := exec.Command("sh", "-c", "apt-get update -qq && apt-get install -y -qq docker-compose-plugin")
				install.Stdout = os.Stdout
				install.Stderr = os.Stderr
				if err := install.Run(); err != nil {
					Fatal(fmt.Sprintf("Docker Compose installation failed: %v", err))
				}
				PrintCheck("Docker Compose installed")
			} else {
				Fatal("Docker Compose is required. Install it and re-run alf init.")
			}
		} else {
			PrintError("'docker compose' is not available.")
			fmt.Println("\n  Docker Compose v2 is required. It comes bundled with Docker Desktop.")
			os.Exit(1)
		}
	} else {
		PrintCheck("Docker Compose found")
	}
}

func promptDirectory(reader *bufio.Reader, previous string) string {
	home, _ := os.UserHomeDir()
	defaultDir := filepath.Join(home, "alf")
	if previous != "" {
		defaultDir = previous
	}

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
		"data/logs", "data/context",
	}
	for _, sub := range subdirs {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			Fatal(fmt.Sprintf("Failed to create %s: %v", sub, err))
		}
	}

	// Seed bundled skills (e.g. security-audit).
	if err := SeedBundledSkills(dir); err != nil {
		fmt.Printf("  Warning: failed to seed bundled skills: %v\n", err)
	}

	// Seed bundled agent teams (e.g. starter).
	if err := SeedBundledAgents(dir); err != nil {
		fmt.Printf("  Warning: failed to seed bundled agents: %v\n", err)
	}

	PrintCheck(fmt.Sprintf("Directory ready: %s", dir))
	return dir
}

func promptBotToken(reader *bufio.Reader, previous string) (string, string) {
	if previous != "" {
		masked := previous[:8] + "..." + previous[len(previous)-4:]
		fmt.Printf("\n  Previous bot token: %s", masked)
		fmt.Print("\n  Press Enter to keep, or paste a new token: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			name, err := validateBotToken(previous)
			if err != nil {
				PrintWarning(fmt.Sprintf("Previous token no longer valid: %v", err))
			} else {
				PrintCheck(fmt.Sprintf("Bot verified: @%s", name))
				return previous, name
			}
		} else {
			previous = "" // fall through to normal flow with the new input
			if tokenRegex.MatchString(input) {
				if name, err := validateBotToken(input); err == nil {
					PrintCheck(fmt.Sprintf("Bot verified: @%s", name))
					return input, name
				} else {
					PrintError(fmt.Sprintf("Token validation failed: %v", err))
				}
			} else {
				PrintError("Invalid token format. Expected: 123456789:ABCdef...")
			}
		}
	} else {
		fmt.Println("\n  To create a Telegram bot:")
		fmt.Println("    1. Open Telegram, search @BotFather")
		fmt.Println("    2. Send /newbot")
		fmt.Println("    3. Choose a name (e.g. \"My ALF\")")
		fmt.Println("    4. Choose a username (must end in \"bot\")")
		fmt.Println("    5. Copy the token BotFather gives you")
	}

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

func promptChatID(reader *bufio.Reader, botToken string, previous string) string {
	if previous != "" {
		fmt.Printf("\n  Previous chat ID: %s", previous)
		fmt.Print("\n  Press Enter to keep, or enter a new one: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			PrintCheck(fmt.Sprintf("Chat ID: %s", previous))
			return previous
		}
		if chatIDRegex.MatchString(input) {
			PrintCheck(fmt.Sprintf("Chat ID: %s", input))
			return input
		}
		PrintError("Invalid chat ID. Expected a number (e.g. 123456789)")
	} else {
		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates", botToken)
		fmt.Println("\n  To find your chat ID:")
		fmt.Println("    1. Send any message to your bot on Telegram")
		fmt.Printf("    2. Open: %s\n", url)
		fmt.Println("    3. Look for \"chat\":{\"id\":YOUR_CHAT_ID}")
	}

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

func promptPort(reader *bufio.Reader, previous string) string {
	defaultPort := "8080"
	if previous != "" {
		defaultPort = previous
	}

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

func promptHost(reader *bufio.Reader, previous string) string {
	defaultHost := "localhost"
	if previous != "" {
		defaultHost = previous
	} else if hostname, _ := os.Hostname(); hostname != "" {
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

func promptHTTPS(reader *bufio.Reader, previous bool) bool {
	fmt.Println("\n  If you have a public domain pointing to this server,")
	fmt.Println("  ALF can set up automatic HTTPS with Let's Encrypt.")
	if previous {
		fmt.Print("\n  Enable HTTPS with Let's Encrypt? [Y/n]: ")
	} else {
		fmt.Print("\n  Enable HTTPS with Let's Encrypt? [y/N]: ")
	}
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return previous
	}
	return input == "y" || input == "yes"
}

func promptDomain(reader *bufio.Reader, previous string) string {
	for {
		if previous != "" {
			fmt.Printf("\n  Domain name [%s]: ", previous)
		} else {
			fmt.Print("\n  Domain name (e.g. alf.example.com): ")
		}
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "" && previous != "" {
			PrintCheck(fmt.Sprintf("Domain: %s", previous))
			return previous
		}

		if !domainRegex.MatchString(input) {
			PrintError("Invalid domain format. Expected: alf.example.com")
			continue
		}

		PrintCheck(fmt.Sprintf("Domain: %s", input))
		return input
	}
}

func promptAcmeEmail(reader *bufio.Reader, previous string) string {
	for {
		if previous != "" {
			fmt.Printf("  Email for Let's Encrypt [%s]: ", previous)
		} else {
			fmt.Print("  Email for Let's Encrypt notifications: ")
		}
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "" && previous != "" {
			PrintCheck(fmt.Sprintf("Email: %s", previous))
			return previous
		}

		if !emailRegex.MatchString(input) {
			PrintError("Invalid email format.")
			continue
		}

		PrintCheck(fmt.Sprintf("Email: %s", input))
		return input
	}
}

func promptTimezone(reader *bufio.Reader, previous string) string {
	// Auto-detect from system.
	detected := ""
	if tz, err := time.LoadLocation("Local"); err == nil && tz.String() != "Local" && tz.String() != "UTC" {
		detected = tz.String()
	}

	hint := "UTC"
	if previous != "" {
		hint = previous
	} else if detected != "" {
		hint = detected
	}

	fmt.Printf("  Timezone [%s]: ", hint)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		PrintCheck(fmt.Sprintf("Timezone: %s", hint))
		return hint
	}

	if _, err := time.LoadLocation(input); err != nil {
		PrintWarning(fmt.Sprintf("Invalid timezone %q, using %s", input, hint))
		PrintCheck(fmt.Sprintf("Timezone: %s", hint))
		return hint
	}

	PrintCheck(fmt.Sprintf("Timezone: %s", input))
	return input
}

func checkPortsForHTTPS() {
	for _, port := range []string{"80", "443"} {
		if !isPortAvailable(port) {
			PrintWarning(fmt.Sprintf("Port %s is in use. Traefik needs ports 80 and 443 to be free.", port))
		}
	}
}

func generateFiles(dir, botToken, chatID string, compose ComposeData) {
	// Reclaim ownership of directories that fixVolumePermissions may have
	// changed to uid 1000 on a previous run, so the host user can write.
	if runtime.GOOS == "linux" {
		uid := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
		for _, sub := range []string{"config.d", "data"} {
			p := filepath.Join(dir, sub)
			if _, err := os.Stat(p); err == nil {
				cmd := exec.Command("sudo", "chown", "-R", uid, p)
				cmd.Run()
			}
		}
	}

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

	if err := RenderConfig(dir, ConfigData{ChatID: chatID, Timezone: compose.Timezone}); err != nil {
		Fatal(fmt.Sprintf("Failed to write config.json: %v", err))
	}
	PrintCheck("config.json")

	// Write config README if it doesn't exist.
	readmePath := filepath.Join(dir, "config.d", "README.md")
	if _, err := os.Stat(readmePath); os.IsNotExist(err) {
		os.WriteFile(readmePath, []byte(configReadme), 0o644)
	}

	// Fix volume permissions last — after all files are written.
	// chown to uid 1000 (node user inside container) so Docker volumes work.
	fixVolumePermissions(dir)
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
	if img := os.Getenv("ALF_IMAGE"); img != "" {
		PrintInfo(fmt.Sprintf("Using local image: %s", img))
	} else {
		PrintInfo("Pulling ALF image...")
		pull := exec.Command("docker", "compose", "pull")
		pull.Dir = dir
		pull.Stdout = os.Stdout
		pull.Stderr = os.Stderr
		if err := pull.Run(); err != nil {
			Fatal(fmt.Sprintf("Failed to pull image: %v", err))
		}
		PrintCheck("Image pulled")
	}

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
