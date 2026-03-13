package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// socketRequest mirrors memstore.socketRequest.
type socketRequest struct {
	Action string `json:"action"`
	Query  string `json:"query,omitempty"`
	Text   string `json:"text,omitempty"`
	Type   string `json:"type,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Days   int    `json:"days,omitempty"`
	ID     int64  `json:"id,omitempty"`
}

type memory struct {
	ID        int64   `json:"ID"`
	Text      string  `json:"Text"`
	Type      string  `json:"Type"`
	Source    string  `json:"Source"`
	CreatedAt string  `json:"CreatedAt"`
	Distance  float64 `json:"Distance"`
}

type socketResponse struct {
	Results []memory `json:"results,omitempty"`
	ID      int64    `json:"id,omitempty"`
	Count   int      `json:"count,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func main() {
	cmd := filepath.Base(os.Args[0])

	dataDir := os.Getenv("HOME")
	if d := os.Getenv("ALF_DATA_DIR"); d != "" {
		dataDir = d
	}
	sockPath := filepath.Join(dataDir, "context", "memstore.sock")

	// If no CLI args, try reading JSON from stdin (for agentic tool loop).
	if len(os.Args) < 2 {
		if input := readStdinJSON(); input != nil {
			handleJSONInput(cmd, sockPath, input)
			return
		}
	}

	switch cmd {
	case "recall":
		doSearch(sockPath)
	case "remember":
		doStore(sockPath)
	case "forget":
		doDelete(sockPath)
	default:
		// Fallback: check first argument.
		if len(os.Args) >= 2 {
			switch os.Args[1] {
			case "search":
				os.Args = append(os.Args[:1], os.Args[2:]...)
				doSearch(sockPath)
				return
			case "store":
				os.Args = append(os.Args[:1], os.Args[2:]...)
				doStore(sockPath)
				return
			case "delete":
				os.Args = append(os.Args[:1], os.Args[2:]...)
				doDelete(sockPath)
				return
			}
		}
		fmt.Fprintf(os.Stderr, "Usage: recall <query> [--limit N]\n       remember <text> [--type fact|preference|decision]\n       forget <id> [id2 id3 ...]\n")
		os.Exit(1)
	}
}

