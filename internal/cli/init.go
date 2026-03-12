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
	"strconv"
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
| tools | []string | [] | Allowed tools (empty = all) |

## sessions.json

Active Control Center login sessions. Managed automatically.
`
var chatIDRegex = regexp.MustCompile(`^-?\d+$`)
var domainRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)+$`)
var emailRegex = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// setupProfile stores previous init values so re-running `alf init` pre-fills them.
type setupProfile struct {
	Dir                string   `json:"dir,omitempty"`
	BotToken           string   `json:"bot_token,omitempty"`
	ChatID             string   `json:"chat_id,omitempty"`
	HTTPS              bool     `json:"https,omitempty"`
	Domain             string   `json:"domain,omitempty"`
	AcmeEmail          string   `json:"acme_email,omitempty"`
	Port               string   `json:"port,omitempty"`
	Host               string   `json:"host,omitempty"`
	Timezone           string   `json:"timezone,omitempty"`
	OpenRouterKey      bool     `json:"openrouter_key,omitempty"`      // true if key was set (backward compat)
	ConfiguredBackends []string `json:"configured_backends,omitempty"` // ["openrouter", "openai"]
	Workspaces         []string `json:"workspaces,omitempty"`          // host paths to mount
	JSRuntime          string   `json:"js_runtime,omitempty"`          // "node", "deno", "bun", or ""
	ImageTag           string   `json:"image_tag,omitempty"`           // Docker image tag override (e.g. "0.6.49")
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

	// Welcome
	PrintBanner()
	fmt.Println("  Welcome to ALF — your personal AI assistant running on your own server.")
	fmt.Println()
	if prev.Dir != "" {
		fmt.Println("  Previous setup detected — press Enter to keep existing values.")
	} else {
		fmt.Println("  This wizard will guide you through the initial setup:")
		fmt.Println()
		fmt.Println("    1. Check prerequisites (Docker)")
		fmt.Println("    2. Choose where to install ALF")
		fmt.Println("    3. Connect Telegram (optional)")
		fmt.Println("    4. Set up your Control Center (web dashboard)")
		fmt.Println("    5. Timezone")
		fmt.Println("    6. Workspaces")
		fmt.Println("    7. JavaScript runtime")
		fmt.Println("    8. Start ALF")
		fmt.Println("    9. LLM providers")
		fmt.Println()
		fmt.Println("  It takes about 2 minutes.")
	}

	step := 0
	nextStep := func(title string) {
		step++
		PrintStep(step, title)
	}

	// Step 1: Prerequisites
	nextStep("Checking prerequisites")
	checkPrerequisites()

	// Step 2: Install directory
	nextStep("Choose install directory")
	dir := promptDirectory(reader, prev.Dir)

	// Step 3: Telegram (optional)
	nextStep("Telegram integration (optional)")
	botToken, botName, chatID := promptTelegram(reader, prev.BotToken, prev.ChatID)
	telegramEnabled := botToken != "" && chatID != ""
	if telegramEnabled {
		PrintCheck(fmt.Sprintf("Telegram enabled — bot @%s, chat %s", botName, chatID))
	} else {
		PrintInfo("Telegram skipped — you can configure it later via the Control Center")
	}

	// Step 4: Control Center access
	nextStep("Control Center access")
	fmt.Println()
	fmt.Println("  The Control Center is your web dashboard to manage ALF:")
	fmt.Println("  chat, configure tiers, schedule tasks, manage skills, and more.")
	var composeData ComposeData
	if promptHTTPS(reader, prev.HTTPS) {
		domain := promptDomain(reader, prev.Domain)
		acmeEmail := promptAcmeEmail(reader, prev.AcmeEmail)
		fmt.Println()
		PrintInfo("Make sure ports 80 and 443 are open on your server/firewall.")
		PrintInfo("Traefik needs both ports for HTTPS certificate generation (ACME).")
		checkPortsForHTTPS()
		composeData = ComposeData{
			EnableHTTPS:   true,
			Domain:        domain,
			AcmeEmail:     acmeEmail,
			CCExternalURL: fmt.Sprintf("https://%s", domain),
		}
		if err := os.MkdirAll(filepath.Join(dir, "letsencrypt"), 0o755); err != nil {
			Fatal(fmt.Sprintf("Failed to create letsencrypt/: %v", err))
		}
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
		saveSetupProfile(setupProfile{
			Dir: dir, BotToken: botToken, ChatID: chatID,
			Port: ccPort, Host: ccHost,
		})
	}

	// Step 5: Timezone
	nextStep("Timezone")
	tz := promptTimezone(reader, prev.Timezone)
	composeData.Timezone = tz
	profile := loadSetupProfile()
	profile.Timezone = tz
	saveSetupProfile(profile)

	// Step 6: Workspaces
	nextStep("Workspaces")
	workspaces := promptWorkspaces(reader, prev.Workspaces)
	composeData.Workspaces = workspaces
	profile = loadSetupProfile()
	profile.Workspaces = workspaces
	saveSetupProfile(profile)

	// Step 7: JS Runtime
	nextStep("JavaScript runtime")
	jsRuntime := promptJSRuntime(reader, prev.JSRuntime)
	composeData.JSRuntime = jsRuntime
	profile = loadSetupProfile()
	profile.JSRuntime = jsRuntime
	saveSetupProfile(profile)

	// Docker image version
	imageTag := promptImageTag(reader, prev.ImageTag)
	if imageTag == "" || imageTag == "latest" {
		composeData.Image = "ghcr.io/alamparelli/alf:latest"
	} else {
		composeData.Image = "ghcr.io/alamparelli/alf:" + imageTag
	}
	if img := os.Getenv("ALF_IMAGE"); img != "" {
		composeData.Image = img
	}
	profile = loadSetupProfile()
	profile.ImageTag = imageTag
	saveSetupProfile(profile)

	// Step 8: Generate files
	nextStep("Generating configuration files")
	generateFiles(dir, botToken, chatID, composeData)

	// Step 9: Pull & Start
	nextStep("Starting ALF")
	pullAndStart(dir, botName, composeData.EnableHTTPS)

	// Step 10: LLM Providers
	nextStep("LLM Providers")
	promptLLMProviders(reader, dir, &profile)

	// Step 12: Generate magic link for Control Center
	nextStep("Control Center access link")
	magicURL := generateInitMagicLink(dir)

	// Summary
	fmt.Println()
	PrintBanner()
	fmt.Println("  Setup complete!")
	fmt.Println()
	PrintCheck(fmt.Sprintf("Install directory: %s", dir))
	if telegramEnabled {
		PrintCheck(fmt.Sprintf("Telegram bot: @%s", botName))
	}
	PrintCheck(fmt.Sprintf("Control Center: %s", composeData.CCExternalURL))
	if magicURL != "" {
		fmt.Println()
		fmt.Println("  Open your Control Center with this link (valid 30 days):")
		fmt.Println("  " + colorBold + magicURL + colorReset)
	}
	fmt.Println()
	if telegramEnabled {
		PrintSuccess("You're all set! Message @" + botName + " on Telegram, or open the Control Center.")
	} else {
		PrintSuccess("You're all set! Open the Control Center link above to start using ALF.")
	}
	fmt.Println()
	PrintInfo("Run 'alf help' to see all available commands.")
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
		"data/logs", "data/context", "data/agents/teams",
		"cache/claude", "cache/local", "cache/npm", "cache/cache",
		"local",
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

	// Save install path so all CLI commands can find it (e.g. alf login, alf status).
	SaveInstallDir(dir)

	PrintCheck(fmt.Sprintf("Directory ready: %s", dir))
	return dir
}

