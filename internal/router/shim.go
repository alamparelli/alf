// Package router is a thin re-export shim. The classifier now lives at
// internal/runtime/classifier (moved during #340 R2a). ResolveModel lives
// at internal/ai (moved during #340 A3). Existing consumers keep importing
// internal/router until A5/R6 rewires them to ai + runtime directly.
package router

import (
	"github.com/alamparelli/alf/internal/ai"
	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/runtime/classifier"
)

// --- Types ---------------------------------------------------------------

type Result = classifier.Result
type AgentTeamInfo = classifier.AgentTeamInfo
type ClassifyInput = classifier.ClassifyInput

// --- Classifier re-exports ----------------------------------------------

func BuildSystemPrompt(tiers *cc.TiersConfig, dataDir, configDir string, agentTeams []AgentTeamInfo) string {
	return classifier.BuildSystemPrompt(tiers, dataDir, configDir, agentTeams)
}

func BuildClassifyPrompt(input ClassifyInput) string {
	return classifier.BuildClassifyPrompt(input)
}

func ParseResponse(raw string, tiers *cc.TiersConfig) Result {
	return classifier.ParseResponse(raw, tiers)
}

func InterpretRaw(raw string, tiers *cc.TiersConfig, message string) Result {
	return classifier.InterpretRaw(raw, tiers, message)
}

func ValidTierSet(tiers *cc.TiersConfig) map[string]bool {
	return classifier.ValidTierSet(tiers)
}

func FallbackResult(tiers *cc.TiersConfig) Result {
	return classifier.FallbackResult(tiers)
}

func TierAccess(tierName string, tiers *cc.TiersConfig) string {
	return classifier.TierAccess(tierName, tiers)
}

func HasWriteIntent(message string) bool {
	return classifier.HasWriteIntent(message)
}

// --- Model resolver re-export -------------------------------------------

// ResolveModel is a thin re-export of ai.ResolveModel (moved during A3).
// Removed in A5 once the last consumer switches to ai.ResolveModel.
func ResolveModel(short string) string {
	return string(ai.ResolveModel(short))
}
