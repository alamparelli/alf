// system-tools is a multi-call binary that bridges CLI tool invocations
// to the daemon's CC HTTP API. It's installed in tools.d/ with symlinks
// for each tool name (task, team, skill, app, config, tier, log, search).
//
// When Claude CLI invokes a tool like `task launch "analyze logs"`,
// this binary sends the request to the daemon HTTP API and returns the result.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	cc "github.com/alamparelli/alf/internal/controlcenter"
)

var ccBaseURL = "http://localhost:" + cc.DefaultPort

func main() {
	if url := os.Getenv("ALF_CC_URL"); url != "" {
		ccBaseURL = url
	}

	// Multi-call: determine tool name from argv[0].
	name := filepath.Base(os.Args[0])
	// Strip -tools suffix if called directly.
	name = strings.TrimSuffix(name, "-tools")
	name = strings.TrimPrefix(name, "system-")

	args := os.Args[1:]

	// Handle --help for all tools.
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		printHelp(name)
		return
	}

	switch name {
	case "task":
		handleAction(name, "/api/tasks", args)
	case "team":
		handleAction(name, "/api/teams", args)
	case "skill":
		handleAction(name, "/api/skills/catalog", args)
	case "app":
		handleApp(args)
	case "config":
		handleConfig(args)
	case "tier":
		handleSimpleGet("/api/tiers")
	case "log":
		handleLog(args)
	case "search":
		handleSearch(args)
	case "llm":
		handleLLM(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown tool: %s\n", name)
		os.Exit(1)
	}
}

func handleAction(tool, endpoint string, args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: %s <action> [options]\n", tool)
		os.Exit(1)
	}
	action := args[0]
	params := parseFlags(args[1:])
	params["action"] = action

	switch action {
	case "list":
		result, err := doGet(endpoint)
		if err != nil {
			fatal(err)
		}
		fmt.Println(result)

	case "chain":
		if tool == "task" {
			stepsJSON := params["steps"]
			if stepsJSON == "" {
				fatal(fmt.Errorf("--steps is required for chain (JSON array of {tier, prompt})"))
			}
			var steps []map[string]string
			if err := json.Unmarshal([]byte(stepsJSON), &steps); err != nil {
				fatal(fmt.Errorf("invalid --steps JSON: %w", err))
			}
			reqBody := map[string]any{"steps": steps}
			if originEnv := os.Getenv("ALF_CHAIN_ORIGIN"); originEnv != "" {
				parts := strings.SplitN(originEnv, ":", 2)
				origin := map[string]string{"source": parts[0]}
				if len(parts) > 1 {
					origin["conv_id"] = parts[1]
				}
				reqBody["origin"] = origin
			}
			body, _ := json.Marshal(reqBody)
			result, err := doPost("/api/tasks/chain", body)
			if err != nil {
				fatal(err)
			}
			fmt.Println(result)
		}

	case "launch":
		if tool == "task" {
			payload := map[string]any{
				"message":         params["prompt"],
				"need_validation": params["need_validation"] == "true",
			}
			if t := params["team"]; t != "" {
				payload["team"] = t
			}
			body, _ := json.Marshal(payload)
			result, err := doPost(endpoint, body)
			if err != nil {
				fatal(err)
			}
			fmt.Println(result)
		}

	case "get":
		name := params["name"]
		if name == "" && len(args) > 1 {
			name = args[1]
		}
		result, err := doGet(endpoint + "?name=" + name)
		if err != nil {
			fatal(err)
		}
		fmt.Println(result)

	case "cancel", "delete":
		id := params["id"]
		if id == "" && len(args) > 1 {
			id = args[1]
		}
		result, err := doDelete(endpoint + "?id=" + id + "&action=" + action)
		if err != nil {
			fatal(err)
		}
		fmt.Println(result)

	case "save":
		body, _ := json.Marshal(typedParams(params))
		result, err := doPut(endpoint, body)
		if err != nil {
			fatal(err)
		}
		fmt.Println(result)

	default:
		fmt.Fprintf(os.Stderr, "unknown action: %s\n", action)
		os.Exit(1)
	}
}

func handleApp(args []string) {
	if len(args) == 0 {
		result, _ := doGet("/api/apps/")
		fmt.Println(result)
		return
	}
	action := args[0]
	switch action {
	case "list":
		result, _ := doGet("/api/apps/")
		fmt.Println(result)
	case "catalog":
		result, _ := doGet("/api/marketplace/catalog")
		fmt.Println(result)
	case "install", "update", "enable", "disable", "uninstall":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: app %s <slug>\n", action)
			os.Exit(1)
		}
		slug := args[1]
		result, err := doPost(fmt.Sprintf("/api/marketplace/%s/%s", slug, action), nil)
		if err != nil {
			fatal(err)
		}
		fmt.Println(result)
	default:
		fmt.Fprintf(os.Stderr, "unknown action: %s\n", action)
		os.Exit(1)
	}
}

