package envelope

// Manifest is the typed projection of a MANIFEST-SCHEMA-compliant
// manifest.toml. It carries what the daemon needs post-validation:
// envelope version, core identity, and the handle-scope permissions
// for 0.8.0 (fs only; other blocks are parse-time errors per §3.4).
//
// Two notes on design:
//  1. This struct is INTERNAL to the verifier's typed pipeline. The
//     existing internal/capability.Manifest continues to describe the
//     runtime-facing contract (legacy FilePaths/Networks/Secrets). A
//     later migration (#382 + post-ocap) reconciles the two once the
//     runtime consumes handle.Grants directly from envelope.Manifest.
//  2. Fields use pointers or explicit empty values so the validator can
//     distinguish "absent" from "present but zero" — required for the
//     MANIFEST-SCHEMA rule that absent optional fields map to explicit
//     null in canonical form.
type Manifest struct {
	EnvelopeVersion int
	ID              string
	Kind            ManifestKind
	Version         string
	Name            string
	Description     string

	FS         FSBlock
	Events     EventsBlock
	Tools      ToolsBlock
	Provider   ProviderBlock // only valid when Kind == KindCapabilityProvider
	Depends    []DependsEntry
	RawImports []RawImport
}

// ManifestKind enumerates the capability kinds recognised by the 0.8.0
// envelope. Ordered to match MANIFEST-SCHEMA §3.3 narrative flow.
//
// #392 split: the legacy "provider" kind covered LLM-backend bundles only.
// Capability providers (Tier 2 of #392 — new authority surfaces like
// Bluetooth, GPU compute, custom IoT) are a different concept; they SIGN
// new handle types into the runtime registry rather than provide an LLM
// API. Conflating them under one kind would force the schema to disambiguate
// at runtime via the [provider] block's content, which is the exact
// "schema-tells-you-what-it-means-via-content" anti-pattern §3.3 of
// MANIFEST-SCHEMA forbids. Solution: rename the LLM-backend kind to
// "llm-provider" and add "capability-provider". No production manifest
// used "provider" — the rename is a clean break, no migration burden.
type ManifestKind string

const (
	KindWASMTool            ManifestKind = "wasm-tool"
	KindWASMApp             ManifestKind = "wasm-app"
	KindSkill               ManifestKind = "skill"
	KindLLMProvider         ManifestKind = "llm-provider"         // LLM backend (Anthropic, OpenAI, ...)
	KindCapabilityProvider  ManifestKind = "capability-provider"  // §3.1 + #392 — exports new handle kinds
	KindMarketplaceApp      ManifestKind = "marketplace-app"      // deprecated; see §3.3
)

// FSBlock captures the [[fs.reads]] / [[fs.writes]] arrays. Empty slices
// mean "no paths declared", which in turn means "handle is nil in
// Grants" — the cap has no filesystem access.
type FSBlock struct {
	Reads  []FSPath
	Writes []FSPath
}

// FSPath is a single entry in an fs.reads / fs.writes array. The Path
// is relative to the bundle root, trailing "/" meaning directory prefix
// match (consistent with internal/capability/handle/fs.go's semantics).
type FSPath struct {
	Path string
}

// EventsBlock captures the [[events.exports]] / [[events.subscribes]]
// arrays per §3.3 of ARCHITECTURE-SECURITY.md. Empty slices mean "no
// publish/subscribe authority" — the cap gets no event handle in
// Grants.
type EventsBlock struct {
	Exports    []EventExport
	Subscribes []EventSubscription
}

// EventExport declares one topic this capability is authorised to
// publish on. Other capabilities may subscribe by referencing this
// (publisher-id, topic) pair in their own events.subscribes.
type EventExport struct {
	Topic string
}

// EventSubscription declares one cross-flow this capability requests:
// receive events from publisher From on topic Topic. The Runtime forge
// only materialises an EventSub handle when the named publisher is
// installed AND its signed manifest declares the topic in its
// events.exports.
type EventSubscription struct {
	From  string
	Topic string
}

// ToolsBlock captures [[tools.declares]] per ARCHITECTURE-SECURITY §3.1
// + #389. Each declaration names another capability ID this capability
// is authorised to invoke. The forge materialises a single ToolHandle
// scoped to the listed IDs; tools not in declares are invisible to the
// LLM tool surface (not blocked, absent — see #389 acceptance criteria).
type ToolsBlock struct {
	Declares []ToolDeclaration
}

// ToolDeclaration is one entry in [[tools.declares]]. ID matches the
// idPattern regex (lowercase, digits, hyphens) — same shape as a
// capability id elsewhere in the schema.
type ToolDeclaration struct {
	ID string
}

