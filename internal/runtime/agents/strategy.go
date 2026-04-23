package agents

import (
	"context"
	"fmt"

	"github.com/alamparelli/alf/internal/ai"
)

// StrategyOptions are the per-strategy knobs that the orchestrator needs but
// ai.Request does not model: skill lookup, memory context block, and the
// "source" tag that shows up in TaskMeta.
//
// These live outside the Request on purpose — they are consumer-specific
// policy (which skills, which memory slice) rather than per-turn data. When
// the scheduler wraps the orchestrator, it passes a SkillStore-backed
// SkillLookup and no MemoryContext; when chat_service wraps it later,
// MemoryContext will carry the conversation summary.
type StrategyOptions struct {
	SkillLookup   SkillLookup
	MemoryContext []string
	Source        string
}

// NewStrategy adapts an *Orchestrator to the ai.Strategy contract. The
// returned Strategy ignores the Engine argument handed to Run: the
// orchestrator carries its own Provider internally (via SetResolveProvider)
// and drives its own sub-agent calls. A future refactor will push the
// orchestrator to consume an injected Engine — at that point this shim
// becomes a thin pass-through.
//
// See #340 R5e2.
func NewStrategy(o *Orchestrator, opts StrategyOptions) ai.Strategy {
	return &orchestratorStrategy{orch: o, opts: opts}
}

type orchestratorStrategy struct {
	orch *Orchestrator
	opts StrategyOptions
}

// Run implements ai.Strategy. It translates the ai.Request into the
// orchestrator's RunConfig + userMessage shape, drives Orchestrator.Run in a
// goroutine, and streams the final text + a terminal EventDone (with Usage
// populated from TaskMeta) on the returned channel. Errors surface as a
// single EventError.
func (s *orchestratorStrategy) Run(ctx context.Context, _ ai.Engine, req ai.Request) (<-chan ai.Event, error) {
	if s.orch == nil {
		return nil, fmt.Errorf("agents.NewStrategy: nil Orchestrator")
	}

	userMessage := lastUserMessage(req.Messages)
	if userMessage == "" {
		return nil, fmt.Errorf("agents.Strategy.Run: no user message in Request")
	}

	rc := buildRunConfig(req, s.opts)

	ch := make(chan ai.Event, 4)
	go func() {
		defer close(ch)
		text, meta, err := s.orch.Run(ctx, userMessage, req.SystemPrompts, rc, nil)
		if err != nil {
			select {
			case <-ctx.Done():
			case ch <- ai.Event{Kind: ai.EventError, Err: err}:
			}
			return
		}
		if text != "" {
			select {
			case <-ctx.Done():
				return
			case ch <- ai.Event{Kind: ai.EventToken, Token: text}:
			}
		}
		var usage *ai.Usage
		if meta != nil {
			usage = &ai.Usage{
				CostUSD:  meta.TotalCost,
				NumTurns: meta.Iterations,
				Model:    rc.Model,
			}
		}
		select {
		case <-ctx.Done():
		case ch <- ai.Event{Kind: ai.EventDone, Usage: usage}:
		}
	}()
	return ch, nil
}

// buildRunConfig is the pure translation from ai.Request + StrategyOptions
// to the orchestrator's RunConfig. Split out of Run so unit tests can pin
// the mapping without spinning up a full Orchestrator.
func buildRunConfig(req ai.Request, opts StrategyOptions) RunConfig {
	return RunConfig{
		Model:         string(req.Model),
		Backend:       req.Backend,
		Effort:        req.Effort,
		MaxTurns:      req.MaxTurns,
		SkillLookup:   opts.SkillLookup,
		MemoryContext: opts.MemoryContext,
		Source:        opts.Source,
	}
}

func lastUserMessage(msgs []ai.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == ai.RoleUser {
			return msgs[i].Content
		}
	}
	return ""
}

// Compile-time assertion: the wrapper satisfies ai.Strategy.
var _ ai.Strategy = (*orchestratorStrategy)(nil)
