package comms

import (
	"fmt"
	"log"
	"strings"
)

const (
	recallDistanceThreshold = 1.2
	recallLimit             = 3
	recallMinMessageLength  = 5
)

// RecallResult contains the formatted recall block and best distance score.
type RecallResult struct {
	Block    string  // formatted system prompt block, or ""
	BestDist float64 // best (lowest) distance; 2.0 if no results
	Count    int     // number of injected memories
}

// Recall searches long-term memory for relevant context.
// Returns ("", 2.0, 0) if nothing relevant or recaller is nil.
func Recall(recaller MemoryRecaller, message string) RecallResult {
	if recaller == nil || len(message) < recallMinMessageLength {
		return RecallResult{BestDist: 2.0}
	}

	q := message
	if len(q) > 60 {
		q = q[:60] + "..."
	}

	results, err := recaller.Search(message, recallLimit)
	if err != nil {
		log.Printf("[comms] recall: search error for %q: %v", q, err)
		return RecallResult{BestDist: 2.0}
	}
	if len(results) == 0 {
		log.Printf("[comms] recall: no results for %q", q)
		return RecallResult{BestDist: 2.0}
	}

	var sb strings.Builder
	bestDist := 2.0
	filtered := 0
	count := 0

	for _, r := range results {
		if r.Distance >= recallDistanceThreshold {
			filtered++
			continue
		}
		if r.Distance < bestDist {
			bestDist = r.Distance
		}
		if sb.Len() == 0 {
			sb.WriteString("=== [auto-recall] ===\nRelevant memories about the user (auto-retrieved):\n")
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", r.Type, r.Text))
		count++
	}

	if count > 0 {
		log.Printf("[comms] recall: injected %d memories for %q (best=%.2f, filtered %d by distance)", count, q, bestDist, filtered)
	} else {
		log.Printf("[comms] recall: %d results for %q but all filtered by distance (>=%.1f)", len(results), q, recallDistanceThreshold)
	}

	return RecallResult{
		Block:    sb.String(),
		BestDist: bestDist,
		Count:    count,
	}
}