func promptTelegram(reader *bufio.Reader, prevToken, prevChatID string) (token, botName, chatID string) {
	fmt.Println()
	fmt.Println("  Telegram lets you chat with ALF from your phone.")
	fmt.Println("  You can skip this and use the Control Center web interface instead.")

	hasPrevious := prevToken != "" && prevChatID != ""
	if hasPrevious {
		fmt.Print("\n  Telegram was previously configured. Keep it? [Y/n]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		if input == "" || input == "y" || input == "yes" {
			token, botName = promptBotToken(reader, prevToken)
			chatID = promptChatID(reader, token, prevChatID)
			return
		}
		// User wants to reconfigure or skip
	}

	if !hasPrevious {
		fmt.Print("\n  Set up Telegram? [y/N]: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		if input != "y" && input != "yes" {
			return "", "", ""
		}
	}

	token, botName = promptBotToken(reader, "")
	chatID = promptChatID(reader, token, "")
	return
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

	// Common timezones for quick selection.
	commonTZs := []string{
		"Europe/London",
		"Europe/Paris",
		"Europe/Berlin",
		"America/New_York",
		"America/Chicago",
		"America/Denver",
		"America/Los_Angeles",
		"Asia/Tokyo",
		"Asia/Shanghai",
		"Australia/Sydney",
	}

	now := time.Now()
	fmt.Printf("  ALF uses your timezone for scheduled tasks and daily routines.\n")
	if loc, err := time.LoadLocation(hint); err == nil {
		fmt.Printf("  Detected: %s (currently %s)\n", hint, now.In(loc).Format("15:04"))
	}

	fmt.Println()
	fmt.Printf("  Common timezones:\n")
	for i, tz := range commonTZs {
		loc, _ := time.LoadLocation(tz)
		marker := "  "
		if tz == hint {
			marker = "→ "
		}
		fmt.Printf("  %s%2d) %-22s %s\n", marker, i+1, tz, now.In(loc).Format("15:04"))
	}

	fmt.Println()
	fmt.Printf("  Pick a number, type a timezone, or press Enter for [%s]: ", hint)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		PrintCheck(fmt.Sprintf("Timezone: %s", hint))
		return hint
	}

	// Check if input is a number selecting from the list.
	if n, err := strconv.Atoi(input); err == nil && n >= 1 && n <= len(commonTZs) {
		chosen := commonTZs[n-1]
		PrintCheck(fmt.Sprintf("Timezone: %s", chosen))
		return chosen
	}

	if _, err := time.LoadLocation(input); err != nil {
		PrintWarning(fmt.Sprintf("Invalid timezone %q — expected format like Europe/Paris or America/New_York", input))
		PrintCheck(fmt.Sprintf("Using: %s", hint))
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
	if botToken != "" {
		if err := SetSecret(dir, "telegram_bot_token", botToken); err != nil {
			Fatal(fmt.Sprintf("Failed to write secret: %v", err))
		}
		PrintCheck("secrets/telegram_bot_token")
	}

	if chatID != "" {
		if err := SetSecret(dir, "telegram_chat_id", chatID); err != nil {
			Fatal(fmt.Sprintf("Failed to write secret: %v", err))
		}
		PrintCheck("secrets/telegram_chat_id")
	}

	// Generate Control Center auth token.
	ccToken, err := generateAuthToken()
	if err != nil {
		Fatal(fmt.Sprintf("Failed to generate auth token: %v", err))
	}
	if err := SetSecret(dir, "cc_auth_token", ccToken); err != nil {
		Fatal(fmt.Sprintf("Failed to write secret: %v", err))
	}
	PrintCheck("secrets/cc_auth_token")

	// Ensure optional secret files exist (even empty) so docker-compose
	// doesn't fail on missing file references.
	ensureOptionalSecrets(dir)

	if err := RenderDockerCompose(dir, compose); err != nil {
		Fatal(fmt.Sprintf("Failed to write docker-compose.yml: %v", err))
	}
	PrintCheck("docker-compose.yml")

	if err := RenderConfig(dir, ConfigData{ChatID: chatID, Timezone: compose.Timezone}); err != nil {
		Fatal(fmt.Sprintf("Failed to write config.json: %v", err))
	}
	PrintCheck("config.json")

	if err := SeedTiersConfig(dir); err != nil {
		Fatal(fmt.Sprintf("Failed to write tiers.json: %v", err))
	}
	PrintCheck("tiers.json")

	if err := SeedBootstrapScript(dir); err != nil {
		Fatal(fmt.Sprintf("Failed to write bootstrap.sh: %v", err))
	}
	PrintCheck("bootstrap.sh")

	// Write runtime.txt with JS runtime choice (if selected).
	writeRuntimeTxt(dir, compose.JSRuntime)

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
				if botName != "" {
					PrintSuccess(fmt.Sprintf("Setup complete! Send a message to @%s on Telegram.", botName))
				} else {
					PrintSuccess("Setup complete!")
				}
				return
			}
		}
	}

	PrintWarning("ALF started but health check inconclusive. Check with: alf status")
}

