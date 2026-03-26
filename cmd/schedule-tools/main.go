package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type socketRequest struct {
	Action   string            `json:"action"`
	Name     string            `json:"name,omitempty"`
	Schedule string            `json:"schedule,omitempty"`
	Tier     string            `json:"tier,omitempty"`
	Prompt   string            `json:"prompt,omitempty"`
	Command  string            `json:"command,omitempty"`
	Message  string            `json:"message,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	Output   string            `json:"output,omitempty"`
	Timeout  string            `json:"timeout,omitempty"`
	ID       string            `json:"id,omitempty"`
	Skills   []string          `json:"skills,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
	UserOnly bool              `json:"user_only,omitempty"`
}

type job struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Schedule  string     `json:"schedule"`
	Tier      string     `json:"tier"`
	Prompt    string     `json:"prompt"`
	Command   string     `json:"command"`
	Message   string     `json:"message"`
	Output    string     `json:"output"`
	Enabled   bool       `json:"enabled"`
	System    bool       `json:"system"`
	Managed   bool       `json:"managed"`
	CreatedAt time.Time  `json:"created_at"`
	LastRun   *time.Time `json:"last_run"`
	NextRun   *time.Time `json:"next_run"`
	LastError string     `json:"last_error"`
}

type socketResponse struct {
	Jobs  []job  `json:"jobs,omitempty"`
	Job   *job   `json:"job,omitempty"`
	Error string `json:"error,omitempty"`
}

func main() {
	dataDir := os.Getenv("HOME")
	if d := os.Getenv("ALF_DATA_DIR"); d != "" {
		dataDir = d
	}
	sockPath := filepath.Join(dataDir, "context", "scheduler.sock")

	// If no CLI args, try reading JSON from stdin (for agentic tool loop).
	if len(os.Args) < 2 {
		if input := readStdinJSON(); input != nil {
			handleJSONInput(sockPath, input)
			return
		}
		printUsage()
		os.Exit(1)
	}

	// Support both symlink-based dispatch and subcommand dispatch.
	subCmd := os.Args[1]
	os.Args = append(os.Args[:1], os.Args[2:]...)
	runSubcommand(subCmd, sockPath)
}

func runSubcommand(sub, sockPath string) {
	switch sub {
	case "create":
		doCreate(sockPath)
	case "list":
		doList(sockPath)
	case "delete":
		doDelete(sockPath)
	case "update":
		doUpdate(sockPath)
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n", sub)
		printUsage()
		os.Exit(1)
	}
}

func doCreate(sockPath string) {
	args := parseFlags(os.Args[1:])

	name := args["name"]
	schedule := args["schedule"]
	tier := args["tier"]
	prompt := args["prompt"]
	command := args["command"]
	message := args["message"]
	reason := args["reason"]
	output := args["output"]
	timeout := args["timeout"]
	skillsRaw := args["skills"]

	if name == "" || schedule == "" {
		fmt.Fprintf(os.Stderr, "Error: --name and --schedule are required\n\n")
		printUsage()
		os.Exit(1)
	}

	// Reminder mode: --message is mutually exclusive with --prompt, --command, --tier.
	if message != "" {
		if prompt != "" || command != "" || tier != "" {
			fmt.Fprintf(os.Stderr, "Error: --message is a direct push notification - cannot be combined with --prompt, --command, or --tier\n")
			os.Exit(1)
		}

		resp := socketCall(sockPath, socketRequest{
			Action:   "create",
			Name:     name,
			Schedule: schedule,
			Message:  message,
			Reason:   reason,
			Output:   output,
			Timeout:  timeout,
		})

		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
			os.Exit(1)
		}

		if resp.Job != nil {
			fmt.Printf("Created reminder %s (%s)\n", resp.Job.ID, resp.Job.Name)
			if resp.Job.NextRun != nil {
				fmt.Printf("Next run: %s\n", resp.Job.NextRun.Format("2006-01-02 15:04:05"))
			}
		}
		return
	}

	// Auto-detect direct tier when --command is provided.
	if tier == "" && command != "" {
		tier = "direct"
	}

	// Validate: direct tier uses --command, LLM tiers use --prompt.
	if tier == "direct" {
		if command == "" && prompt != "" {
			// Auto-convert: treat --prompt as --command for direct tier.
			command = prompt
			prompt = ""
		}
		if command == "" {
			fmt.Fprintf(os.Stderr, "Error: direct tier requires --command (bash command to execute)\n")
			os.Exit(1)
		}
		if prompt != "" {
			fmt.Fprintf(os.Stderr, "Error: direct tier uses --command, not --prompt\n")
			os.Exit(1)
		}
	} else {
		if prompt == "" {
			fmt.Fprintf(os.Stderr, "Error: LLM tiers require --prompt\n")
			os.Exit(1)
		}
		if command != "" {
			fmt.Fprintf(os.Stderr, "Error: --command is only for direct tier (deterministic bash jobs)\n")
			os.Exit(1)
		}
	}

	var skills []string
	if skillsRaw != "" {
		for _, s := range strings.Split(skillsRaw, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				skills = append(skills, s)
			}
		}
	}

	resp := socketCall(sockPath, socketRequest{
		Action:   "create",
		Name:     name,
		Schedule: schedule,
		Tier:     tier,
		Prompt:   prompt,
		Command:  command,
		Reason:   reason,
		Output:   output,
		Timeout:  timeout,
		Skills:   skills,
	})

	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	if resp.Job != nil {
		fmt.Printf("Created job %s (%s)\n", resp.Job.ID, resp.Job.Name)
		if resp.Job.NextRun != nil {
			fmt.Printf("Next run: %s\n", resp.Job.NextRun.Format("2006-01-02 15:04:05"))
		}
	}
}

