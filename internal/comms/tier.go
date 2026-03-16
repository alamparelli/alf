package comms

import (
	"log"
	"strings"

	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/tooling"
)

// TierInfo is a read-only view of a tier for routing decisions.
type TierInfo struct {
	Name         string
	Model        string
	Priority     int
	Enabled      bool
	Routable     bool
	ForceCommand bool
	Tools        []string
	WriteCapable bool
	Effort       string
	MaxTurns     int
	OrchestratorMaxTurns int
	MaxIterations int
	TimeoutMin   int
	Backend      string
	SystemPrompt string
	ContextWeight string // "light", "standard", "full"
}

// TiersSnapshot is a read-only snapshot of tier configuration.
type TiersSnapshot struct {
	Tiers           []TierInfo
	DefaultFallback string
}

// TierStoreReader provides read access to tier configuration.
type TierStoreReader interface {
	Snapshot() TiersSnapshot
}

// ResolveTierParams looks up a tier by name and resolves model, backend, tools.
// resolveModel maps short model names (e.g. "haiku") to full IDs; may be nil.
func ResolveTierParams(tierName string, tiers TiersSnapshot, dataDir string, toolReg *tooling.Registry, provRegistry *provider.Registry, resolveModel func(string) string) (TierParams, bool) {
	for _, t := range tiers.Tiers {
		if t.Name == tierName {
			model := t.Model
			backend := t.Backend
			// Auto-detect API backend from model name.
			if (backend == "" || backend == "cli") && strings.Contains(model, "/") {
				if provRegistry != nil {
					names := provRegistry.BackendNames()
					if len(names) > 0 {
						backend = names[0]
						log.Printf("[comms] tier %q: auto-detected backend=%s for model=%s", tierName, backend, model)
					}
				}
			}
			if backend == "" || backend == "cli" {
				if resolveModel != nil {
					model = resolveModel(t.Model)
				}
			}
			tools := t.Tools
			if len(tools) == 1 && tools[0] == "*" {
				tools = tooling.DiscoverToolNames(dataDir)
				if toolReg != nil {
					tools = append(tools, toolReg.NativeToolNames()...)
				}
				if len(tools) > 0 {
					log.Printf("[comms] tier %q: wildcard resolved to %d tools", tierName, len(tools))
				}
			} else if len(tools) == 1 && tools[0] == "*native" {
				if toolReg != nil {
					tools = toolReg.NativeToolNames()
				} else {
					tools = nil
				}
				if len(tools) > 0 {
					log.Printf("[comms] tier %q: native wildcard resolved to %d tools", tierName, len(tools))
				}
			}
			cw := t.ContextWeight
			if cw == "" {
				cw = "full"
			}
			return TierParams{
				Model:                model,
				Tools:                tools,
				WriteCapable:         t.WriteCapable,
				Effort:               t.Effort,
				MaxTurns:             t.MaxTurns,
				OrchestratorMaxTurns: t.OrchestratorMaxTurns,
				MaxIterations:        t.MaxIterations,
				TimeoutMin:           t.TimeoutMin,
				Backend:              backend,
				SystemPrompt:         t.SystemPrompt,
				ContextWeight:        cw,
			}, true
		}
	}
	return TierParams{Model: "claude-haiku-4-5"}, false
}

// FirstFallbackTier returns the default fallback from config, or the first
// enabled tier, or the first tier overall.
func FirstFallbackTier(tierStore TierStoreReader) string {
	snap := tierStore.Snapshot()
	if snap.DefaultFallback != "" {
		return snap.DefaultFallback
	}
	for _, t := range snap.Tiers {
		if t.Enabled {
			return t.Name
		}
	}
	if len(snap.Tiers) > 0 {
		return snap.Tiers[0].Name
	}
	return ""
}

// OnboardingTier picks a capable tier for onboarding (second priority, e.g. sonnet).
func OnboardingTier(tierStore TierStoreReader) string {
	snap := tierStore.Snapshot()
	type candidate struct {
		name     string
		priority int
	}
	var candidates []candidate
	for _, t := range snap.Tiers {
		if t.Enabled && t.Name != "agent" {
			candidates = append(candidates, candidate{t.Name, t.Priority})
		}
	}
	if len(candidates) >= 2 {
		best := candidates[0]
		second := candidates[1]
		if second.priority < best.priority {
			best, second = second, best
		}
		for _, c := range candidates[2:] {
			if c.priority < best.priority {
				second = best
				best = c
			} else if c.priority < second.priority {
				second = c
			}
		}
		return second.name
	}
	return FirstFallbackTier(tierStore)
}

// TierHasRead returns true if the tier has Read tool access.
func TierHasRead(t TierInfo) bool {
	if t.WriteCapable {
		return true
	}
	for _, tool := range t.Tools {
		if tool == "Read" {
			return true
		}
	}
	return false
}

// LowestMediaTier returns the cheapest enabled tier that has the Read tool.
func LowestMediaTier(tiers TiersSnapshot) string {
	bestName := ""
	bestPriority := int(^uint(0) >> 1)
	for _, t := range tiers.Tiers {
		if t.Enabled && TierHasRead(t) && t.Priority < bestPriority {
			bestName = t.Name
			bestPriority = t.Priority
		}
	}
	if bestName != "" {
		return bestName
	}
	bestPriority = int(^uint(0) >> 1)
	for _, t := range tiers.Tiers {
		if t.Enabled && t.Priority < bestPriority {
			bestName = t.Name
			bestPriority = t.Priority
		}
	}
	if bestName != "" {
		return bestName
	}
	if len(tiers.Tiers) > 0 {
		return tiers.Tiers[0].Name
	}
	return ""
}

// IsTierValid checks if a tier is enabled and routable (or force-commandable).
func IsTierValid(tierName string, tiers TiersSnapshot) bool {
	for _, t := range tiers.Tiers {
		if t.Name == tierName && t.Enabled && (t.Routable || t.ForceCommand) {
			return true
		}
	}
	return false
}