// backendOption defines a backend choice in the init wizard.
type backendOption struct {
	Key       string // "openrouter", "openai", "custom"
	Name      string
	BaseURL   string // pre-filled for known backends
	SecretName string // secret file name for the API key
	KeyPrefix string // expected key prefix for validation hint
}

// llmProvider defines a provider choice in the init wizard.
type llmProvider struct {
	Key        string
	Name       string
	IsDefault  bool   // selected by default (Claude)
	IsClaude   bool   // needs Claude auth flow instead of API key
	BaseURL    string
	SecretName string
	KeyPrefix  string
}

var llmProviders = []llmProvider{
	{Key: "claude", Name: "Claude (Anthropic subscription / Claude Code)", IsDefault: true, IsClaude: true},
	{Key: "openrouter", Name: "OpenRouter (GPT, Gemini, Llama, etc.)", BaseURL: "https://openrouter.ai/api/v1", SecretName: "openrouter_api_key", KeyPrefix: "sk-or-"},
	{Key: "openai", Name: "OpenAI (GPT-4o, o1, etc.)", BaseURL: "https://api.openai.com/v1", SecretName: "openai_api_key", KeyPrefix: "sk-"},
	{Key: "ollama", Name: "Ollama (local models)", BaseURL: "http://host.docker.internal:11434/v1"},
	{Key: "custom", Name: "Other (custom OpenAI-compatible endpoint)"},
}

