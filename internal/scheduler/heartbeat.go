package scheduler

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	heartbeatJobID   = "heartbeat"
	heartbeatJobName = "Heartbeat"
	heartbeatFile    = "heartbeat.md"
)

// RegisterHeartbeat seeds a managed heartbeat job that reads context/heartbeat.md.
// The heartbeat is skipped at execution time if the file body is empty.
// The tier and schedule are read from the frontmatter.
func (e *Engine) RegisterHeartbeat(contextDir string) {
	hbPath := filepath.Join(contextDir, heartbeatFile)

	// Parse frontmatter to get tier and schedule.
	tier, schedule := parseHeartbeatMeta(hbPath)
	if schedule == "" {
		schedule = "0 0 */6 * * *" // default: every 6 hours
	}

	if _, err := e.EnsureManaged(
		heartbeatJobID,
		heartbeatJobName,
		schedule,
		tier,
		"__heartbeat__", // sentinel — executeJob detects this and runs heartbeat logic
		"telegram",
		nil,
	); err != nil {
		log.Printf("warning: failed to seed heartbeat job: %v", err)
	}
}

// executeHeartbeat reads context/heartbeat.md, skips if empty body,
// otherwise invokes the LLM with the body as prompt.
func (e *Engine) executeHeartbeat(j *Job) (string, *execResult, error) {
	contextDir := e.cfg.ContextDir
	if contextDir == "" {
		contextDir = filepath.Join(e.cfg.DataDir, "context")
	}
	hbPath := filepath.Join(contextDir, heartbeatFile)

	data, err := os.ReadFile(hbPath)
	if err != nil {
		log.Printf("scheduler: [heartbeat] no heartbeat.md found, skipping")
		return "", nil, nil
	}

	// Parse frontmatter and extract body.
	_, _, body := parseHeartbeatFull(string(data))
	body = strings.TrimSpace(body)

	if body == "" {
		log.Printf("scheduler: [heartbeat] heartbeat.md body is empty, skipping")
		return "", nil, nil
	}

	// Re-read tier from frontmatter (user may have changed it via CC).
	tier, _ := parseHeartbeatMeta(hbPath)
	if tier != "" && tier != j.Tier {
		j.Tier = tier
	}

	// Create a temporary job with the body as prompt.
	hbJob := *j
	hbJob.Prompt = body
	return e.invokeLLMWithMeta(&hbJob)
}

// parseHeartbeatMeta reads tier and schedule from heartbeat.md frontmatter.
func parseHeartbeatMeta(path string) (tier, schedule string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}

	tier, schedule, _ = parseHeartbeatFull(string(data))
	return
}

// parseHeartbeatFull parses frontmatter and returns tier, schedule, and body.
func parseHeartbeatFull(content string) (tier, schedule, body string) {
	content = strings.TrimLeft(content, "\xef\xbb\xbf")
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---") {
		return "", "", trimmed
	}

	lines := strings.Split(content, "\n")
	inFront := false
	frontEnd := -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "---" {
			if !inFront {
				inFront = true
				continue
			}
			frontEnd = i
			break
		}
		if inFront {
			if idx := strings.Index(line, ":"); idx > 0 {
				key := strings.TrimSpace(line[:idx])
				val := strings.TrimSpace(line[idx+1:])
				if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
					val = val[1 : len(val)-1]
				}
				switch key {
				case "tier":
					tier = val
				case "schedule":
					schedule = val
				}
			}
		}
	}

	if frontEnd > 0 && frontEnd+1 < len(lines) {
		body = strings.TrimSpace(strings.Join(lines[frontEnd+1:], "\n"))
	} else {
		body = trimmed
	}
	return
}
