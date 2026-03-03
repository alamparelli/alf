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
	sockPath := filepath.Join(dataDir, "memstore.sock")

	switch cmd {
	case "memory-search":
		doSearch(sockPath)
	case "memory-store":
		doStore(sockPath)
	case "memory-delete":
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
		fmt.Fprintf(os.Stderr, "Usage: memory-search <query> [--limit N]\n       memory-store <text> [--type fact|preference|decision]\n       memory-delete <id>\n")
		os.Exit(1)
	}
}

func doSearch(sockPath string) {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: memory-search <query> [--limit N]\n")
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
		fmt.Println("No memories found.")
		return
	}

	fmt.Printf("Found %d memories:\n\n", len(resp.Results))
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
		fmt.Fprintf(os.Stderr, "Usage: memory-store <text> [--type fact|preference|decision]\n")
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

	fmt.Printf("Stored memory #%d\n", resp.ID)
}

func doDelete(sockPath string) {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: memory-delete <id>\n")
		os.Exit(1)
	}

	id, err := strconv.ParseInt(os.Args[1], 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid id %q\n", os.Args[1])
		os.Exit(1)
	}

	resp := socketCall(sockPath, socketRequest{
		Action: "delete",
		ID:     id,
	})

	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}

	fmt.Printf("Deleted memory #%d\n", id)
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