func promptLLMProviders(reader *bufio.Reader, dir string, profile *setupProfile) {
	fmt.Println()
	fmt.Println("  Choose which LLM providers to configure.")
	fmt.Println("  Claude is selected by default. You can add others now or later via the Control Center.")
	fmt.Println()

	// Show previous config.
	if len(profile.ConfiguredBackends) > 0 {
		fmt.Printf("  Previously configured: %s\n\n", strings.Join(profile.ConfiguredBackends, ", "))
	}

	for i, p := range llmProviders {
		marker := " "
		if p.IsDefault {
			marker = "*"
		}
		fmt.Printf("  %s %d) %s\n", marker, i+1, p.Name)
	}
	fmt.Println()
	fmt.Println("  Enter numbers separated by commas (e.g. 1,2,3).")
	fmt.Print("  Providers [1]: ")

	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	// Default: just Claude.
	if input == "" {
		input = "1"
	}

	var configured []string
	parts := strings.Split(input, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		idx, err := strconv.Atoi(p)
		if err != nil || idx < 1 || idx > len(llmProviders) {
			PrintWarning(fmt.Sprintf("Invalid choice: %s (skipped)", p))
			continue
		}
		prov := llmProviders[idx-1]
		if prov.IsClaude {
			// Claude auth flow.
			if isClaudeAuthenticated() {
				PrintCheck("Claude is already authenticated")
			} else {
				fmt.Println()
				fmt.Println("  Claude is the AI that powers ALF. You need to authenticate once")
				fmt.Println("  so ALF can use Claude on your behalf.")
				fmt.Println()
				RunLogin()
			}
			configured = append(configured, "claude")
		} else if prov.Key == "custom" {
			promptCustomBackend(reader, dir, &configured)
		} else if prov.Key == "ollama" {
			PrintCheck("Ollama configured (expects Ollama running on host at port 11434)")
			PrintInfo("Make sure Ollama is running: ollama serve")
			configured = append(configured, "ollama")
		} else {
			promptKnownBackend(reader, dir, backendOption{
				Key:        prov.Key,
				Name:       prov.Name,
				BaseURL:    prov.BaseURL,
				SecretName: prov.SecretName,
				KeyPrefix:  prov.KeyPrefix,
			}, &configured)
		}
	}

	profile.ConfiguredBackends = configured
	for _, name := range configured {
		if name == "openrouter" {
			profile.OpenRouterKey = true
		}
	}
	saveSetupProfile(*profile)
}

func promptKnownBackend(reader *bufio.Reader, dir string, opt backendOption, configured *[]string) {
	existing := secretExists(dir, opt.SecretName)
	fmt.Printf("\n  %s API key", opt.Name)
	if existing {
		fmt.Print(" (press Enter to keep existing): ")
	} else {
		fmt.Print(": ")
	}

	key, _ := reader.ReadString('\n')
	key = strings.TrimSpace(key)
	if key == "" {
		if existing {
			PrintCheck(fmt.Sprintf("Keeping existing %s key", opt.Key))
			*configured = append(*configured, opt.Key)
		} else {
			PrintWarning(fmt.Sprintf("No %s API key provided — skipped", opt.Key))
		}
		return
	}

	if opt.KeyPrefix != "" && !strings.HasPrefix(key, opt.KeyPrefix) {
		PrintWarning(fmt.Sprintf("Key doesn't start with %s — saving anyway", opt.KeyPrefix))
	}
	if err := SetSecret(dir, opt.SecretName, key); err != nil {
		PrintError(fmt.Sprintf("Failed to save %s key: %v", opt.Key, err))
		return
	}
	PrintCheck(fmt.Sprintf("%s API key saved", opt.Name))
	*configured = append(*configured, opt.Key)
}

