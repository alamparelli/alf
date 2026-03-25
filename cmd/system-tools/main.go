// system-tools is a multi-call binary that bridges CLI tool invocations
// to the daemon's CC HTTP API. It's installed in tools.d/ with symlinks
// for each tool name (task, team, skill, app, config, tier, log, search).
//
// When Claude CLI invokes a tool like `task launch "analyze logs"`,
// this binary sends the request to the daemon HTTP API and returns the result.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ccBaseURL = "http://localhost:8080"

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
		handleSimpleGet("/api/config")
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

	case "launch":
		if tool == "task" {
			body, _ := json.Marshal(map[string]any{
				"message":         params["prompt"],
				"need_validation": params["need_validation"] == "true",
			})
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
		body, _ := json.Marshal(params)
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
		fmt.Fprintln(os.Stderr, "usage: llm <tier> <prompt> [--system <system_prompt>]")
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

	data, _ := json.Marshal(body)
	result, err := doPost("/api/llm/invoke", data)
	if err != nil {
		fatal(err)
	}
	// Extract text from JSON response.
	var resp struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(result), &resp) == nil && resp.Text != "" {
		fmt.Println(resp.Text)
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

func authToken() string {
	return os.Getenv("CC_AUTH_TOKEN")
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
	url := ccBaseURL + path
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
	if tok := authToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
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
		"task":   "Manage agent tasks.\n  task launch --prompt \"objective\" [--tier T] [--team T] [--skills S]\n  task list\n  task cancel <id>\n  task delete <id>\n  task approve <id> --approved true [--feedback F]",
		"team":   "Manage agent teams.\n  team list\n  team get <name>\n  team save --name N --agents '[{\"name\":\"a\",\"tier\":\"haiku\"}]'\n  team delete <name>",
		"skill":  "Browse skills.\n  skill list\n  skill get <name>",
		"app":    "Manage apps.\n  app list\n  app catalog\n  app install <slug>\n  app enable/disable/uninstall <slug>",
		"config": "Show configuration.\n  config",
		"tier":   "Show tiers.\n  tier",
		"log":    "Access logs.\n  log list\n  log tail <name> [lines]",
		"search": "Search workspace.\n  search <query> [--types apps,files,docs]",
		"llm":    "Invoke an LLM tier.\n  llm <tier> <prompt> [--system <system_prompt>]",
	}
	if h, ok := helps[name]; ok {
		fmt.Println(h)
	} else {
		fmt.Printf("Usage: %s <action> [options]\n", name)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
