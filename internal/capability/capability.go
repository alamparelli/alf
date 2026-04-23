// Package capability defines the target interface for everything ALF can execute
// on behalf of the AI: native tools, skills, and apps.
//
// This package is a Step 0 scaffold for the v0.7.10 foundation rework
// (see technical/ARCHITECTURE-v0.7.10.md). Signatures only — no implementation.
// Business code from tooling/, skills/, and marketplace/ migrates here in Step 2.
//
// Dependency rule: capability MUST NOT import memory, ai, sandbox, or runtime.
package capability

import "context"

// Kind classifies the three flavours of Capability.
type Kind int

const (
	KindTool  Kind = iota // native short-lived execution (read_file, bash, grep, ...)
	KindSkill             // prompt + orchestrated tools (commit-push, doc-writer, ...)
	KindApp               // UI iframe + backend (xpost, contacthive, ...)
)

// ID uniquely identifies a Capability within the registry.
type ID string

// Manifest is the versioned, declarative description of a Capability.
// It is the input the Sandbox uses to derive an effective Policy.
type Manifest struct {
	ID          ID
	Kind        Kind
	Name        string
	Version     string
	Description string
	Permissions PermissionSet
}

// PermissionSet enumerates what a Capability declares it needs.
// The Sandbox turns this (plus user tier) into an effective Policy.
type PermissionSet struct {
	FilePaths []string // read/write globs
	Networks  []string // domains / CIDRs
	Secrets   []string // vault key patterns
}

// Input is the arbitrary arguments handed to Execute.
type Input map[string]any

// Output is the result surfaced back to the Runtime.
type Output struct {
	Data  any
	Error string // empty when successful
}

// Capability is the uniform contract every tool, skill, and app implements.
//
// Hard rules (enforced by architecture, not yet by code):
//   - A Capability never calls another Capability directly — the Runtime composes.
//   - A Capability does not know about Memory nor AI. Inputs arrive via Input.
type Capability interface {
	Manifest() Manifest
	Permissions() PermissionSet
	Execute(ctx context.Context, in Input) (Output, error)
}
