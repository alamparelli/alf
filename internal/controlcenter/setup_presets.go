package controlcenter

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
