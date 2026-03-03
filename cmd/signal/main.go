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

// Protocol types mirror internal/signal.
type request struct {
	Action string `json:"action"`
	Emoji  string `json:"emoji,omitempty"`
	Text   string `json:"text,omitempty"`
}

type response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func main() {
	cmd := filepath.Base(os.Args[0])

	sockPath := os.Getenv("ALF_SIGNAL_SOCK")
	if sockPath == "" {
		fmt.Fprintln(os.Stderr, "ALF_SIGNAL_SOCK not set")
		os.Exit(1)
	}

	switch cmd {
	case "react":
		doReact(sockPath)
	case "status":
		doStatus(sockPath)
	default:
		// Fallback: check first argument.
		if len(os.Args) >= 2 {
			switch os.Args[1] {
			case "react":
				os.Args = append(os.Args[:1], os.Args[2:]...)
				doReact(sockPath)
				return
			case "status":
				os.Args = append(os.Args[:1], os.Args[2:]...)
				doStatus(sockPath)
				return
			}
		}
		fmt.Fprintf(os.Stderr, "Usage: react <emoji>\n       status <message>\n")
		os.Exit(1)
	}
}

func doReact(sockPath string) {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: react <emoji>")
		os.Exit(1)
	}
	resp := socketCall(sockPath, request{Action: "react", Emoji: os.Args[1]})
	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}
	fmt.Println("OK")
}

func doStatus(sockPath string) {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: status <message>")
		os.Exit(1)
	}
	text := strings.Join(os.Args[1:], " ")
	resp := socketCall(sockPath, request{Action: "status", Text: text})
	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
		os.Exit(1)
	}
	fmt.Println("OK")
}

func socketCall(sockPath string, req request) response {
	conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		return response{Error: fmt.Sprintf("connect: %v", err)}
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(10 * time.Second))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return response{Error: fmt.Sprintf("send: %v", err)}
	}

	var resp response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return response{Error: fmt.Sprintf("read: %v", err)}
	}
	return resp
}
