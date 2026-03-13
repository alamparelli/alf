package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

// socketRequest is the JSON protocol for schedule tool → daemon communication.
type socketRequest struct {
	Action   string            `json:"action"` // "create", "list", "delete", "update"
	Name     string            `json:"name,omitempty"`
	Schedule string            `json:"schedule,omitempty"`
	Tier     string            `json:"tier,omitempty"`
	Prompt   string            `json:"prompt,omitempty"`
	Command  string            `json:"command,omitempty"`
	Message  string            `json:"message,omitempty"`
	Output   string            `json:"output,omitempty"`
	Timeout  string            `json:"timeout,omitempty"` // Go duration string (e.g. "10m", "1h")
	ID       string            `json:"id,omitempty"`
	Skills   []string          `json:"skills,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
	UserOnly bool              `json:"user_only,omitempty"`
}

// socketResponse is sent back to the tool.
type socketResponse struct {
	Jobs  []*Job `json:"jobs,omitempty"`
	Job   *Job   `json:"job,omitempty"`
	Error string `json:"error,omitempty"`
}

// Server handles Unix socket connections for the schedule tool.
type Server struct {
	engine   *Engine
	listener net.Listener
	sockPath string
}

// NewServer creates a socket server for the scheduler.
func NewServer(engine *Engine, sockPath string) *Server {
	return &Server{
		engine:   engine,
		sockPath: sockPath,
	}
}

// Serve starts listening. Blocks until closed.
func (s *Server) Serve() error {
	os.Remove(s.sockPath)

	ln, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.sockPath, err)
	}
	s.listener = ln

	// Make socket accessible to claude subprocess (uid 1001, gid 1000 = node group).
	os.Chmod(s.sockPath, 0660)
	os.Chown(s.sockPath, 1001, 1000)

	log.Printf("scheduler: socket server listening on %s", s.sockPath)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "use of closed") {
				return nil
			}
			log.Printf("scheduler: accept error: %v", err)
			continue
		}
		go s.handleConn(conn)
	}
}

// Close stops the listener.
func (s *Server) Close() {
	if s.listener != nil {
		s.listener.Close()
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var req socketRequest
	if err := dec.Decode(&req); err != nil {
		enc.Encode(socketResponse{Error: fmt.Sprintf("decode: %v", err)})
		return
	}

	var resp socketResponse

	switch req.Action {
	case "create":
		if req.Name == "" || req.Schedule == "" {
			resp.Error = "name and schedule are required"
			break
		}

		// Reminder mode: --message is mutually exclusive with --prompt, --command, --tier.
		if req.Message != "" {
			if req.Prompt != "" || req.Command != "" || req.Tier != "" {
				resp.Error = "--message is a direct push notification - cannot be combined with --prompt, --command, or --tier"
				break
			}
			var timeout time.Duration
			if req.Timeout != "" {
				var terr error
				timeout, terr = time.ParseDuration(req.Timeout)
				if terr != nil {
					resp.Error = fmt.Sprintf("invalid timeout %q: %v", req.Timeout, terr)
					break
				}
			}
			job, err := s.engine.CreateReminder(req.Name, req.Schedule, req.Message, req.Output, timeout)
			if err != nil {
				resp.Error = err.Error()
			} else {
				resp.Job = job
			}
			break
		}

		tier := req.Tier
		// Auto-detect direct tier when command is provided without explicit tier.
		if tier == "" && req.Command != "" {
			tier = "direct"
		}
		// Validate direct tier requires command, LLM tiers require prompt.
		if tier == "direct" {
			if req.Command == "" && req.Prompt != "" {
				// Auto-convert: treat prompt as command for direct tier.
				req.Command = req.Prompt
				req.Prompt = ""
			}
			if req.Command == "" {
				resp.Error = "direct tier requires --command (bash command to execute)"
				break
			}
			if req.Prompt != "" {
				resp.Error = "direct tier uses --command, not --prompt"
				break
			}
		} else {
			if req.Prompt == "" {
				resp.Error = "LLM tiers require --prompt"
				break
			}
			if req.Command != "" {
				resp.Error = "--command is only for direct tier (deterministic bash jobs)"
				break
			}
			if tier == "" {
				resp.Error = "--tier is required for LLM jobs"
				break
			}
		}
		var timeout time.Duration
		if req.Timeout != "" {
			var terr error
			timeout, terr = time.ParseDuration(req.Timeout)
			if terr != nil {
				resp.Error = fmt.Sprintf("invalid timeout %q: %v", req.Timeout, terr)
				break
			}
		}
		job, err := s.engine.Create(req.Name, req.Schedule, tier, req.Prompt, req.Command, req.Output, timeout, req.Skills)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Job = job
		}

	case "list":
		resp.Jobs = s.engine.List(req.UserOnly)

	case "delete":
		if req.ID == "" {
			resp.Error = "id required"
			break
		}
		if err := s.engine.Delete(req.ID); err != nil {
			resp.Error = err.Error()
		}

	case "update":
		if req.ID == "" {
			resp.Error = "id required"
			break
		}
		if len(req.Fields) == 0 {
			resp.Error = "no fields to update"
			break
		}
		job, err := s.engine.Update(req.ID, req.Fields)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Job = job
		}

	default:
		resp.Error = fmt.Sprintf("unknown action: %s", req.Action)
	}

	enc.Encode(resp)
}
