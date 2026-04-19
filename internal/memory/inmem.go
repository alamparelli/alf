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
	documents map[Scope]map[string]Document
	docOrder  map[Scope][]string // preserves Index order for deterministic Search
	prefs     map[string]Value

	nowFn func() int64 // injectable clock; defaults to time.Now().UnixMilli
}

// NewInMem returns a ready-to-use InMem Store.
func NewInMem() *InMem {
	return &InMem{
		messages:  make(map[ConvID][]Message),
		documents: make(map[Scope]map[string]Document),
		docOrder:  make(map[Scope][]string),
		prefs:     make(map[string]Value),
		nowFn:     func() int64 { return time.Now().UnixMilli() },
	}
}

// Conversations ---------------------------------------------------------------

func (s *InMem) AppendMessage(ctx context.Context, convID ConvID, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if convID == "" {
		return errors.New("memory: AppendMessage: empty convID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	n := s.msgSeq.Add(1)
	msg.ID = MsgID(fmt.Sprintf("m%d", n))
	msg.CreatedAt = s.nowFn()
	s.messages[convID] = append(s.messages[convID], msg)
	return nil
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
	out := make([]Message, end-start)
	copy(out, raw[start:end])
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
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
