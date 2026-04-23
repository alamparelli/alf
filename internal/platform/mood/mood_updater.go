package mood

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UpdateLiveFeedback rewrites the ## Live Feedback section in mood.md
// with the current score, state, and behavioral instruction.
func UpdateLiveFeedback(contextDir, dataDir string) {
	path := filepath.Join(contextDir, "mood.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	content := string(data)

	// Remove existing Live Feedback section.
	if idx := strings.Index(content, "## Live Feedback"); idx >= 0 {
		content = strings.TrimRight(content[:idx], "\n")
	}

	score, state := GetTodayScore(dataDir)
	instruction := StateInstruction(state)

	var fb strings.Builder
	fb.WriteString("\n\n## Live Feedback\n")
	fb.WriteString(fmt.Sprintf("Score: %d\n", score))
	fb.WriteString(fmt.Sprintf("State: %s\n", state))
	fb.WriteString(fmt.Sprintf("Updated: %s\n", time.Now().Format("15:04")))
	if instruction != "" {
		fb.WriteString(fmt.Sprintf("Instruction: %s\n", instruction))
	}

	os.WriteFile(path, []byte(content+fb.String()), 0o644)
}
