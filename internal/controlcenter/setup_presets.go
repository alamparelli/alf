package controlcenter

import (
	"embed"
	"encoding/json"
	"log"
	"strings"
)

//go:embed defaults/setup-presets/*.json
var defaultPresetsFS embed.FS

// TierPreset represents a pre-configured set of tiers for a specific backend.
// Presets are loaded from JSON files in config.d/setup-presets/.
type TierPreset struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	Backend      string              `json:"backend"`
	RouterConfig *PresetRouterConfig `json:"router_config,omitempty"`
	Tiers        []Tier              `json:"tiers"`
}

// PresetRouterConfig holds router settings that accompany a tier preset.
type PresetRouterConfig struct {
	RouterModel     string `json:"router_model"`
	RouterBackend   string `json:"router_backend"`
	DefaultFallback string `json:"default_fallback"`
	Distinctions    string `json:"router_distinctions"`
}

// loadEmbeddedPresets returns the built-in presets embedded in the binary.
func loadEmbeddedPresets() map[string][]TierPreset {
	result := make(map[string][]TierPreset)
	entries, err := defaultPresetsFS.ReadDir("defaults/setup-presets")
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := defaultPresetsFS.ReadFile("defaults/setup-presets/" + e.Name())
		if err != nil {
			log.Printf("[setup] warning: embedded preset %s: %v", e.Name(), err)
			continue
		}
		var p TierPreset
		if err := json.Unmarshal(data, &p); err != nil {
			log.Printf("[setup] warning: invalid embedded preset %s: %v", e.Name(), err)
			continue
		}
		if p.ID == "" || p.Backend == "" {
			continue
		}
		result[p.Backend] = append(result[p.Backend], p)
	}
	return result
}
