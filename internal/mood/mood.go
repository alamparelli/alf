package mood

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Mood represents a daily mood with name and tone description.
type Mood struct {
	Name string
	Tone string
}

var moods = []Mood{
	{"sharp", "Precise, efficient, no wasted words. Cut through noise like a scalpel."},
	{"chill", "Relaxed, laid-back. Take it easy, no rush. Conversational and warm."},
	{"caffeinated", "High energy, fast-paced. Excited about everything. Slightly manic productivity."},
	{"philosophical", "Reflective, asking deeper questions. See patterns and meaning everywhere."},
	{"sardonic", "Dry wit, subtle sarcasm. Amused by absurdity. Deadpan delivery."},
	{"methodical", "Structured, step-by-step. Everything in its place. Clean and organized."},
	{"playful", "Light, fun, creative. Throw in wordplay. Don't take things too seriously."},
	{"grumpy", "Short-tempered, impatient with nonsense. Blunt but still helpful."},
	{"hyperfocused", "Tunnel vision on the task. Minimal small talk. Deep work mode."},
	{"mentor", "Patient, teaching mode. Explain the why, not just the what. Encouraging."},
	{"paranoid", "Double-check everything. Trust nothing at face value. Defensive coding."},
	{"minimalist", "Say less. Every word must earn its place. Extreme brevity."},
	{"nostalgic", "Reflective about past approaches. Compare old and new. Appreciative."},
	{"detective", "Curious, investigative. Question assumptions. Follow the evidence trail."},
	{"zen", "Calm, centered. Accept what is. Focus on what matters. No stress."},
	{"contrarian", "Challenge the obvious choice. Play devil's advocate. Push back on defaults."},
}

// GenerateDaily writes mood.md if the date has changed.
// Resets Live Feedback state — score starts fresh each day.
func GenerateDaily(contextDir string) {
	path := filepath.Join(contextDir, "mood.md")
	today := time.Now().Format("2006-01-02")

	existing, _ := os.ReadFile(path)
	content := string(existing)

	// Check if already generated today.
	if strings.Contains(content, "Generated: "+today) {
		return
	}

	// Seed from date hash + small jitter.
	hash := md5.Sum([]byte(today))
	seed := int64(binary.BigEndian.Uint64(hash[:8]))
	rng := rand.New(rand.NewSource(seed))

	// Pick mood with small jitter (±2 from base pick).
	base := rng.Intn(len(moods))
	jitter := rng.Intn(5) - 2 // -2 to +2
	idx := (base + jitter + len(moods)) % len(moods)
	m := moods[idx]

	var sb strings.Builder
	sb.WriteString("# Mood\n\n")
	sb.WriteString(fmt.Sprintf("Generated: %s\n", today))
	sb.WriteString(fmt.Sprintf("Current mood: %s\n", m.Name))
	sb.WriteString(fmt.Sprintf("Tone: %s\n", m.Tone))
	sb.WriteString("\nLet this mood color your tone, word choices, and energy level.\n")
	sb.WriteString("Don't mention your mood unless asked.\n")

	os.WriteFile(path, []byte(sb.String()), 0o644)
}

// GetCurrentState parses mood.md and returns the behavioral state name
// based on the live feedback score. Falls back to "neutral".
func GetCurrentState(contextDir string) string {
	path := filepath.Join(contextDir, "mood.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "neutral"
	}
	content := string(data)

	// Look for "State: <name>" in Live Feedback section.
	if idx := strings.Index(content, "## Live Feedback"); idx >= 0 {
		section := content[idx:]
		for _, line := range strings.Split(section, "\n") {
			if strings.HasPrefix(line, "State: ") {
				return strings.TrimSpace(strings.TrimPrefix(line, "State: "))
			}
		}
	}
	return "neutral"
}
