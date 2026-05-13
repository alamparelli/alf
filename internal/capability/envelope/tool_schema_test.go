package envelope

import (
	"errors"
	"testing"
)

// TestValidate_ToolSchema_HappyPath_WasmTool pins #423: a wasm-tool
// manifest may declare [tool.schema] with description + parameters.
// The parsed Manifest exposes Tool.Schema with both fields populated.
func TestValidate_ToolSchema_HappyPath_WasmTool(t *testing.T) {
	input := validManifest() + `
[tool.schema]
description = "Fetch a URL via the WASM http handle."

[tool.schema.parameters]
type = "object"
required = ["url"]

[tool.schema.parameters.properties]
[tool.schema.parameters.properties.url]
type = "string"
description = "URL to fetch (must match http.scopes)"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Tool.Schema == nil {
		t.Fatal("Tool.Schema is nil")
	}
	if m.Tool.Schema.Description != "Fetch a URL via the WASM http handle." {
		t.Errorf("Description=%q", m.Tool.Schema.Description)
	}
	if t2, ok := m.Tool.Schema.Parameters["type"].(string); !ok || t2 != "object" {
		t.Errorf("Parameters.type=%v, want object", m.Tool.Schema.Parameters["type"])
	}
}

// TestValidate_ToolSchema_HappyPath_Skill pins that the [tool.schema]
// block is also accepted on `kind = "skill"`. Skills already declare
// their tools via [[tools.declares]]; the schema describes the skill's
// own LLM surface.
func TestValidate_ToolSchema_HappyPath_Skill(t *testing.T) {
	input := `alf_envelope_version = 1
id      = "my-skill"
kind    = "skill"
version = "0.1.0"
name    = "My Skill"

[tool.schema]
description = "Run the skill"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Tool.Schema == nil {
		t.Fatal("Tool.Schema is nil")
	}
	if m.Tool.Schema.Description != "Run the skill" {
		t.Errorf("Description=%q", m.Tool.Schema.Description)
	}
}

// TestValidate_ToolSchema_NoParametersIsOK pins the documented "takes
// no input" pattern: a tool that does not declare parameters has nil
// Parameters in the parsed schema. Still surfaced to the LLM with an
// empty input schema.
func TestValidate_ToolSchema_NoParametersIsOK(t *testing.T) {
	input := validManifest() + `
[tool.schema]
description = "A no-input tool"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Tool.Schema == nil {
		t.Fatal("Tool.Schema is nil")
	}
	if m.Tool.Schema.Parameters != nil {
		t.Errorf("Parameters=%v, want nil", m.Tool.Schema.Parameters)
	}
}

// TestValidate_ToolSchema_AbsentIsOK pins that omitting the block
// entirely is valid for any kind. The bundle is then capability-only
// (loaded into capRegistry but invisible to tooling.Registry / LLM
// surface).
func TestValidate_ToolSchema_AbsentIsOK(t *testing.T) {
	m, err := Validate([]byte(validManifest()))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.Tool.Schema != nil {
		t.Errorf("Tool.Schema should be nil when block is absent")
	}
}

// TestValidate_ToolSchema_RejectedOnWasmApp pins the kind gate: the
// LLM tool surface only applies to wasm-tool and skill kinds. A
// wasm-app declaring [tool.schema] is a programmer error — the bundle
// is consumed via the CC UI, not the chat engine.
func TestValidate_ToolSchema_RejectedOnWasmApp(t *testing.T) {
	input := `alf_envelope_version = 1
id      = "my-app"
kind    = "wasm-app"
version = "0.1.0"
name    = "My App"

[tool.schema]
description = "should be rejected"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrToolSchemaNotAllowedHere) {
		t.Errorf("got %v, want ErrToolSchemaNotAllowedHere", err)
	}
}

// TestValidate_ToolSchema_RejectedOnCapabilityProvider pins the gate
// for capability-provider kind too — providers export handle kinds,
// they are not LLM-callable directly.
func TestValidate_ToolSchema_RejectedOnCapabilityProvider(t *testing.T) {
	input := `alf_envelope_version = 1
id      = "my-provider"
kind    = "capability-provider"
version = "0.1.0"
name    = "My Provider"

[tool.schema]
description = "should be rejected"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrToolSchemaNotAllowedHere) {
		t.Errorf("got %v, want ErrToolSchemaNotAllowedHere", err)
	}
}

// TestValidate_ToolSchema_EmptyDescriptionRejected pins that an
// empty description is a parse error. Operators see the description
// at install + the LLM sees it as part of the tool definition — both
// rely on it being meaningful.
func TestValidate_ToolSchema_EmptyDescriptionRejected(t *testing.T) {
	input := validManifest() + `
[tool.schema]
description = ""
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrToolSchemaDescriptionEmpty) {
		t.Errorf("got %v, want ErrToolSchemaDescriptionEmpty", err)
	}
}

// TestValidate_ToolSchema_WhitespaceDescriptionRejected pins the
// trimming behaviour: an all-whitespace description equals empty.
func TestValidate_ToolSchema_WhitespaceDescriptionRejected(t *testing.T) {
	input := validManifest() + `
[tool.schema]
description = "   "
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrToolSchemaDescriptionEmpty) {
		t.Errorf("got %v, want ErrToolSchemaDescriptionEmpty", err)
	}
}

// TestValidate_ToolSchema_ParametersWrongTypeRejected pins that
// parameters must declare top-level "type": "object" per the
// OpenAI tool-schema contract. A "type": "string" parameters block
// is rejected.
func TestValidate_ToolSchema_ParametersWrongTypeRejected(t *testing.T) {
	input := validManifest() + `
[tool.schema]
description = "Has wrong-type parameters"

[tool.schema.parameters]
type = "string"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrToolSchemaParametersInvalid) {
		t.Errorf("got %v, want ErrToolSchemaParametersInvalid", err)
	}
}

// TestValidate_ToolSchema_ParametersMissingTypeRejected pins that
// the top-level "type" field is required when parameters is set.
func TestValidate_ToolSchema_ParametersMissingTypeRejected(t *testing.T) {
	input := validManifest() + `
[tool.schema]
description = "Missing parameters.type"

[tool.schema.parameters.properties]
[tool.schema.parameters.properties.x]
type = "string"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrToolSchemaParametersInvalid) {
		t.Errorf("got %v, want ErrToolSchemaParametersInvalid", err)
	}
}
