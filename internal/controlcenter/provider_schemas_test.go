package controlcenter

import "testing"

func TestAllProviderSchemas(t *testing.T) {
	schemas := AllProviderSchemas()

	expectedIDs := []string{"cli", "codex", "openrouter", "openai", "ollama", "custom"}
	if len(schemas) != len(expectedIDs) {
		t.Fatalf("expected %d provider schemas, got %d", len(expectedIDs), len(schemas))
	}

	idSet := make(map[string]bool)
	for _, s := range schemas {
		idSet[s.ID] = true
		if s.Name == "" {
			t.Errorf("provider %q has empty Name", s.ID)
		}
		if s.Type == "" {
			t.Errorf("provider %q has empty Type", s.ID)
		}
		if s.Description == "" {
			t.Errorf("provider %q has empty Description", s.ID)
		}
	}

	for _, id := range expectedIDs {
		if !idSet[id] {
			t.Errorf("missing provider schema for %q", id)
		}
	}
}

func TestAllProviderSchemas_NoDuplicateIDs(t *testing.T) {
	schemas := AllProviderSchemas()
	seen := make(map[string]bool)
	for _, s := range schemas {
		if seen[s.ID] {
			t.Errorf("duplicate provider schema ID: %q", s.ID)
		}
		seen[s.ID] = true
	}
}

func TestProviderSchemaFields(t *testing.T) {
	schemas := AllProviderSchemas()
	byID := make(map[string]ProviderSchema)
	for _, s := range schemas {
		byID[s.ID] = s
	}

	// CLI has no fields.
	if len(byID["cli"].Fields) != 0 {
		t.Error("cli should have no fields")
	}

	// OpenRouter requires api_key.
	or := byID["openrouter"]
	if len(or.Fields) != 1 || or.Fields[0].Key != "api_key" {
		t.Error("openrouter should have exactly one field: api_key")
	}
	if !or.Fields[0].Required {
		t.Error("openrouter api_key should be required")
	}

	// Ollama has base_url, no required api_key.
	ol := byID["ollama"]
	if len(ol.Fields) != 1 || ol.Fields[0].Key != "base_url" {
		t.Error("ollama should have exactly one field: base_url")
	}
	if ol.Auth != "none" {
		t.Errorf("ollama auth should be 'none', got %q", ol.Auth)
	}

	// OpenAI has base_url + api_key.
	oa := byID["openai"]
	if len(oa.Fields) != 2 {
		t.Errorf("openai should have 2 fields, got %d", len(oa.Fields))
	}

	// Custom has 3 fields.
	cu := byID["custom"]
	if len(cu.Fields) != 3 {
		t.Errorf("custom should have 3 fields, got %d", len(cu.Fields))
	}
}

func TestKnownProviderIDs(t *testing.T) {
	ids := KnownProviderIDs()
	for _, id := range []string{"cli", "codex", "openrouter", "openai", "ollama", "custom"} {
		if !ids[id] {
			t.Errorf("KnownProviderIDs missing %q", id)
		}
	}
	if ids["nonexistent"] {
		t.Error("KnownProviderIDs should not contain 'nonexistent'")
	}
}

func TestAnnotateConfigured(t *testing.T) {
	schemas := AllProviderSchemas()

	// Only openrouter and ollama registered.
	result := AnnotateConfigured(schemas, []string{"openrouter", "ollama"})

	for _, s := range result {
		switch s.ID {
		case "cli":
			if !s.Configured {
				t.Error("cli should always be configured")
			}
		case "openrouter", "ollama":
			if !s.Configured {
				t.Errorf("%s should be configured", s.ID)
			}
		default:
			if s.Configured {
				t.Errorf("%s should NOT be configured", s.ID)
			}
		}
	}
}

func TestAnnotateConfigured_EmptyRegistered(t *testing.T) {
	schemas := AllProviderSchemas()
	result := AnnotateConfigured(schemas, nil)

	for _, s := range result {
		if s.ID == "cli" {
			if !s.Configured {
				t.Error("cli should always be configured")
			}
		} else if s.Configured {
			t.Errorf("%s should not be configured with empty registered list", s.ID)
		}
	}
}

func TestAnnotateConfigured_DoesNotMutateInput(t *testing.T) {
	schemas := AllProviderSchemas()
	_ = AnnotateConfigured(schemas, []string{"openrouter"})

	// Original should not be mutated.
	for _, s := range schemas {
		if s.Configured {
			t.Errorf("original schema %q was mutated", s.ID)
		}
	}
}
