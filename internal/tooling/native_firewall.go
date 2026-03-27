package tooling

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FirewallService provides read-only access to firewall data.
type FirewallService interface {
	// RecentEntries returns the last N request log entries.
	RecentEntries(limit int) []FirewallEntry
	// Hosts returns cumulative host statistics.
	Hosts() []FirewallHostStat
}

// FirewallEntry is a simplified view of a firewall request log entry.
type FirewallEntry struct {
	Time    time.Time `json:"time"`
	Method  string    `json:"method"`
	Host    string    `json:"host"`
	Path    string    `json:"path"`
	Blocked bool      `json:"blocked"`
	Source  string    `json:"source,omitempty"`
}

// FirewallHostStat is a simplified view of cumulative host stats.
type FirewallHostStat struct {
	Host    string `json:"host"`
	Count   int    `json:"count"`
	Allowed int    `json:"allowed"`
	Blocked int    `json:"blocked"`
	Vault   bool   `json:"vault,omitempty"`
}

// FirewallNativeTool gives the LLM read-only visibility into firewall activity.
type FirewallNativeTool struct {
	Service FirewallService
}

func (FirewallNativeTool) ToolName() string { return "firewall" }

func (FirewallNativeTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "firewall",
		Description: "Read-only view of network firewall activity. List recent connections, view host statistics, or search for a specific host.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"recent", "hosts", "search"},
					"description": "Action: 'recent' for latest entries, 'hosts' for cumulative stats, 'search' to filter by host pattern.",
				},
				"limit": map[string]any{
					"type":        []string{"integer", "null"},
					"description": "Number of recent entries to return (default 50, max 200). Only for 'recent'.",
				},
				"query": map[string]any{
					"type":        []string{"string", "null"},
					"description": "Host pattern to search for (substring match). Required for 'search'.",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (t FirewallNativeTool) Run(_ context.Context, argsJSON string) (string, error) {
	var args struct {
		Action string `json:"action"`
		Limit  int    `json:"limit"`
		Query  string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch args.Action {
	case "recent":
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		if limit > 200 {
			limit = 200
		}
		entries := t.Service.RecentEntries(limit)
		if len(entries) == 0 {
			return "No recent firewall entries.", nil
		}
		return formatEntries(entries), nil

	case "hosts":
		hosts := t.Service.Hosts()
		if len(hosts) == 0 {
			return "No hosts recorded.", nil
		}
		return formatHosts(hosts), nil

	case "search":
		if args.Query == "" {
			return "", fmt.Errorf("query is required for search action")
		}
		query := strings.ToLower(args.Query)
		entries := t.Service.RecentEntries(500)
		var matches []FirewallEntry
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.Host), query) {
				matches = append(matches, e)
			}
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No entries matching %q.", args.Query), nil
		}
		if len(matches) > 100 {
			matches = matches[len(matches)-100:]
		}
		return fmt.Sprintf("Found %d entries matching %q:\n\n%s", len(matches), args.Query, formatEntries(matches)), nil

	default:
		return "", fmt.Errorf("unknown action: %s (valid: recent, hosts, search)", args.Action)
	}
}

func formatEntries(entries []FirewallEntry) string {
	var sb strings.Builder
	for _, e := range entries {
		ts := e.Time.Format("15:04:05")
		status := "OK"
		if e.Blocked {
			status = "BLOCKED"
		}
		src := ""
		if e.Source != "" {
			src = " [" + e.Source + "]"
		}
		fmt.Fprintf(&sb, "%s  %-7s  %-30s  %-20s  %s%s\n", ts, e.Method, e.Host, e.Path, status, src)
	}
	return sb.String()
}

func formatHosts(hosts []FirewallHostStat) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-35s  %8s  %8s  %8s  %s\n", "HOST", "TOTAL", "ALLOWED", "BLOCKED", "SOURCE")
	fmt.Fprintln(&sb, strings.Repeat("-", 80))
	for _, h := range hosts {
		src := ""
		if h.Vault {
			src = "vault"
		}
		fmt.Fprintf(&sb, "%-35s  %8d  %8d  %8d  %s\n", h.Host, h.Count, h.Allowed, h.Blocked, src)
	}
	return sb.String()
}
