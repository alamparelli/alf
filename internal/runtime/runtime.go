// Package runtime is the single orchestrator that composes capability + memory
// + ai + sandbox. It is the only package allowed to import all four.
//
// This package is a Step 0 scaffold for the v0.7.10 foundation rework
// (see technical/ARCHITECTURE-v0.7.10.md). Signatures only — no implementation.
//
// Dependency rule:
//   - runtime MAY import capability, memory, ai, sandbox.
//   - None of the four may import runtime.
//   - Consumers (controlcenter, telegram, scheduler, cli, ...) import ONLY runtime.
//
// What Runtime does per turn:
//  1. Resolve the Capability from the registry.
//  2. Load history from Memory (scoped by convID).
//  3. Derive the Policy from Manifest + user tier.
//  4. Sandbox.Apply to prepare a scoped ctx.
//  5. AI.Run with the prepared Request.
//  6. Loop on ToolCall events: execute each Capability through Sandbox, reinject results.
//  7. Persist the turn into Memory.
package runtime

import (
	"context"

	"github.com/alamparelli/alf/internal/ai"
	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/sandbox"
)

// Args are the arguments handed to a one-shot Invoke.
type Args map[string]any

// EventKind enumerates the unified stream surfaced to consumers.
type EventKind int

const (
	EventToken      EventKind = iota // streamed assistant token
	EventToolResult                  // a Capability finished executing
	EventDone                        // terminal
	EventError                       // terminal error
)

// Event is the unified stream item returned to UI / scheduler / telegram.
// It merges AI token events with Capability execution results, hiding the
// internal loop.
type Event struct {
	Kind       EventKind
	Token      string             // set when Kind == EventToken
	ToolResult *capability.Output // set when Kind == EventToolResult
	ToolName   string             // set when Kind == EventToolResult
	Err        error              // set when Kind == EventError
}

// Output is the result of a one-shot Invoke (scheduler, button, ...).
type Output = capability.Output

// Runtime is the single orchestration surface exposed to all consumers.
type Runtime interface {
	// Chat processes one user turn inside a conversation. Streams events.
	Chat(ctx context.Context, convID memory.ConvID, userInput string) (<-chan Event, error)

	// Invoke runs a single Capability (scheduler, UI button, cron).
	Invoke(ctx context.Context, capID capability.ID, args Args) (Output, error)
}

// Deps groups the collaborators. The concrete Runtime (next milestones) wires
// these together; at Step 0 the struct exists only to document the surface.
type Deps struct {
	Registry CapabilityRegistry
	Memory   memory.Store
	AI       ai.Engine
	Sandbox  sandbox.Sandbox
}

// CapabilityRegistry is the resolver that turns an ID into a Capability.
// Lives here (not in capability/) to keep capability dependency-free.
type CapabilityRegistry interface {
	Resolve(id capability.ID) (capability.Capability, bool)
	List() []capability.Manifest
}