func handleLog(args []string) {
	if len(args) == 0 || args[0] == "list" {
		result, _ := doGet("/api/logs")
		fmt.Println(result)
		return
	}
	if args[0] == "tail" && len(args) > 1 {
		name := args[1]
		lines := "100"
		if len(args) > 2 {
			lines = args[2]
		}
		result, err := doGet(fmt.Sprintf("/api/logs?name=%s&n=%s", name, lines))
		if err != nil {
			fatal(err)
		}
		fmt.Println(result)
		return
	}
	// Treat first arg as log name for convenience.
	result, err := doGet(fmt.Sprintf("/api/logs?name=%s&n=100", args[0]))
	if err != nil {
		fatal(err)
	}
	fmt.Println(result)
}

func handleSearch(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: search <query> [--types apps,files,docs]")
		os.Exit(1)
	}
	query := args[0]
	params := parseFlags(args[1:])
	url := "/api/search?q=" + query
	if t, ok := params["types"]; ok {
		url += "&types=" + t
	}
	result, err := doGet(url)
	if err != nil {
		fatal(err)
	}
	fmt.Println(result)
}

func handleLLM(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: llm <tier> <prompt> [--system S] [--fire-and-forget --on-complete '<json>' --max-depth N]")
		os.Exit(1)
	}
	tier := args[0]
	prompt := args[1]
	params := parseFlags(args[2:])

	body := map[string]any{
		"tier":   tier,
		"prompt": prompt,
	}
	if sys, ok := params["system"]; ok {
		body["system"] = sys
	}
	if _, ok := params["fire-and-forget"]; ok {
		body["fire_and_forget"] = true

		if oc, ok := params["on-complete"]; ok {
			var onComplete json.RawMessage
			if err := json.Unmarshal([]byte(oc), &onComplete); err != nil {
				fmt.Fprintf(os.Stderr, "invalid --on-complete JSON: %v\n", err)
				os.Exit(1)
			}
			body["on_complete"] = onComplete
		}
		if md, ok := params["max-depth"]; ok {
			n, err := strconv.Atoi(md)
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid --max-depth: %v\n", err)
				os.Exit(1)
			}
			body["max_depth"] = n
		}
	}

	data, _ := json.Marshal(body)
	result, err := doPost("/api/llm/invoke", data)
	if err != nil {
		fatal(err)
	}
	// Extract text from JSON response (sync mode) or print raw (async mode).
	var resp struct {
		Text    string `json:"text"`
		ChainID string `json:"chain_id"`
		Status  string `json:"status"`
	}
	if json.Unmarshal([]byte(result), &resp) == nil {
		if resp.ChainID != "" {
			fmt.Printf("Chain %s %s\n", resp.ChainID, resp.Status)
		} else if resp.Text != "" {
			fmt.Println(resp.Text)
		} else {
			fmt.Println(result)
		}
	} else {
		fmt.Println(result)
	}
}

func handleSimpleGet(endpoint string) {
	result, err := doGet(endpoint)
	if err != nil {
		fatal(err)
	}
	fmt.Println(result)
}

// --- HTTP helpers ---

// httpClient returns an HTTP client that connects via Unix socket (ALF_TOOLS_SOCK)
// or falls back to TCP with Bearer auth (ALF_CC_URL / CC_AUTH_TOKEN).
func httpClient() *http.Client {
	if sock := os.Getenv("ALF_TOOLS_SOCK"); sock != "" {
		return &http.Client{
			Timeout: 10 * time.Minute,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", sock)
				},
			},
		}
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func doGet(path string) (string, error) {
	return doRequest("GET", path, nil)
}

func doPost(path string, body []byte) (string, error) {
	return doRequest("POST", path, body)
}

func doPut(path string, body []byte) (string, error) {
	return doRequest("PUT", path, body)
}

func doDelete(path string) (string, error) {
	return doRequest("DELETE", path, nil)
}

