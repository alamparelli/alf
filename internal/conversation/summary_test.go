package conversation

import (
	"strings"
	"testing"
	"time"
)

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func makeMsg(id, ch, conv, role, text string) Message {
	return Message{
		ID:        id,
		ConvID:    conv,
		Channel:   ch,
		Role:      role,
		Blocks:    []ContentBlock{{Type: BlockText, Text: text}},
		Timestamp: time.Now(),
	}
}

// Summary replaces covered messages and is prepended; uncovered tail remains.
func TestSummary_RecentAppliesSummary(t *testing.T) {
	s := NewStore(t.TempDir())
	ch := "tg:1"
	conv := s.ConvID(ch)

	s.Append(makeMsg("m1", ch, conv, "user", "hello"))
	s.Append(makeMsg("m2", ch, conv, "assistant", "hi"))
	s.Append(makeMsg("m3", ch, conv, "user", "keep this"))
	s.Append(makeMsg("m4", ch, conv, "assistant", "and this"))

	s.AppendSummary(ch, conv, "earlier greetings", []string{"m1", "m2"})

	got := s.Recent(ch, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 (summary + m3 + m4), got %d", len(got))
	}
	if got[0].Role != RoleSummary {
		t.Errorf("want first msg role=summary, got %q", got[0].Role)
	}
	if got[1].ID != "m3" || got[2].ID != "m4" {
		t.Errorf("tail out of order: %s, %s", got[1].ID, got[2].ID)
	}
}

// Only the latest summary applies; earlier summary records are dropped.
func TestSummary_LatestSummaryWins(t *testing.T) {
	s := NewStore(t.TempDir())
	ch := "tg:1"
	conv := s.ConvID(ch)

	for i, txt := range []string{"a", "b", "c", "d", "e"} {
		id := string(rune('0' + i))
		s.Append(makeMsg("m"+id, ch, conv, "user", txt))
	}

	s.AppendSummary(ch, conv, "first batch", []string{"m0", "m1"})
	s.AppendSummary(ch, conv, "bigger batch", []string{"m0", "m1", "m2", "m3"})

	got := s.Recent(ch, 0)
	if len(got) != 2 {
		t.Fatalf("expected summary + m4, got %d msgs", len(got))
	}
	if got[0].Role != RoleSummary || got[0].Blocks[0].Text != "bigger batch" {
		t.Errorf("want latest summary, got %+v", got[0].Blocks)
	}
	if got[1].ID != "m4" {
		t.Errorf("want m4 as tail, got %s", got[1].ID)
	}
}

// AppendSummary ignores empty inputs (no-op, no panic).
func TestSummary_AppendSummaryValidation(t *testing.T) {
	s := NewStore(t.TempDir())
	ch := "tg:1"
	conv := s.ConvID(ch)

	s.AppendSummary(ch, "", "x", []string{"a"})               // no convID
	s.AppendSummary(ch, conv, "", []string{"a"})              // no text
	s.AppendSummary(ch, conv, "x", nil)                       // no covered
	s.AppendSummary(ch, conv, "x", []string{})                // empty covered

	if got := s.Recent(ch, 0); len(got) != 0 {
		t.Fatalf("expected no messages, got %d", len(got))
	}
}

// RecentRaw returns all messages including any summary records, without collapsing.
func TestSummary_RecentRawNoCollapse(t *testing.T) {
	s := NewStore(t.TempDir())
	ch := "tg:1"
	conv := s.ConvID(ch)

	s.Append(makeMsg("m1", ch, conv, "user", "a"))
	s.Append(makeMsg("m2", ch, conv, "assistant", "b"))
	s.AppendSummary(ch, conv, "summary", []string{"m1"})

	raw := s.RecentRaw(ch)
	if len(raw) != 3 {
		t.Fatalf("expected 3 raw msgs (m1, m2, summary), got %d", len(raw))
	}
}