// parseIDList extracts a list of int64 IDs from JSON input.
// Supports: {"ids": [1,2,3]}, {"ids": "1 2 3"}, {"id": 5} (legacy single).
func parseIDList(input map[string]any) []int64 {
	var ids []int64
	if arr, ok := input["ids"]; ok {
		switch v := arr.(type) {
		case []any:
			for _, item := range v {
				switch n := item.(type) {
				case float64:
					ids = append(ids, int64(n))
				case string:
					if id, err := strconv.ParseInt(n, 10, 64); err == nil {
						ids = append(ids, id)
					}
				}
			}
		case string:
			for _, part := range strings.Fields(v) {
				if id, err := strconv.ParseInt(part, 10, 64); err == nil {
					ids = append(ids, id)
				}
			}
		case float64:
			ids = append(ids, int64(v))
		}
	}
	// Legacy: single "id" field
	if len(ids) == 0 {
		if v, ok := input["id"]; ok {
			switch n := v.(type) {
			case float64:
				ids = append(ids, int64(n))
			case string:
				if id, err := strconv.ParseInt(n, 10, 64); err == nil {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

// readStdinJSON reads JSON from stdin if data is available (non-blocking check).
func readStdinJSON() map[string]any {
	info, err := os.Stdin.Stat()
	if err != nil {
		return nil
	}
	// Check if stdin has data (pipe or redirect, not a terminal).
	if info.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	var input map[string]any
	dec := json.NewDecoder(os.Stdin)
	if err := dec.Decode(&input); err != nil {
		return nil
	}
	return input
}

// handleJSONInput dispatches a JSON-parsed stdin request to the appropriate action.
func handleJSONInput(cmd, sockPath string, input map[string]any) {
	switch cmd {
	case "recall":
		query, _ := input["query"].(string)
		if query == "" {
			// Try "args" fallback (generic schema).
			query, _ = input["args"].(string)
		}
		if query == "" {
			fmt.Fprintln(os.Stderr, "Error: missing 'query' field")
			os.Exit(1)
		}
		limit := 5
		if l, ok := input["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		resp := socketCall(sockPath, socketRequest{
			Action: "search",
			Query:  query,
			Limit:  limit,
		})
		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
			os.Exit(1)
		}
		if len(resp.Results) == 0 {
			fmt.Println("Nothing found.")
			return
		}
		fmt.Printf("Found %d results:\n\n", len(resp.Results))
		for _, m := range resp.Results {
			date := parseDate(m.CreatedAt)
			fmt.Printf("[#%d] (%s, %s) %s\n", m.ID, m.Type, date, m.Text)
		}

	case "remember":
		text, _ := input["text"].(string)
		if text == "" {
			text, _ = input["args"].(string)
		}
		if text == "" {
			fmt.Fprintln(os.Stderr, "Error: missing 'text' field")
			os.Exit(1)
		}
		memType, _ := input["type"].(string)
		if memType == "" {
			memType = "fact"
		}
		resp := socketCall(sockPath, socketRequest{
			Action: "store",
			Text:   text,
			Type:   memType,
		})
		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
			os.Exit(1)
		}
		fmt.Printf("Remembered #%d\n", resp.ID)

	case "forget":
		ids := parseIDList(input)
		if len(ids) == 0 {
			fmt.Fprintln(os.Stderr, "Error: missing or invalid 'ids' field")
			os.Exit(1)
		}
		for _, id := range ids {
			resp := socketCall(sockPath, socketRequest{
				Action: "delete",
				ID:     id,
			})
			if resp.Error != "" {
				fmt.Fprintf(os.Stderr, "Error deleting #%d: %s\n", id, resp.Error)
			} else {
				fmt.Printf("Forgotten #%d\n", id)
			}
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}

func doSearch(sockPath string) {
	if len(os.Args) >= 2 && os.Args[1] == "--status" {
		doStatus(sockPath)
		return
	}

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: recall <query> [--limit N]\n       recall --status\n")
		os.Exit(1)
	}

	query := os.Args[1]
	limit := 5

	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--limit" && i+1 < len(os.Args) {
			limit, _ = strconv.Atoi(os.Args[i+1])
			i++
		}
	}

	resp := socketCall(sockPath, socketRequest{
		Action: "search",
		Query:  query,
		Limit:  limit,
	})

	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	if len(resp.Results) == 0 {
		fmt.Println("Nothing found.")
		return
	}

	fmt.Printf("Found %d results:\n\n", len(resp.Results))
	for _, m := range resp.Results {
		date := parseDate(m.CreatedAt)
		distInfo := ""
		if m.Distance > 0 {
			distInfo = fmt.Sprintf(", dist=%.3f", m.Distance)
		}
		fmt.Printf("[#%d] (%s, %s%s) %s\n", m.ID, m.Type, date, distInfo, m.Text)
	}
}

func doStore(sockPath string) {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: remember <text> [--type fact|preference|decision]\n")
		os.Exit(1)
	}

	text := os.Args[1]
	memType := "fact"

	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--type" && i+1 < len(os.Args) {
			memType = os.Args[i+1]
			i++
		}
	}

	resp := socketCall(sockPath, socketRequest{
		Action: "store",
		Text:   text,
		Type:   memType,
	})

	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	fmt.Printf("Remembered #%d\n", resp.ID)
}

func doDelete(sockPath string) {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: forget <id> [id2 id3 ...]\n")
		os.Exit(1)
	}

	for _, arg := range os.Args[1:] {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid id %q\n", arg)
			continue
		}
		resp := socketCall(sockPath, socketRequest{
			Action: "delete",
			ID:     id,
		})
		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "Error deleting #%d: %s\n", id, resp.Error)
		} else {
			fmt.Printf("Forgotten #%d\n", id)
		}
	}
}

const extractionInterval = 3 * time.Hour

func doStatus(sockPath string) {
	// Read state file (sibling of socket).
	stateFile := filepath.Join(filepath.Dir(sockPath), "memory_extractor_state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		fmt.Println("Extraction has not run yet.")
		return
	}

	var state struct {
		LastRun time.Time `json:"last_run"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		fmt.Println("Extraction has not run yet.")
		return
	}

	nextRun := state.LastRun.Add(extractionInterval)
	now := time.Now()

	fmt.Printf("Last extraction: %s\n", state.LastRun.Format("2006-01-02 15:04"))
	if now.Before(nextRun) {
		fmt.Printf("Next extraction: %s (in %s)\n", nextRun.Format("15:04"), nextRun.Sub(now).Round(time.Minute))
	} else {
		fmt.Printf("Next extraction: overdue (expected %s)\n", nextRun.Format("15:04"))
	}

	// Also show total count via socket.
	resp := socketCall(sockPath, socketRequest{Action: "recent", Limit: 0, Days: 9999})
	if resp.Error == "" {
		fmt.Printf("Total memories: %d\n", resp.Count)
	}
}

func socketCall(sockPath string, req socketRequest) socketResponse {
	conn, err := net.DialTimeout("unix", sockPath, 10*time.Second)
	if err != nil {
		return socketResponse{Error: fmt.Sprintf("connect to %s: %v", sockPath, err)}
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

func parseDate(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Try alternate format from JSON marshaling.
		t, err = time.Parse("2006-01-02T15:04:05Z", s)
		if err != nil {
			return strings.Split(s, "T")[0]
		}
	}
	return t.Format("2006-01-02")
}
