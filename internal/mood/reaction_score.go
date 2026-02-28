package mood

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// EmojiWeights maps emoji to their sentiment score.
var EmojiWeights = map[string]int{
	// Strong positive (+3)
	"🔥": 3, "💯": 3, "❤": 3, "❤\u200d🔥": 3, "🏆": 3, "🤩": 3, "🥰": 3,
	// Mild positive (+1)
	"👍": 1, "👏": 1, "🎉": 1, "😁": 1, "👌": 1, "💘": 1, "😍": 1,
	"🙏": 1, "😘": 1,
	// Neutral (0)
	"🤔": 0, "😐": 0, "🥱": 0, "👀": 0,
	// Mild negative (−3)
	"👎": -3, "😢": -3, "💔": -3, "😡": -3, "🤨": -3,
	// Strong negative (−6)
	"💩": -6, "🤬": -6, "🤮": -6,
}

// State thresholds and behavioral instructions.
type behaviorState struct {
	Name        string
	MinScore    int
	Instruction string
}

var states = []behaviorState{
	{"on_fire", 6, "Be opinionated. Don't hedge. Push ideas further."},
	{"flowing", 3, "Good momentum. Stay direct and confident."},
	{"neutral", -2, ""},
	{"careful", -6, "Keep responses shorter. Verify before acting. Ask when uncertain."},
	// off_track is the catch-all below -6.
}

const offTrackInstruction = "Something's off. Explicitly say what you think the issue is and how you'll fix it."

// IsNegative returns true if the emoji has a negative weight.
func IsNegative(emoji string) bool {
	w, ok := EmojiWeights[emoji]
	return ok && w < 0
}

// IsStrongNegative returns true if the emoji has a strong negative weight (≤-6).
func IsStrongNegative(emoji string) bool {
	w, ok := EmojiWeights[emoji]
	return ok && w <= -6
}

type reactionEntry struct {
	Timestamp time.Time `json:"ts"`
	Emoji     string    `json:"emoji"`
	MessageID int64     `json:"msg_id"`
	Weight    int       `json:"weight"`
}

// LogReaction appends a reaction to the JSONL log.
func LogReaction(dataDir, emoji string, messageID int64) {
	logDir := filepath.Join(dataDir, "logs")
	os.MkdirAll(logDir, 0o755)
	path := filepath.Join(logDir, "reactions.jsonl")

	weight := EmojiWeights[emoji]

	entry := reactionEntry{
		Timestamp: time.Now(),
		Emoji:     emoji,
		MessageID: messageID,
		Weight:    weight,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", data)
}

// GetTodayScore returns the weighted score for today and the state name.
// Reactions from the last 30 minutes get a 2x recency boost.
func GetTodayScore(dataDir string) (int, string) {
	path := filepath.Join(dataDir, "logs", "reactions.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return 0, "neutral"
	}
	defer f.Close()

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	recentCutoff := now.Add(-30 * time.Minute)

	score := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e reactionEntry
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if e.Timestamp.Before(todayStart) {
			continue
		}
		w := e.Weight
		if e.Timestamp.After(recentCutoff) {
			w *= 2 // recency boost
		}
		score += w
	}

	return score, scoreToState(score)
}

func scoreToState(score int) string {
	for _, s := range states {
		if score >= s.MinScore {
			return s.Name
		}
	}
	return "off_track"
}

// StateInstruction returns the behavioral instruction for a state.
func StateInstruction(state string) string {
	for _, s := range states {
		if s.Name == state {
			return s.Instruction
		}
	}
	if state == "off_track" {
		return offTrackInstruction
	}
	return ""
}
