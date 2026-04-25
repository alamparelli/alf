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

	FS     FSBlock
	Events EventsBlock
}

// ManifestKind enumerates the capability kinds recognised by the 0.8.0
// envelope. Ordered to match MANIFEST-SCHEMA §3.3 narrative flow.
type ManifestKind string

const (
	KindWASMTool        ManifestKind = "wasm-tool"
	KindWASMApp         ManifestKind = "wasm-app"
	KindSkill           ManifestKind = "skill"
	KindProvider        ManifestKind = "provider"
	KindMarketplaceApp  ManifestKind = "marketplace-app" // deprecated; see §3.3
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

	FS     tomlFSBlock     `toml:"fs"`
	Events tomlEventsBlock `toml:"events"`

	// Deferred blocks — presence is a validation error. We decode them
	// to detect presence only; contents are ignored.
	HTTP    *map[string]any `toml:"http"`
	Exec    *map[string]any `toml:"exec"`
	Secrets *map[string]any `toml:"secrets"`
	Tools   *map[string]any `toml:"tools"`
	Memory  *map[string]any `toml:"memory"`
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