func doList(sockPath string) {
	userOnly := false
	for _, a := range os.Args[1:] {
		if a == "--user" {
			userOnly = true
		}
	}

	resp := socketCall(sockPath, socketRequest{
		Action:   "list",
		UserOnly: userOnly,
	})

	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	if len(resp.Jobs) == 0 {
		fmt.Println("No scheduled jobs.")
		return
	}

	fmt.Printf("%-10s %-8s %-25s %-20s %-10s %-10s %-8s %s\n", "ID", "Type", "Name", "Schedule", "Tier", "Output", "Enabled", "Next Run")
	fmt.Println(strings.Repeat("-", 112))
	for _, j := range resp.Jobs {
		typ := "user"
		if j.System {
			typ = "system"
		} else if j.Managed {
			typ = "managed"
		}
		enabled := "yes"
		if !j.Enabled {
			enabled = "no"
		}
		nextRun := "-"
		if j.NextRun != nil {
			nextRun = j.NextRun.Format("2006-01-02 15:04")
		}
		sched := j.Schedule
		if len(sched) > 20 {
			sched = sched[:17] + "..."
		}
		name := j.Name
		if len(name) > 25 {
			name = name[:22] + "..."
		}
		tier := j.Tier
		if j.Message != "" {
			tier = "reminder"
		} else if tier == "" {
			tier = "-"
		}
		fmt.Printf("%-10s %-8s %-25s %-20s %-10s %-10s %-8s %s\n", j.ID, typ, name, sched, tier, j.Output, enabled, nextRun)

		if j.LastError != "" {
			fmt.Printf("  -> last error: %s\n", j.LastError)
		}
	}
}

func doDelete(sockPath string) {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: schedule delete <id>\n")
		os.Exit(1)
	}

	resp := socketCall(sockPath, socketRequest{
		Action: "delete",
		ID:     os.Args[1],
	})

	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	fmt.Printf("Deleted job %s\n", os.Args[1])
}

func doUpdate(sockPath string) {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: schedule update <id> [--enabled true|false] [--schedule ...] [--prompt ...] [--name ...] [--output ...]\n")
		os.Exit(1)
	}

	id := os.Args[1]
	fields := parseFlags(os.Args[2:])

	if len(fields) == 0 {
		fmt.Fprintf(os.Stderr, "No fields to update.\n")
		os.Exit(1)
	}

	resp := socketCall(sockPath, socketRequest{
		Action: "update",
		ID:     id,
		Fields: fields,
	})

	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	fmt.Printf("Updated job %s\n", id)
}

// parseFlags extracts --key value pairs from args.
func parseFlags(args []string) map[string]string {
	m := make(map[string]string)
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") && i+1 < len(args) {
			key := strings.TrimPrefix(args[i], "--")
			m[key] = args[i+1]
			i++
		}
	}
	return m
}

func socketCall(sockPath string, req socketRequest) socketResponse {
	conn, err := net.DialTimeout("unix", sockPath, 10*time.Second)
	if err != nil {
		return socketResponse{Error: fmt.Sprintf("connect to %s: %v (is the scheduler running?)", sockPath, err)}
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(30 * time.Second))

	enc := json.NewEncoder(conn)
	if err := enc.Encode(req); err != nil {
		return socketResponse{Error: fmt.Sprintf("send request: %v", err)}
	}

	var resp socketResponse
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&resp); err != nil {
		return socketResponse{Error: fmt.Sprintf("read response: %v", err)}
	}

	return resp
}

