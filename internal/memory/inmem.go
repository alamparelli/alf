package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// InMem is a process-local Store implementation backed by maps. It is the
// reference fake: Step 1.1 (#335) ships it so contract tests, higher-layer
// unit tests, and future Store implementations all share one baseline.
//
// Safe for concurrent use. Not persisted across restarts.
type InMem struct {
	mu        sync.RWMutex
	msgSeq    atomic.Uint64
	messages  map[ConvID][]Message
	convs     map[ConvID]*convState
	convOrder []ConvID // insertion order for deterministic ListConvs

	documents map[Scope]map[string]Document
	docOrder  map[Scope][]string // preserves Index order for deterministic Search
	prefs     map[string]Value

	nowFn func() int64 // injectable clock; defaults to time.Now().UnixMilli
}

type convState struct {
	info ConvInfo
	// seq is the per-conv 1-based sequence counter for messages.
	seq int64
	// lastWriteOrder is the global msgSeq value of the most recent write
	// into this conv. Used by LatestConvID to break timestamp ties when
	// several convs are written to within the same millisecond.
	lastWriteOrder uint64
}

// NewInMem returns a ready-to-use InMem Store.
func NewInMem() *InMem {
	return &InMem{
		messages:  make(map[ConvID][]Message),
		convs:     make(map[ConvID]*convState),
		documents: make(map[Scope]map[string]Document),
		docOrder:  make(map[Scope][]string),
		prefs:     make(map[string]Value),
		nowFn:     func() int64 { return time.Now().UnixMilli() },
	}
}

// Conversations ---------------------------------------------------------------

