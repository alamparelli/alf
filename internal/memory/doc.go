// Package memory is the unified persistence surface for conversations,
// embeddings, and user preferences. It is one of the 5 foundational blocks
// defined in technical/ARCHITECTURE-v0.7.10.md and absorbs the current
// chatdb, conversation, memstore, and memory/preferences packages.
//
// # Hard rules
//
// 1. ConvID-in-signature rule. Every function that touches conversation
// state takes a memory.ConvID as an explicit parameter. There is no
// "current conversation" singleton, no ambient convID, no default fallback.
// If a function cannot name the conversation it operates on, it does not
// belong on Store.
//
// Rationale: 5+ bugs in the 0.7.8 cycle traced back to hidden "current conv"
// state (#310, #312, #318, summarize regression, active conv rotation).
// Putting convID in the signature turns a runtime bug into a compile error.
//
// 2. Dependency rule. memory MUST NOT import capability, ai, sandbox, or
// runtime. It is a leaf of the foundation graph:
//
//	consumers  →  runtime  →  { capability, memory, ai, sandbox }
//
// Anything that would require a back-edge (e.g. a Store that calls an AI
// summarizer) is the Runtime's job: the Store returns data, the Runtime
// orchestrates.
//
// 3. Concurrency rule. Every Store implementation MUST be safe for
// concurrent use across goroutines. Callers never need to wrap Store calls
// in their own mutex.
//
// 4. Not-found rule. Missing data is not an error: unset prefs, unknown
// convIDs, and empty search results return zero values with a nil error.
// See the Store doc comment in store.go for the full table.
//
// # What lives here vs. what doesn't
//
// Belongs in memory:
//   - Anything persisted about conversations, summaries, embeddings, prefs.
//   - The in-memory fake (inmem.go) used by tests.
//   - The reusable contract tests (contract.go) that any Store impl must pass.
//
// Does NOT belong in memory:
//   - The AI call that generates a summary (that's ai + runtime).
//   - The routing decision about which conv is "active" (that's a preference
//     stored in memory, but the policy lives in runtime).
//   - Any business rule about when to summarize, purge, or embed (runtime).
//
// # Migration status (as of Step 1.1)
//
// Step 1.1 (this ticket, #335) defines the contract and ships an InMem fake.
// Step 1.2 (#336) migrates chatdb + conversation into a SQLite-backed Store.
// Step 1.3 (#337) migrates memstore + preferences and deletes ConvStore.
// Consumers (controlcenter, comms, router, scheduler, agents) migrate off
// direct chatdb/memstore/conversation imports across those steps.
package memory