// readStdinJSON reads JSON from stdin if data is available (pipe, not terminal).
func readStdinJSON() map[string]any {
	info, err := os.Stdin.Stat()
	if err != nil {
		return nil
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	var input map[string]any
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return nil
	}
	return input
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// handleJSONInput dispatches a JSON stdin request to the appropriate action.
func handleJSONInput(sockPath string, input map[string]any) {
	action := str(input, "action")
	if action == "" {
		action = "list" // safe default
	}

	switch action {
	case "create":
		name := str(input, "name")
		schedule := str(input, "schedule")
		if name == "" || schedule == "" {
			fmt.Fprintln(os.Stderr, "Error: 'name' and 'schedule' are required")
			os.Exit(1)
		}
		req := socketRequest{
			Action:   "create",
			Name:     name,
			Schedule: schedule,
			Tier:     str(input, "tier"),
			Prompt:   str(input, "prompt"),
			Command:  str(input, "command"),
			Message:  str(input, "message"),
			Reason:   str(input, "reason"),
			Output:   str(input, "output"),
			Timeout:  str(input, "timeout"),
		}
		if s := str(input, "skills"); s != "" {
			for _, sk := range strings.Split(s, ",") {
				sk = strings.TrimSpace(sk)
				if sk != "" {
					req.Skills = append(req.Skills, sk)
				}
			}
		}
		resp := socketCall(sockPath, req)
		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
			os.Exit(1)
		}
		if resp.Job != nil {
			fmt.Printf("Created job %s (%s)\n", resp.Job.ID, resp.Job.Name)
			if resp.Job.NextRun != nil {
				fmt.Printf("Next run: %s\n", resp.Job.NextRun.Format("2006-01-02 15:04:05"))
			}
		}

	case "list":
		userOnly := false
		if v, ok := input["user_only"].(bool); ok {
			userOnly = v
		}
		resp := socketCall(sockPath, socketRequest{Action: "list", UserOnly: userOnly})
		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
			os.Exit(1)
		}
		if len(resp.Jobs) == 0 {
			fmt.Println("No scheduled jobs.")
			return
		}
		for _, j := range resp.Jobs {
			tier := j.Tier
			if j.Message != "" {
				tier = "reminder"
			}
			nextRun := "-"
			if j.NextRun != nil {
				nextRun = j.NextRun.Format("2006-01-02 15:04")
			}
			enabled := "yes"
			if !j.Enabled {
				enabled = "no"
			}
			fmt.Printf("[%s] %s | schedule=%s tier=%s enabled=%s next=%s\n", j.ID, j.Name, j.Schedule, tier, enabled, nextRun)
		}

	case "delete":
		id := str(input, "id")
		if id == "" {
			fmt.Fprintln(os.Stderr, "Error: 'id' is required")
			os.Exit(1)
		}
		resp := socketCall(sockPath, socketRequest{Action: "delete", ID: id})
		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
			os.Exit(1)
		}
		fmt.Printf("Deleted job %s\n", id)

	case "update":
		id := str(input, "id")
		if id == "" {
			fmt.Fprintln(os.Stderr, "Error: 'id' is required")
			os.Exit(1)
		}
		fields := make(map[string]string)
		for _, k := range []string{"name", "schedule", "prompt", "command", "message", "reason", "output", "timeout", "enabled"} {
			if v := str(input, k); v != "" {
				fields[k] = v
			}
		}
		resp := socketCall(sockPath, socketRequest{Action: "update", ID: id, Fields: fields})
		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
			os.Exit(1)
		}
		fmt.Printf("Updated job %s\n", id)

	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", action)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: schedule <command> [options]

Commands:
  create  Create a scheduled job
  list    List all jobs [--user for user jobs only]
  delete  Delete a job by ID
  update  Update a job by ID

Create options:
  --name <name>           Job name (required)
  --schedule <expr>       Cron expression or RFC3339 one-shot (required)
  --tier <tier>           LLM tier (e.g. haiku, sonnet, opus) or "direct" for bash
  --prompt <text>         Prompt for LLM tiers (required for LLM jobs)
  --command <cmd>         Bash command for direct tier (required for direct jobs)
  --message <text>        Direct push notification (no LLM, no command - just sends the message)
  --reason <text>         Why this job exists (context injected into LLM at execution time)
  --output <dest>         telegram | file | both | silent (default: telegram)
  --timeout <duration>    Execution timeout (e.g. 5m, 10m, 1h). Defaults: direct=2m, LLM=5m, agent=30m
  --skills <s1,s2>        Comma-separated skill names (LLM jobs only)

  Note: --message, --prompt, and --command are mutually exclusive.

Examples:
  # Reminder: daily standup notification (no LLM cost)
  schedule create --name "standup" --schedule "0 55 8 * * 1-5" \
    --message "Daily standup in 5 minutes"

  # Reminder: one-shot reminder at a specific time
  schedule create --name "call john" --schedule "2026-03-10T14:00:00Z" \
    --message "Call John about the contract renewal"

  # Deterministic: check disk usage every 6 hours
  schedule create --name "disk check" --schedule "0 0 */6 * * *" \
    --command "df -h" --output telegram

  # Deterministic: health check every 5 min, log to file
  schedule create --name "healthcheck" --schedule "0 */5 * * * *" \
    --command "curl -sf http://localhost:8080/health && echo OK || echo FAIL" \
    --output file

  # LLM: daily briefing with sonnet
  schedule create --name "morning brief" --schedule "0 0 9 * * 1-5" \
    --tier sonnet --prompt "Summarize today's priorities" --output telegram

  # LLM: one-shot task at a specific time
  schedule create --name "deploy check" --schedule "2026-03-10T15:00:00Z" \
    --tier haiku --prompt "Check if v2.1 deployed correctly" --output telegram

Update options:
  schedule update <id> [--enabled true|false] [--schedule ...] [--prompt ...] [--command ...] [--message ...] [--reason ...] [--name ...] [--output ...] [--timeout ...]

Other:
  schedule list [--user]
  schedule delete <id>
`)
}