func promptCustomBackend(reader *bufio.Reader, dir string, configured *[]string) {
	fmt.Print("\n  Backend name (e.g. ollama, together): ")
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		PrintWarning("No name provided — skipped")
		return
	}

	fmt.Print("  Base URL (e.g. http://localhost:11434/v1): ")
	baseURL, _ := reader.ReadString('\n')
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		PrintWarning("No base URL provided — skipped")
		return
	}

	fmt.Print("  Requires API key? [y/N]: ")
	authInput, _ := reader.ReadString('\n')
	authInput = strings.TrimSpace(strings.ToLower(authInput))

	if authInput == "y" || authInput == "yes" {
		secretName := name + "_api_key"
		fmt.Printf("  API key for %s: ", name)
		key, _ := reader.ReadString('\n')
		key = strings.TrimSpace(key)
		if key != "" {
			if err := SetSecret(dir, secretName, key); err != nil {
				PrintError(fmt.Sprintf("Failed to save %s key: %v", name, err))
				return
			}
			PrintCheck(fmt.Sprintf("%s API key saved", name))
		}
	}

	PrintCheck(fmt.Sprintf("Custom backend %q configured (base_url: %s)", name, baseURL))
	PrintInfo(fmt.Sprintf("Add this to config.json backends section: %q: {\"base_url\": \"%s\"}", name, baseURL))
	*configured = append(*configured, name)
}

func promptWorkspaces(reader *bufio.Reader, previous []string) []string {
	fmt.Println("\n  Mount host directories as workspaces so ALF can access and code in your repos.")
	if len(previous) > 0 {
		fmt.Println("  Current workspaces:")
		for _, w := range previous {
			fmt.Printf("    - %s\n", w)
		}
	}
	fmt.Println("  Enter absolute paths, one per line. Empty line to finish.")

	var workspaces []string
	// Pre-fill with previous if user just presses Enter on first line.
	firstLine := true
	for {
		fmt.Print("  Path: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			if firstLine && len(previous) > 0 {
				PrintCheck(fmt.Sprintf("Keeping %d workspace(s)", len(previous)))
				return previous
			}
			break
		}
		firstLine = false

		// Expand ~ to home dir.
		if strings.HasPrefix(input, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				input = filepath.Join(home, input[2:])
			}
		}

		// Validate path exists.
		info, err := os.Stat(input)
		if err != nil {
			PrintWarning(fmt.Sprintf("Path does not exist: %s (skipped)", input))
			continue
		}
		if !info.IsDir() {
			PrintWarning(fmt.Sprintf("Not a directory: %s (skipped)", input))
			continue
		}

		workspaces = append(workspaces, input)
		PrintCheck(fmt.Sprintf("Added: %s → /workspaces/%s", input, filepath.Base(input)))
	}

	if len(workspaces) == 0 {
		fmt.Println("  No workspaces configured.")
	}
	return workspaces
}

func promptImageTag(reader *bufio.Reader, previous string) string {
	def := "latest"
	if previous != "" {
		def = previous
	}
	fmt.Printf("\n  Docker image version [%s]: ", def)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return def
	}
	// Strip leading "v" if user types "v0.6.49"
	input = strings.TrimPrefix(input, "v")
	PrintCheck(fmt.Sprintf("Image: ghcr.io/alamparelli/alf:%s", input))
	return input
}

func writeRuntimeTxt(dir, jsRuntime string) {
	runtimePath := filepath.Join(dir, "config.d", "runtime.txt")
	if jsRuntime == "" {
		// Remove runtime.txt if no runtime selected.
		os.Remove(runtimePath)
		return
	}
	content := fmt.Sprintf("# JS runtime installed at container startup\n%s\n", jsRuntime)
	if err := os.WriteFile(runtimePath, []byte(content), 0o644); err != nil {
		PrintWarning(fmt.Sprintf("Failed to write runtime.txt: %v", err))
		return
	}
	PrintCheck("config.d/runtime.txt (" + jsRuntime + ")")
}

