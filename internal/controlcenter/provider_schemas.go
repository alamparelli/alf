package controlcenter

// AllProviderSchemas returns the static registry of all known provider types.
// This is the single source of truth for provider form schemas.
func AllProviderSchemas() []ProviderSchema {
	return []ProviderSchema{
		{
			ID:              "cli",
			Name:            "Claude",
			Description:     "Anthropic via local CLI",
			Type:            "cli",
			SupportsTools:   true,
			Auth:            "cli",
			HasNativeTools:  true,
			SupportsEffort:  true,
			SupportsWriting: true,
			DefaultHints: &TierHints{
				WriteCapable:  boolPtr(true),
				Effort:        "medium",
				ContextWeight: "full",
				Tools:         []string{"*"},
				MaxTurns:      10,
				TimeoutMin:    15,
			},
		},
		{
			ID:              "codex",
			Name:            "OpenAI Codex",
			Description:     "OpenAI via local CLI",
			Type:            "cli",
			SupportsTools:   false,
			HasNativeTools:  true,
			SupportsEffort:  true,
			SupportsWriting: false,
			Fields: []ProviderField{
				{Key: "api_key", Label: "API Key (optional)", Placeholder: "sk-... or leave empty for codex login", Type: "password"},
			},
			DefaultHints: &TierHints{
				WriteCapable:  boolPtr(false),
				Effort:        "medium",
				ContextWeight: "full",
				Tools:         []string{"*native"},
				MaxTurns:      10,
				TimeoutMin:    15,
			},
		},
		{
			ID:              "openrouter",
			Name:            "OpenRouter",
			Description:     "Multi-model gateway",
			Type:            "api",
			SupportsTools:   true,
			HasNativeTools:  false,
			SupportsEffort:  true,
			SupportsWriting: true,
			DefaultURL:      "https://openrouter.ai/api/v1",
			Auth:            "bearer",
			Fields: []ProviderField{
				{Key: "api_key", Label: "API Key", Placeholder: "sk-or-...", Type: "password", Required: true},
			},
			DefaultHints: &TierHints{
				WriteCapable:  boolPtr(true),
				Effort:        "medium",
				ContextWeight: "standard",
				Tools:         []string{"*"},
				MaxTurns:      8,
				TimeoutMin:    10,
			},
		},
		{
			ID:              "openai",
			Name:            "OpenAI",
			Description:     "GPT models",
			Type:            "api",
			SupportsTools:   true,
			HasNativeTools:  false,
			SupportsEffort:  true,
			SupportsWriting: true,
			DefaultURL:      "https://api.openai.com/v1",
			Auth:            "bearer",
			Fields: []ProviderField{
				{Key: "base_url", Label: "Base URL", Placeholder: "https://api.openai.com/v1", Type: "url", DefaultVal: "https://api.openai.com/v1"},
				{Key: "api_key", Label: "API Key", Placeholder: "sk-...", Type: "password", Required: true},
			},
			DefaultHints: &TierHints{
				WriteCapable:  boolPtr(true),
				Effort:        "medium",
				ContextWeight: "standard",
				Tools:         []string{"*"},
				MaxTurns:      8,
				TimeoutMin:    10,
			},
		},
		{
			ID:              "ollama",
			Name:            "Ollama",
			Description:     "Local models",
			Type:            "local",
			SupportsTools:   true,
			HasNativeTools:  false,
			SupportsEffort:  false,
			SupportsWriting: false,
			DefaultURL:      "http://host.docker.internal:11434/v1",
			Auth:            "none",
			Fields: []ProviderField{
				{Key: "base_url", Label: "Base URL", Placeholder: "http://host.docker.internal:11434/v1", Type: "url", DefaultVal: "http://host.docker.internal:11434/v1"},
			},
			DefaultHints: &TierHints{
				WriteCapable:  boolPtr(false),
				Effort:        "low",
				ContextWeight: "light",
				Tools:         []string{"*native"},
				MaxTurns:      5,
				TimeoutMin:    5,
			},
		},
		{
			ID:              "custom",
			Name:            "Custom",
			Description:     "OpenAI-compatible endpoint",
			Type:            "api",
			SupportsTools:   true,
			HasNativeTools:  false,
			SupportsEffort:  false,
			SupportsWriting: false,
			Auth:            "bearer",
			Fields: []ProviderField{
				{Key: "base_url", Label: "Base URL", Placeholder: "https://...", Type: "url", Required: true},
				{Key: "api_key", Label: "API Key", Placeholder: "sk-...", Type: "password"},
				{Key: "default_model", Label: "Default model", Placeholder: "model-name", Type: "text"},
			},
			DefaultHints: &TierHints{
				WriteCapable:  boolPtr(false),
				Effort:        "low",
				ContextWeight: "standard",
				Tools:         []string{"*native"},
				MaxTurns:      5,
				TimeoutMin:    10,
			},
		},
	}
}

// KnownProviderIDs returns the set of all known provider schema IDs.
func KnownProviderIDs() map[string]bool {
	ids := make(map[string]bool)
	for _, s := range AllProviderSchemas() {
		ids[s.ID] = true
	}
	return ids
}

// AnnotateConfigured returns a copy of schemas with Configured=true for
// backends that are present in the registered backend list.
func AnnotateConfigured(schemas []ProviderSchema, registered []string) []ProviderSchema {
	set := make(map[string]bool, len(registered))
	for _, name := range registered {
		set[name] = true
	}
	// CLI is always configured.
	set["cli"] = true

	result := make([]ProviderSchema, len(schemas))
	for i, s := range schemas {
		s.Configured = set[s.ID]
		result[i] = s
	}
	return result
}
