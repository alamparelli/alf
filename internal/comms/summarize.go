package comms

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/memory"
	provider "github.com/alamparelli/alf/internal/ai/provider"
)

// Summarization defaults. The threshold is the number of uncovered messages
// in a conversation beyond which older messages get compressed into a
// summary. KeepLast is how many recent messages stay in full detail.
const (
	DefaultSummarizationThreshold = 20
	DefaultSummarizationKeepLast  = 10
	summarizationTimeout          = 60 * time.Second
)

// summarizationInFlight tracks which (channel, convID) pairs currently
// have a summarization goroutine running, to prevent overlapping runs.
var summarizationInFlight sync.Map // key: "channel|convID" -> struct{}

// maybeSummarizeAsync checks whether the channel's active conversation has
// grown past the threshold and, if so, launches a background goroutine that
// summarizes the older messages and appends a summary record to the store.
//
// The goroutine is fire-and-forget: the current turn does not wait. The
// summary becomes visible on the next turn. Only one summarization runs per
// (channel, convID) at a time; concurrent triggers are skipped.
func (e *ChatEngine) maybeSummarizeAsync(ctx context.Context, channel, convID string) {
	if e.Memory == nil || e.Registry == nil || e.TierStore == nil {
		return
	}
	if !e.SummarizationEnabled {
		return
	}
	threshold := e.SummarizationThreshold
	if threshold <= 0 {
		threshold = DefaultSummarizationThreshold
	}
	keepLast := e.SummarizationKeepLast
	if keepLast <= 0 {
		keepLast = DefaultSummarizationKeepLast
	}
	if keepLast >= threshold {
		return // nonsensical config — refuse to summarize
	}

	// Scope reads to the target convID (not the channel's active conv) —
	// CC multi-tab chat rotates active convID, so channel-scoped reads
	// would mix messages across tabs and reset covered_ids every run.
	raw, _ := e.Memory.ListMessages(ctx, memory.ConvID(convID), memory.ListOpts{ApplySummary: false})
	coveredIDs, _ := e.Memory.LatestSummaryCovered(ctx, memory.ConvID(convID))
	alreadyCovered := make(map[memory.MsgID]struct{}, len(coveredIDs))
	for _, id := range coveredIDs {
		alreadyCovered[id] = struct{}{}
	}

	var toSummarize []memory.Message
	var toSummarizeIDs []memory.MsgID
	var uncovered []memory.Message
	hasSummary := false
	for _, m := range raw {
		if m.Role == memory.RoleSummary {
			hasSummary = true
			continue
		}
		if _, skip := alreadyCovered[m.ID]; skip {
			continue
		}
		uncovered = append(uncovered, m)
	}
	// Visible count = prior summary (1 if any) + uncovered tail. Below
	// threshold means nothing worth compacting yet.
	visible := len(uncovered)
	if hasSummary {
		visible++
	}
	if visible < threshold {
		return
	}
	if len(uncovered) <= keepLast {
		return
	}
	cutoff := len(uncovered) - keepLast
	toSummarize = uncovered[:cutoff]
	for _, m := range toSummarize {
		toSummarizeIDs = append(toSummarizeIDs, m.ID)
	}

	// Build the list of covered IDs we'll record in the summary:
	// prior summary's covered IDs ∪ IDs we're about to summarize.
	covered := make([]memory.MsgID, 0, len(alreadyCovered)+len(toSummarizeIDs))
	for id := range alreadyCovered {
		covered = append(covered, id)
	}
	covered = append(covered, toSummarizeIDs...)

	// Single-flight per (channel, convID).
	key := channel + "|" + convID
	if _, loaded := summarizationInFlight.LoadOrStore(key, struct{}{}); loaded {
		return
	}

	go func() {
		defer summarizationInFlight.Delete(key)
		if err := e.runSummarization(channel, convID, toSummarize, covered); err != nil {
			log.Printf("[summarize] %s: %v", key, err)
		}
	}()
}

// runSummarization selects the cheapest tier, calls the provider with a
// summarization prompt, and appends a summary record on success.
func (e *ChatEngine) runSummarization(channel, convID string, msgs []memory.Message, covered []memory.MsgID) error {
	if len(msgs) == 0 {
		return nil
	}
	tiers := e.TierStore.Snapshot()
	tierName := LowestMediaTier(tiers)
	if tierName == "" {
		return fmt.Errorf("no tier available for summarization")
	}
	tp, ok := ResolveTierParams(tierName, tiers, e.DataDir, e.ToolRegistry, e.Registry, e.ResolveModel)
	if !ok {
		return fmt.Errorf("tier %q did not resolve", tierName)
	}
	prov := e.Registry.ForBackend(tp.Backend)
	if prov == nil {
		return fmt.Errorf("no provider for backend %q", tp.Backend)
	}

	prompt := buildSummarizationPrompt(msgs)
	params := provider.Params{
		Model:        tp.Model,
		Effort:       tp.Effort,
		WriteCapable: false,
		Tools:        nil, // no tools — pure text completion
		DataDir:      e.DataDir,
		MaxTurns:     1,
	}

	ctx, cancel := context.WithTimeout(context.Background(), summarizationTimeout)
	defer cancel()

	result, err := prov.Invoke(ctx, prompt, params, nil)
	if err != nil {
		return fmt.Errorf("provider invoke: %w", err)
	}
	text := strings.TrimSpace(result.Text)
	if text == "" {
		return fmt.Errorf("empty summary text")
	}
	ctx2 := context.Background()
	_ = e.Memory.AppendSummary(ctx2, memory.ConvID(convID), text, covered)
	log.Printf("[summarize] %s: compressed %d messages into %d-char summary (tier=%s, cost=%.4f)",
		channel, len(msgs), len(text), tierName, result.CostUSD)
	return nil
}

// buildSummarizationPrompt renders the messages to summarize as a single
// prompt asking for a condensed, fact-preserving summary.
func buildSummarizationPrompt(msgs []memory.Message) string {
	var sb strings.Builder
	sb.WriteString("You are a conversation summarizer. Produce a concise summary ")
	sb.WriteString("of the exchange below that preserves key facts, user intent, ")
	sb.WriteString("decisions made, and any unresolved questions. Do not add ")
	sb.WriteString("commentary; output only the summary paragraph.\n\n")
	sb.WriteString("=== conversation ===\n")
	for _, m := range msgs {
		text := memory.TextContent(m)
		if text == "" {
			continue
		}
		role := m.Role
		if role == "" {
			role = "user"
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", role, text))
	}
	sb.WriteString("=== end ===\n")
	return sb.String()
}