func (s *InMem) EnsureConv(ctx context.Context, convID ConvID, title string, channel Channel) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if convID == "" {
		return errors.New("memory: EnsureConv: empty convID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.convs[convID]; ok {
		return nil
	}
	now := s.nowFn()
	s.convs[convID] = &convState{
		info: ConvInfo{
			ID:        convID,
			Title:     title,
			Channel:   channel,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	s.convOrder = append(s.convOrder, convID)
	return nil
}

func (s *InMem) GetConv(ctx context.Context, convID ConvID) (ConvInfo, error) {
	if err := ctx.Err(); err != nil {
		return ConvInfo{}, err
	}
	if convID == "" {
		return ConvInfo{}, errors.New("memory: GetConv: empty convID")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.convs[convID]
	if !ok {
		return ConvInfo{}, nil
	}
	info := c.info
	info.MsgCount = len(s.messages[convID])
	if info.MsgCount > 0 {
		info.LastMessage = s.messages[convID][info.MsgCount-1].CreatedAt
	}
	return info, nil
}

func (s *InMem) ListConvs(ctx context.Context, filter ConvFilter) ([]ConvInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []ConvInfo
	for _, id := range s.convOrder {
		c, ok := s.convs[id]
		if !ok {
			continue
		}
		if filter.Channel != "" && c.info.Channel != filter.Channel {
			continue
		}
		if c.info.Archived && !filter.IncludeArchived {
			continue
		}
		info := c.info
		info.MsgCount = len(s.messages[id])
		if info.MsgCount > 0 {
			info.LastMessage = s.messages[id][info.MsgCount-1].CreatedAt
		}
		out = append(out, info)
	}
	return out, nil
}

func (s *InMem) UpdateConvTitle(ctx context.Context, convID ConvID, title string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if convID == "" {
		return errors.New("memory: UpdateConvTitle: empty convID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.convs[convID]; ok {
		c.info.Title = title
		c.info.UpdatedAt = s.nowFn()
	}
	return nil
}

func (s *InMem) ArchiveConv(ctx context.Context, convID ConvID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if convID == "" {
		return errors.New("memory: ArchiveConv: empty convID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.convs[convID]; ok {
		c.info.Archived = true
		c.info.UpdatedAt = s.nowFn()
	}
	return nil
}

func (s *InMem) DeleteConv(ctx context.Context, convID ConvID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if convID == "" {
		return errors.New("memory: DeleteConv: empty convID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.convs, convID)
	delete(s.messages, convID)
	for i, id := range s.convOrder {
		if id == convID {
			s.convOrder = append(s.convOrder[:i], s.convOrder[i+1:]...)
			break
		}
	}
	return nil
}

func (s *InMem) LatestConvID(ctx context.Context, channel Channel) (ConvID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var bestID ConvID
	var bestOrder uint64
	for id, c := range s.convs {
		if c.info.Archived {
			continue
		}
		if channel != "" && c.info.Channel != channel {
			continue
		}
		if len(s.messages[id]) == 0 {
			continue
		}
		if c.lastWriteOrder > bestOrder {
			bestOrder = c.lastWriteOrder
			bestID = id
		}
	}
	return bestID, nil
}

func (s *InMem) AppendMessage(ctx context.Context, convID ConvID, msg Message) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	if convID == "" {
		return Message{}, errors.New("memory: AppendMessage: empty convID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.convs[convID]
	if !ok {
		now := s.nowFn()
		c = &convState{info: ConvInfo{ID: convID, CreatedAt: now, UpdatedAt: now}}
		s.convs[convID] = c
		s.convOrder = append(s.convOrder, convID)
	}

	n := s.msgSeq.Add(1)
	msg.ID = MsgID(fmt.Sprintf("m%d", n))
	msg.CreatedAt = s.nowFn()
	c.seq++
	msg.Seq = c.seq
	c.info.UpdatedAt = msg.CreatedAt
	c.lastWriteOrder = n

	s.messages[convID] = append(s.messages[convID], msg)
	return msg, nil
}

func (s *InMem) GetMessage(ctx context.Context, convID ConvID, msgID MsgID) (*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if convID == "" {
		return nil, errors.New("memory: GetMessage: empty convID")
	}
	if msgID == "" {
		return nil, errors.New("memory: GetMessage: empty msgID")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range s.messages[convID] {
		if s.messages[convID][i].ID == msgID {
			cp := s.messages[convID][i]
			return &cp, nil
		}
	}
	return nil, nil
}

func (s *InMem) ListMessages(ctx context.Context, convID ConvID, opts ListOpts) ([]Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if convID == "" {
		return nil, errors.New("memory: ListMessages: empty convID")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	raw, ok := s.messages[convID]
	if !ok || len(raw) == 0 {
		return nil, nil
	}

	start := 0
	end := len(raw)
	if opts.After != "" {
		for i, m := range raw {
			if m.ID == opts.After {
				start = i + 1
				break
			}
		}
	}
	if opts.Before != "" {
		for i, m := range raw {
			if m.ID == opts.Before {
				if i < end {
					end = i
				}
				break
			}
		}
	}
	if start >= end {
		return nil, nil
	}
	window := raw[start:end]

	if opts.ApplySummary {
		window = applySummaryInPlace(window)
	}

	if opts.Limit > 0 && len(window) > opts.Limit {
		window = window[len(window)-opts.Limit:]
	}
	out := make([]Message, len(window))
	copy(out, window)
	return out, nil
}

// applySummaryInPlace returns msgs collapsed by the most recent summary in
// the window: covered messages are filtered out, older summaries are
// dropped, and the latest summary is prepended. The input slice is not
// mutated.
func applySummaryInPlace(msgs []Message) []Message {
	var latestIdx = -1
	for i := range msgs {
		if msgs[i].Role == RoleSummary {
			latestIdx = i
		}
	}
	if latestIdx < 0 {
		return msgs
	}
	latest := msgs[latestIdx]
	covered := make(map[MsgID]struct{}, len(latest.CoveredIDs))
	for _, id := range latest.CoveredIDs {
		covered[id] = struct{}{}
	}
	out := make([]Message, 0, len(msgs))
	out = append(out, latest)
	for i := range msgs {
		m := msgs[i]
		if m.Role == RoleSummary {
			continue
		}
		if _, skip := covered[m.ID]; skip {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (s *InMem) AddReaction(ctx context.Context, convID ConvID, msgID MsgID, r Reaction) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if convID == "" {
		return false, errors.New("memory: AddReaction: empty convID")
	}
	if msgID == "" {
		return false, errors.New("memory: AddReaction: empty msgID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.messages[convID]
	for i := range msgs {
		if msgs[i].ID != msgID {
			continue
		}
		for _, existing := range msgs[i].Reactions {
			if existing.Emoji == r.Emoji && existing.Source == r.Source {
				return true, nil
			}
		}
		msgs[i].Reactions = append(msgs[i].Reactions, r)
		return true, nil
	}
	return false, nil
}

func (s *InMem) AppendSummary(ctx context.Context, convID ConvID, text string, coveredIDs []MsgID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if convID == "" {
		return errors.New("memory: AppendSummary: empty convID")
	}
	if text == "" || len(coveredIDs) == 0 {
		return nil
	}
	copied := make([]MsgID, len(coveredIDs))
	copy(copied, coveredIDs)
	_, err := s.AppendMessage(ctx, convID, Message{
		Role:       RoleSummary,
		Content:    text,
		Blocks:     []ContentBlock{{Type: BlockSummary, Text: text}},
		CoveredIDs: copied,
	})
	return err
}

func (s *InMem) LatestSummaryCovered(ctx context.Context, convID ConvID) ([]MsgID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if convID == "" {
		return nil, errors.New("memory: LatestSummaryCovered: empty convID")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	msgs := s.messages[convID]
	var covered []MsgID
	for i := range msgs {
		if msgs[i].Role == RoleSummary {
			covered = msgs[i].CoveredIDs
		}
	}
	if len(covered) == 0 {
		return nil, nil
	}
	out := make([]MsgID, len(covered))
	copy(out, covered)
	return out, nil
}

func (s *InMem) Summarize(ctx context.Context, convID ConvID) (Summary, error) {
	if err := ctx.Err(); err != nil {
		return Summary{}, err
	}
	if convID == "" {
		return Summary{}, errors.New("memory: Summarize: empty convID")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	msgs := s.messages[convID]
	if len(msgs) == 0 {
		return Summary{}, nil
	}
	// If the conv already carries a RoleSummary message, surface it.
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleSummary {
			return Summary{
				ConvID:    convID,
				Text:      msgs[i].Content,
				UpToMsgID: msgs[i].ID,
				CreatedAt: msgs[i].CreatedAt,
			}, nil
		}
	}
	// Deterministic stub summary — the real summarizer lives in runtime/ai.
	return Summary{
		ConvID:    convID,
		Text:      fmt.Sprintf("conv %s: %d message(s)", convID, len(msgs)),
		UpToMsgID: msgs[len(msgs)-1].ID,
		CreatedAt: s.nowFn(),
	}, nil
}

// Embeddings -----------------------------------------------------------------

func (s *InMem) Index(ctx context.Context, scope Scope, doc Document) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if scope == "" {
		return errors.New("memory: Index: empty scope")
	}
	if doc.ID == "" {
		return errors.New("memory: Index: empty Document.ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.documents[scope]; !ok {
		s.documents[scope] = make(map[string]Document)
	}
	if _, existed := s.documents[scope][doc.ID]; !existed {
		s.docOrder[scope] = append(s.docOrder[scope], doc.ID)
	}
	s.documents[scope][doc.ID] = doc
	return nil
}

func (s *InMem) Search(ctx context.Context, scope Scope, query string, k int) ([]Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if k < 0 {
		return nil, errors.New("memory: Search: k must be >= 0")
	}
	if k == 0 {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	docs := s.documents[scope]
	if len(docs) == 0 {
		return nil, nil
	}

	q := strings.ToLower(query)
	type scored struct {
		hit Hit
		idx int // insertion order, for stable sort
	}
	var matches []scored
	for i, id := range s.docOrder[scope] {
		d := docs[id]
		if q != "" && !strings.Contains(strings.ToLower(d.Text), q) {
			continue
		}
		score := float32(1.0)
		if q != "" {
			// crude: longer text => slightly lower score; purely to give Search
			// a deterministic ordering beyond insertion order.
			score = float32(len(q)) / float32(len(d.Text)+1)
		}
		matches = append(matches, scored{Hit{Document: d, Score: score}, i})
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].hit.Score != matches[j].hit.Score {
			return matches[i].hit.Score > matches[j].hit.Score
		}
		return matches[i].idx < matches[j].idx
	})
	if len(matches) > k {
		matches = matches[:k]
	}
	out := make([]Hit, len(matches))
	for i, m := range matches {
		out[i] = m.hit
	}
	return out, nil
}

// Preferences ---------------------------------------------------------------

func (s *InMem) GetPref(ctx context.Context, key string) (Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if key == "" {
		return nil, errors.New("memory: GetPref: empty key")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.prefs[key]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (s *InMem) SetPref(ctx context.Context, key string, val Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return errors.New("memory: SetPref: empty key")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if val == nil {
		delete(s.prefs, key)
		return nil
	}
	s.prefs[key] = val
	return nil
}
