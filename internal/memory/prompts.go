package memory

import _ "embed"

// Centralized prompt templates. Edit the .md files to modify prompts.
// Dynamic parts (tier lists, agent teams, emoji lists) are injected at call sites.

//go:embed core.md
var coreMD string

//go:embed onboarding.md
var OnboardingMD string

//go:embed reaction.md
var ReactionMD string

//go:embed orchestrator.md
var OrchestratorMD string

//go:embed router.md
var RouterMD string
