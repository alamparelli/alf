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
	Output   string            `json:"output,omitempty"`
	ID       string            `json:"id,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
	UserOnly bool              `json:"user_only,omitempty"`
}

type job struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Schedule  string     `json:"schedule"`
	Tier      string     `json:"tier"`
	Prompt    string     `json:"prompt"`
	Output    string     `json:"output"`
	Enabled   bool       `json:"enabled"`
	System    bool       `json:"system"`
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
	output := args["output"]

	if name == "" || schedule == "" || prompt == "" {
		fmt.Fprintf(os.Stderr, "Usage: schedule create --name <name> --schedule <cron|RFC3339> --prompt <text> [--tier <tier>] [--output telegram|file|both|silent]\n")
		os.Exit(1)
	}

	resp := socketCall(sockPath, socketRequest{
		Action:   "create",
		Name:     name,
		Schedule: schedule,
		Tier:     tier,
		Prompt:   prompt,
		Output:   output,
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

	fmt.Printf("%-10s %-6s %-25s %-20s %-10s %-10s %-8s %s\n", "ID", "Type", "Name", "Schedule", "Tier", "Output", "Enabled", "Next Run")
	fmt.Println(strings.Repeat("-", 110))
	for _, j := range resp.Jobs {
		typ := "user"
		if j.System {
			typ = "system"
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
		fmt.Printf("%-10s %-6s %-25s %-20s %-10s %-10s %-8s %s\n", j.ID, typ, name, sched, tier, j.Output, enabled, nextRun)

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
  create  --name <name> --schedule <cron|RFC3339> --prompt <text> [--tier <tier>] [--output telegram|file|both|silent]
  list    [--user]
  delete  <id>
  update  <id> [--enabled true|false] [--schedule ...] [--prompt ...] [--name ...] [--output ...]

Examples:
  schedule create --name "morning brief" --schedule "0 0 9 * * 1-5" --tier sonnet_r --prompt "Summarize my tasks" --output telegram
  schedule create --name "reminder" --schedule "2026-03-04T15:00:00Z" --tier direct --prompt "Check deployment" --output telegram
  schedule list
  schedule delete a1b2c3d4
  schedule update a1b2c3d4 --enabled false
`)
}