// LastSummaryCovered exposes covered IDs and returns nil when no summary exists.
func TestSummary_LastSummaryCovered(t *testing.T) {
	s := NewStore(t.TempDir())
	ch := "tg:1"
	conv := s.ConvID(ch)

	s.Append(makeMsg("m1", ch, conv, "user", "a"))
	if got := s.LastSummaryCovered(ch); got != nil {
		t.Fatalf("expected nil before any summary, got %v", got)
	}

	s.AppendSummary(ch, conv, "sum", []string{"m1", "m2"})
	got := s.LastSummaryCovered(ch)
	if len(got) != 2 {
		t.Fatalf("expected 2 covered IDs, got %d", len(got))
	}
	if _, ok := got["m1"]; !ok {
		t.Error("missing m1")
	}
	if _, ok := got["m2"]; !ok {
		t.Error("missing m2")
	}
}

// BuildContext preserves summary blocks.
func TestSummary_BuildContextKeepsSummary(t *testing.T) {
	msgs := []Message{
		{
			ID:     "s1",
			Role:   RoleSummary,
			Blocks: []ContentBlock{{Type: BlockSummary, Text: "older stuff"}},
		},
		{
			ID:     "m1",
			Role:   "user",
			Blocks: []ContentBlock{{Type: BlockText, Text: "new"}},
		},
	}
	out := BuildContext(msgs, 10)
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
	if out[0].Role != RoleSummary || out[0].Blocks[0].Type != BlockSummary {
		t.Errorf("summary not preserved: %+v", out[0])
	}
}

// FormatAsSystemPrompt renders summary with a distinct header.
func TestSummary_FormatAsSystemPrompt(t *testing.T) {
	msgs := []Message{
		{
			Role:   RoleSummary,
			Blocks: []ContentBlock{{Type: BlockSummary, Text: "early conv digest"}},
		},
		{
			Role:   "user",
			Blocks: []ContentBlock{{Type: BlockText, Text: "later"}},
		},
	}
	out := FormatAsSystemPrompt(msgs)
	if out == "" {
		t.Fatal("empty output")
	}
	if !contains(out, "summary of earlier conversation") {
		t.Errorf("expected summary header, got: %s", out)
	}
	if !contains(out, "early conv digest") {
		t.Errorf("expected summary body, got: %s", out)
	}
	if !contains(out, "later") {
		t.Errorf("expected tail message, got: %s", out)
	}
}

// FlattenForAPI emits summary as a system-role message.
func TestSummary_FlattenForAPI(t *testing.T) {
	msgs := []Message{
		{Role: RoleSummary, Blocks: []ContentBlock{{Type: BlockSummary, Text: "digest"}}},
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "hi"}}},
	}
	out := FlattenForAPI(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 api messages, got %d", len(out))
	}
	if out[0].Role != "system" {
		t.Errorf("want summary role=system, got %q", out[0].Role)
	}
	if !contains(out[0].Content, "digest") {
		t.Errorf("summary body missing: %s", out[0].Content)
	}
	if out[1].Role != "user" || out[1].Content != "hi" {
		t.Errorf("tail message wrong: %+v", out[1])
	}
}

// FlattenForOpenAI emits summary as a system-role message.
func TestSummary_FlattenForOpenAI(t *testing.T) {
	msgs := []Message{
		{Role: RoleSummary, Blocks: []ContentBlock{{Type: BlockSummary, Text: "digest"}}},
		{Role: "user", Blocks: []ContentBlock{{Type: BlockText, Text: "hi"}}},
	}
	out := FlattenForOpenAI(msgs)
	if len(out) < 2 {
		t.Fatalf("expected >=2 messages, got %d", len(out))
	}
	if out[0].Role != "system" || !contains(out[0].Content, "digest") {
		t.Errorf("summary not emitted as system: %+v", out[0])
	}
}