// tomlManifest is the raw TOML-decoded shape. Field tags drive the
// pelletier/go-toml/v2 unmarshal. Separate from Manifest so we can
// detect unknown fields by unmarshalling into a strict struct then
// comparing against the generic parse output — see Validate.
type tomlManifest struct {
	AlfEnvelopeVersion *int    `toml:"alf_envelope_version"`
	ID                 string  `toml:"id"`
	Kind               string  `toml:"kind"`
	Version            string  `toml:"version"`
	Name               string  `toml:"name"`
	Description        string  `toml:"description"`

	FS         tomlFSBlock        `toml:"fs"`
	Events     tomlEventsBlock    `toml:"events"`
	Tools      tomlToolsBlock     `toml:"tools"`
	Provider   tomlProviderBlock  `toml:"provider"`
	Depends    []tomlDependsEntry `toml:"depends"`
	RawImports []tomlRawImport    `toml:"raw_imports"`

	// Deferred blocks — presence is a validation error. We decode them
	// to detect presence only; contents are ignored.
	HTTP    *map[string]any `toml:"http"`
	Exec    *map[string]any `toml:"exec"`
	Secrets *map[string]any `toml:"secrets"`
	Memory  *map[string]any `toml:"memory"`
}

type tomlProviderBlock struct {
	Exports []tomlProviderExport `toml:"exports"`
}

type tomlProviderExport struct {
	ID string `toml:"id"`
}

type tomlDependsEntry struct {
	Handle string         `toml:"handle"`
	Scope  map[string]any `toml:"scope"`
}

type tomlRawImport struct {
	Module        string `toml:"module"`
	Function      string `toml:"function"`
	Justification string `toml:"justification"`
}

type tomlFSBlock struct {
	Reads  []tomlFSPath `toml:"reads"`
	Writes []tomlFSPath `toml:"writes"`
}

type tomlFSPath struct {
	Path string `toml:"path"`
}

type tomlEventsBlock struct {
	Exports    []tomlEventExport       `toml:"exports"`
	Subscribes []tomlEventSubscription `toml:"subscribes"`
}

type tomlEventExport struct {
	Topic string `toml:"topic"`
}

type tomlEventSubscription struct {
	From  string `toml:"from"`
	Topic string `toml:"topic"`
}

type tomlToolsBlock struct {
	Declares []tomlToolDeclaration `toml:"declares"`
}

type tomlToolDeclaration struct {
	ID string `toml:"id"`
}

// ProviderBlock captures `[provider]` per §3.4 of MANIFEST-SCHEMA + #392.
// Only valid when Kind == KindCapabilityProvider. The block declares the
// new handle kinds this provider exports into the runtime registry; once
// installed under a fingerprint-scoped namespace, downstream caps may
// `[[depends]]` on `<ns>:<id>` to receive a forged handle of that kind.
//
// Stage 1 (this commit) ships the schema and validation. Registry +
// forge wiring lands in subsequent stages of #392.
type ProviderBlock struct {
	Exports []ProviderExport
}

// ProviderExport is one entry in [[provider.exports]]. The ID is the
// handle kind name (lowercase, dot-segmented — e.g. "bluetooth.scan",
// "gpu.compute"); namespace + fingerprint scoping is applied at install
// time. SchemaRef (the JSON Schema URL or inline schema for the scope
// argument) is deferred to Stage 4 of #392 — Stage 1 ships id-only.
type ProviderExport struct {
	ID string
}

// DependsEntry captures one `[[depends]]` entry per #392. Handle is the
// fully-qualified `<ns>:<id>` reference: `alf:` (reserved for core
// kinds — fs, http, exec, secrets, events, tools) or a known-publisher
// fingerprint short form (Stage 3 will validate against the trust
// store; Stage 1 ships format check only). Scope is the opaque table the
// consumer asks for — Runtime-side validation against the provider's
// exported scope schema is Stage 4 (M8 audit finding).
type DependsEntry struct {
	Handle string
	Scope  map[string]any
}

// RawImport captures one `[[raw_imports]]` entry per #392. Module +
// Function name a WASI function the guest needs to import directly
// (escape hatch — see §3 of #392). The Justification is required so
// the install UX can surface a human-readable rationale to the operator
// before approving raw access.
//
// Forbidden modules (§4.5 of #392 — must use a scoped handle instead)
// are rejected at validate time with ErrRawImportForbidden. Allowed
// modules pass validation; Stage 4 of #392 wires them through
// CheckImports so the guest can actually link the symbols.
type RawImport struct {
	Module        string
	Function      string
	Justification string
}