func doRequest(method, path string, body []byte) (string, error) {
	// When using Unix socket, the host part is ignored — use a dummy host.
	base := ccBaseURL
	if os.Getenv("ALF_TOOLS_SOCK") != "" {
		base = "http://tools"
	}
	url := base + path
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Requested-With", "system-tools")
	// Fallback: Bearer auth when not using socket transport.
	if os.Getenv("ALF_TOOLS_SOCK") == "" {
		if tok := os.Getenv("CC_AUTH_TOKEN"); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}

	resp, err := httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}

	// Try to pretty-print JSON.
	var v any
	if json.Unmarshal(data, &v) == nil {
		pretty, err := json.MarshalIndent(v, "", "  ")
		if err == nil {
			return string(pretty), nil
		}
	}
	return string(data), nil
}

// typedParams converts string params to their proper JSON types.
// JSON arrays/objects are parsed as json.RawMessage, numeric strings as numbers.
func typedParams(params map[string]string) map[string]any {
	result := make(map[string]any, len(params))
	for k, v := range params {
		trimmed := strings.TrimSpace(v)
		// Try JSON array or object.
		if (strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) ||
			(strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) {
			if json.Valid([]byte(trimmed)) {
				result[k] = json.RawMessage(trimmed)
				continue
			}
		}
		// Try integer.
		if n, err := strconv.Atoi(trimmed); err == nil {
			result[k] = n
			continue
		}
		// Try boolean.
		if trimmed == "true" || trimmed == "false" {
			result[k] = trimmed == "true"
			continue
		}
		result[k] = v
	}
	return result
}

func parseFlags(args []string) map[string]string {
	result := make(map[string]string)
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			key := strings.TrimPrefix(args[i], "--")
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				result[key] = args[i+1]
				i++
			} else {
				result[key] = "true"
			}
		}
	}
	return result
}

func printHelp(name string) {
	helps := map[string]string{
		"task":   "Manage agent tasks and LLM chains.\n  task chain --steps '[{\"tier\":\"haiku\",\"prompt\":\"fact\"},{\"tier\":\"sonnet\",\"prompt\":\"tweet: {result}\"}]'\n  task launch --prompt \"objective\" [--team T]\n  task list\n  task cancel <id>\n  task delete <id>\n  task approve <id> --approved true [--feedback F]",
		"team":   "Manage agent teams.\n  team list\n  team get <name>\n  team save --name N --agents '[{\"name\":\"a\",\"tier\":\"haiku\"}]'\n  team delete <name>",
		"skill":  "Browse skills.\n  skill list\n  skill get <name>",
		"app":    "Manage apps.\n  app list\n  app catalog\n  app install <slug>\n  app enable/disable/uninstall <slug>",
		"config": "System configuration and avatar.\n  config                        Show configuration\n  config avatar-set <base64>    Upload profile avatar (PNG/JPEG/WebP, max 256KB)\n  config avatar-reset           Remove custom avatar\n  config avatar-status          Check if custom avatar is set",
		"tier":   "Show tiers.\n  tier",
		"log":    "Access logs.\n  log list\n  log tail <name> [lines]",
		"search": "Search workspace.\n  search <query> [--types apps,files,docs]",
		"llm":    "Invoke an LLM tier.\n  llm <tier> <prompt> [--system <system_prompt>]\n  llm <tier> <prompt> --fire-and-forget --max-depth N --on-complete '<json>'\n\nExamples:\n  llm haiku \"summarize this\" --system \"Be concise\"\n  llm haiku \"extract TODOs\" --fire-and-forget --max-depth 2 --on-complete '{\"tier\":\"sonnet\",\"prompt\":\"generate tests for:\\n{result}\"}'",
	}
	if h, ok := helps[name]; ok {
		fmt.Println(h)
	} else {
		fmt.Printf("Usage: %s <action> [options]\n", name)
	}
}

func handleConfig(args []string) {
	// No args or "get" → show config.
	if len(args) == 0 || args[0] == "get" {
		handleSimpleGet("/api/config")
		return
	}

	// Avatar sub-commands: config avatar-set <base64>, config avatar-reset, config avatar-status.
	switch args[0] {
	case "avatar-set":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: config avatar-set <base64_image>")
			os.Exit(1)
		}
		body, _ := json.Marshal(map[string]string{"image": args[1]})
		result, err := doRequest("PUT", "/api/settings/avatar", body)
		if err != nil {
			fatal(err)
		}
		fmt.Println(result)
	case "avatar-reset":
		result, err := doRequest("DELETE", "/api/settings/avatar", nil)
		if err != nil {
			fatal(err)
		}
		fmt.Println(result)
	case "avatar-status":
		_, err := doRequest("GET", "/api/settings/avatar", nil)
		if err != nil {
			fmt.Println("Using default avatar.")
		} else {
			fmt.Println("Custom avatar is set.")
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown config action: %s (valid: get, avatar-set, avatar-reset, avatar-status)\n", args[0])
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