var jsRuntimeOptions = []struct {
	Key  string
	Name string
}{
	{"", "None"},
	{"node", "Node.js (nodejs + npm)"},
	{"deno", "Deno"},
	{"bun", "Bun"},
}

func promptJSRuntime(reader *bufio.Reader, previous string) string {
	fmt.Println("\n  JavaScript runtime for the container:")
	for i, opt := range jsRuntimeOptions {
		marker := "  "
		if opt.Key == previous {
			marker = "* "
		}
		fmt.Printf("    %s%d) %s\n", marker, i, opt.Name)
	}

	defaultIdx := 0
	for i, opt := range jsRuntimeOptions {
		if opt.Key == previous {
			defaultIdx = i
			break
		}
	}

	fmt.Printf("  Choice [%d]: ", defaultIdx)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		choice := jsRuntimeOptions[defaultIdx].Key
		if choice == "" {
			PrintCheck("No JS runtime")
		} else {
			PrintCheck("JS runtime: " + choice)
		}
		return choice
	}

	idx := 0
	if _, err := fmt.Sscanf(input, "%d", &idx); err != nil || idx < 0 || idx >= len(jsRuntimeOptions) {
		PrintWarning("Invalid choice, keeping default")
		return previous
	}

	choice := jsRuntimeOptions[idx].Key
	if choice == "" {
		PrintCheck("No JS runtime")
	} else {
		PrintCheck("JS runtime: " + choice)
	}
	return choice
}

// generateInitMagicLink generates a 30-day magic link via the daemon API.
// Returns the URL or empty string on failure (non-fatal).
func generateInitMagicLink(dir string) string {
	tokenFile := filepath.Join(dir, "secrets", "cc_auth_token")
	tokenBytes, err := os.ReadFile(tokenFile)
	if err != nil || strings.TrimSpace(string(tokenBytes)) == "" {
		PrintWarning("Could not read cc_auth_token — skipping magic link")
		return ""
	}
	token := strings.TrimSpace(string(tokenBytes))

	cmd := exec.Command("docker", "exec", "alf",
		"curl", "-sf", "-X", "POST",
		"-H", "Authorization: Bearer "+token,
		"http://127.0.0.1:8080/api/magic-link?days=30")
	out, err := cmd.Output()
	if err != nil {
		PrintWarning("Could not generate magic link — try: alf magic-link")
		return ""
	}

	var resp struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || resp.URL == "" {
		PrintWarning("Invalid magic link response — try: alf magic-link")
		return ""
	}
	return resp.URL
}

// RunLogin launches Claude Code interactively so the user can authenticate
// via /login. Auth persists in the .claude volume mount.
func RunLogin() {
	PrintInfo("Launching Claude Code for authentication...")
	fmt.Println("  Type " + colorBold + "/login" + colorReset + " inside Claude to authenticate, then " + colorBold + "/exit" + colorReset + " when done.")
	fmt.Println()

	cmd := exec.Command("docker", "exec", "-it", "--user", "1000:1000", "-e", "HOME=/home/alf", "alf", "claude")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()

	fixClaudeOwnership()
	verifyClaudeAuth()
}

func fixClaudeOwnership() {
	fix := exec.Command("docker", "exec", "alf",
		"sh", "-c", `chown -R 1000:1000 /home/alf/.claude /home/alf/.claude.json 2>/dev/null
chmod 640 /home/alf/.claude.json 2>/dev/null
chmod -R g+rX /home/alf/.claude 2>/dev/null
cp /home/alf/.claude.json /home/alf/.claude/claude.json 2>/dev/null
true`)
	fix.Run()
}

func isClaudeAuthenticated() bool {
	verify := exec.Command("docker", "exec", "--user", "1000:1000", "-e", "HOME=/home/alf",
		"alf", "claude", "-p", "ping", "--output-format", "json", "--max-turns", "1")
	out, _ := verify.Output()
	return len(out) > 0 && strings.Contains(string(out), `"is_error":false`)
}

func verifyClaudeAuth() {
	if isClaudeAuthenticated() {
		PrintCheck("Claude authenticated")
	} else {
		PrintWarning("Claude may not be authenticated yet. Try running: alf login")
	}
}