// Recent with n limit still includes summary correctly in the slice window.
func TestSummary_RecentWithLimit(t *testing.T) {
	s := NewStore(t.TempDir())
	ch := "tg:1"
	conv := s.ConvID(ch)

	for i := 0; i < 5; i++ {
		id := string(rune('0' + i))
		s.Append(makeMsg("m"+id, ch, conv, "user", id))
	}
	s.AppendSummary(ch, conv, "s", []string{"m0", "m1", "m2"})

	got := s.Recent(ch, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
}

// Summary persists across store reloads (JSONL roundtrip).
func TestSummary_PersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	ch := "tg:1"
	conv := s.ConvID(ch)

	s.Append(makeMsg("m1", ch, conv, "user", "a"))
	s.Append(makeMsg("m2", ch, conv, "assistant", "b"))
	s.AppendSummary(ch, conv, "digest", []string{"m1"})

	s2 := NewStore(dir)
	got := s2.Recent(ch, 0)
	if len(got) != 2 {
		t.Fatalf("expected summary + m2 after reload, got %d", len(got))
	}
	if got[0].Role != RoleSummary {
		t.Errorf("want summary first, got %q", got[0].Role)
	}
	if got[1].ID != "m2" {
		t.Errorf("want m2 as tail, got %s", got[1].ID)
	}
}

// RecentRawByConv and LastSummaryCoveredByConv must be scoped by the
// passed convID, not by the channel's active conversation. Regression
// guard for the CC multi-tab summarizer bug where messages from conv A
// were returned while the summarizer targeted conv B, causing covered
// IDs to reset on every run.
func TestSummary_ByConv_IgnoresChannelActiveConv(t *testing.T) {
	s := NewStore(t.TempDir())
	ch := "cc"

	// Two concurrent convs on the same channel (multi-tab CC chat).
	convA := "conv-aaaa"
	convB := "conv-bbbb"

	s.Append(makeMsg("a1", ch, convA, "user", "hi A"))
	s.Append(makeMsg("a2", ch, convA, "assistant", "hello A"))
	s.AppendSummary(ch, convA, "sumA", []string{"a1", "a2"})

	s.Append(makeMsg("b1", ch, convB, "user", "hi B"))
	s.Append(makeMsg("b2", ch, convB, "assistant", "hello B"))

	// RecentRawByConv(convA) must only return convA messages (incl. summary),
	// independent of whichever convID got written last on the channel.
	rawA := s.RecentRawByConv(convA)
	if len(rawA) != 3 {
		t.Fatalf("convA raw: want 3 (a1,a2,summary), got %d", len(rawA))
	}
	for _, m := range rawA {
		if m.ConvID != convA {
			t.Errorf("convA raw leaked msg from %s", m.ConvID)
		}
	}

	rawB := s.RecentRawByConv(convB)
	if len(rawB) != 2 {
		t.Fatalf("convB raw: want 2 (b1,b2), got %d", len(rawB))
	}

	// LastSummaryCoveredByConv(convA) → 2 covered IDs.
	covA := s.LastSummaryCoveredByConv(convA)
	if len(covA) != 2 {
		t.Fatalf("convA covered: want 2, got %d", len(covA))
	}

	// LastSummaryCoveredByConv(convB) → nil (no summary in convB).
	if got := s.LastSummaryCoveredByConv(convB); got != nil {
		t.Fatalf("convB covered: want nil (no summary), got %v", got)
	}
}

// RecentRaw (channel variant) should still work and follow the channel's
// active conv. Sanity check so the refactor didn't regress the old API.
func TestSummary_RecentRaw_StillFollowsChannelActive(t *testing.T) {
	s := NewStore(t.TempDir())
	ch := "cc"
	conv := s.ConvID(ch)
	s.Append(makeMsg("x1", ch, conv, "user", "x"))
	raw := s.RecentRaw(ch)
	if len(raw) != 1 || raw[0].ID != "x1" {
		t.Fatalf("channel-scoped RecentRaw broken: %+v", raw)
	}
}
