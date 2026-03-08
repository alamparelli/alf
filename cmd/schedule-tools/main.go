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
	Output   string            `json:"output,omitempty"`
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
	cmd := filepath.Base(os.Args[0])

	dataDir := os.Getenv("HOME")
	if d := os.Getenv("ALF_DATA_DIR"); d != "" {
		dataDir = d
	}
	sockPath := filepath.Join(dataDir, "context", "scheduler.sock")

	// Support both symlink-based dispatch and subcommand dispatch.
	switch cmd {
	case "schedule":
		if len(os.Args) < 2 {
			printUsage()
			os.Exit(1)
		}
		subCmd := os.Args[1]
		os.Args = append(os.Args[:1], os.Args[2:]...)
		runSubcommand(subCmd, sockPath)
	default:
		// Direct binary name (schedule-tools) with subcommand.
		if len(os.Args) < 2 {
			printUsage()
			os.Exit(1)
		}
		subCmd := os.Args[1]
		os.Args = append(os.Args[:1], os.Args[2:]...)
		runSubcommand(subCmd, sockPath)
	}
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
	output := args["output"]
	skillsRaw := args["skills"]

	if name == "" || schedule == "" {
		fmt.Fprintf(os.Stderr, "Error: --name and --schedule are required\n\n")
		printUsage()
		os.Exit(1)
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
		Output:   output,
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
		if tier == "" {
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
  --output <dest>         telegram | file | both | silent (default: telegram)
  --skills <s1,s2>        Comma-separated skill names (LLM jobs only)

Examples:
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

  # LLM: one-shot reminder
  schedule create --name "deploy check" --schedule "2026-03-10T15:00:00Z" \
    --tier haiku --prompt "Check if v2.1 deployed correctly" --output telegram

Update options:
  schedule update <id> [--enabled true|false] [--schedule ...] [--prompt ...] [--command ...] [--name ...] [--output ...]

Other:
  schedule list [--user]
  schedule delete <id>
`)
}
