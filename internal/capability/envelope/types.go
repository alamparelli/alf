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
	HTTP       HTTPBlock
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

// HTTPBlock captures `[[http.scopes]]` per §3.4 of MANIFEST-SCHEMA and
// #421. Empty Scopes means "no outbound HTTP authority" — the forge
// does not mint an http.Handle and the WASM host import alf_http_request
// (Wave 2) fails CheckImports with "extra grants without consumer".
//
// Per §7.3, declaring `[[http.scopes]]` exceeds the local-daemon-key
// (Tier 2) ceiling: the operator must sign with the user-endorsed key
// (Tier 3) via `alf keygen` + `alf sign`. EnforceTier2Ceiling refuses
// to sign manifests with a non-empty Scopes slice.
type HTTPBlock struct {
	Scopes []HTTPScope
}

// HTTPScope is a single entry in [[http.scopes]] — one URL pattern the
// capability is authorised to reach. The shape is intentionally narrow:
//
//   - Host is the exact match target. Lowercase (normalised at parse),
//     DNS-shape labels (letters, digits, hyphens), optional ":port"
//     suffix (port 1..65535). No wildcards, no globs, no regex.
//   - PathPrefix is an optional literal prefix. Empty means "any path
//     under this host". When set, must start with "/" and contain no
//     glob/regex meta characters. Matching is segment-aware at the
//     forge layer ("/books/v1" matches "/books/v1" and "/books/v1/X"
//     but NOT "/books/v10" — defeats the prefix-collision footgun).
//
// Wave 1 (this commit) validates the shape at parse time. Wave 2 wires
// alf_http_request through VAULT_PROXY_SOCK. Wave 3 mints the scoped
// http.Handle at forge time. Wave 4 migrates real apps and re-runs the
// soak from #416.
type HTTPScope struct {
	Host       string
	PathPrefix string
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
	HTTP       tomlHTTPBlock      `toml:"http"`
	Events     tomlEventsBlock    `toml:"events"`
	Tools      tomlToolsBlock     `toml:"tools"`
	Provider   tomlProviderBlock  `toml:"provider"`
	Depends    []tomlDependsEntry `toml:"depends"`
	RawImports []tomlRawImport    `toml:"raw_imports"`

	// Deferred blocks — presence is a validation error. We decode them
	// to detect presence only; contents are ignored.
	Exec    *map[string]any `toml:"exec"`
	Secrets *map[string]any `toml:"secrets"`
	Memory  *map[string]any `toml:"memory"`
}

type tomlHTTPBlock struct {
	Scopes []tomlHTTPScope `toml:"scopes"`
}

type tomlHTTPScope struct {
	Host       string `toml:"host"`
	PathPrefix string `toml:"path_prefix"`
}

type tomlProviderBlock struct {
	Exports []tomlProviderExport `toml:"exports"`
}

type tomlProviderExport struct {
	ID          string            `toml:"id"`
	ScopeFields []tomlScopeField  `toml:"scope_fields"`
}

type tomlScopeField struct {
	Name     string `toml:"name"`
	Type     string `toml:"type"`
	Required bool   `toml:"required"`
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
// time. ScopeFields declares the typed fields a consumer's
// `[[depends]].scope` table is allowed to carry — empty means the
// handle takes no scope (e.g. `bluetooth.scan` may not need any).
//
// #392 Stage 4 ships ScopeFields. The full JSON Schema reference
// (`schema_ref`) variant noted in the original ticket is replaced by
// this typed-field-list form. Rationale: complex schemas (nested
// objects, conditionals, regex patterns) are 80% over-engineering
// for the actual use cases. A flat field-list with five primitive
// types covers Bluetooth devices, GPU device names, IoT topic IDs,
// and the rest of the §392 scope catalogue. M8 audit finding holds:
// validation is Runtime-side (resolveDepends drives it) so a buggy
// provider implementation cannot accept broader input than declared.
type ProviderExport struct {
	ID          string
	ScopeFields []ScopeField
}

// ScopeFieldType is the closed set of types a [[depends]].scope field
// may carry under #392 Stage 4. Each name maps to a TOML/JSON value
// shape the runtime can verify without delegating to a JSON Schema
// validator.
type ScopeFieldType string

const (
	ScopeFieldTypeString     ScopeFieldType = "string"
	ScopeFieldTypeInt        ScopeFieldType = "int"
	ScopeFieldTypeBool       ScopeFieldType = "bool"
	ScopeFieldTypeStringList ScopeFieldType = "string-list"
	ScopeFieldTypeIntList    ScopeFieldType = "int-list"
)

// ScopeField is one entry in `[[provider.exports]].scope_fields`.
// Name is the TOML key the consumer uses in `[[depends]].scope`;
// Type is one of the closed enum above; Required toggles whether
// the consumer manifest must declare the field. Stage 4 has no
// default-value support — a missing optional field stays absent
// at the consumer side.
type ScopeField struct {
	Name     string
	Type     ScopeFieldType
	Required bool
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

// SplitHandle splits Handle into (namespace, id). Pre-condition: this
// DependsEntry came from Validate, which already enforced the
// `<ns>:<id>` format via dependsHandlePattern — so the split always
// returns two non-empty parts. Callers in the runtime (forge,
// registry resolver) skip the format check; envelope.Validate is the
// authoritative gate.
func (d DependsEntry) SplitHandle() (namespace, id string) {
	for i := 0; i < len(d.Handle); i++ {
		if d.Handle[i] == ':' {
			return d.Handle[:i], d.Handle[i+1:]
		}
	}
	// Unreachable for any DependsEntry from Validate; defensive.
	return d.Handle, ""
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
