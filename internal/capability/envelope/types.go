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

	FS FSBlock
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

	FS tomlFSBlock `toml:"fs"`

	// Deferred blocks — presence is a validation error. We decode them
	// to detect presence only; contents are ignored.
	HTTP     *map[string]any `toml:"http"`
	Exec     *map[string]any `toml:"exec"`
	Secrets  *map[string]any `toml:"secrets"`
	Events   *map[string]any `toml:"events"`
	Tools    *map[string]any `toml:"tools"`
	Memory   *map[string]any `toml:"memory"`
}

type tomlFSBlock struct {
	Reads  []tomlFSPath `toml:"reads"`
	Writes []tomlFSPath `toml:"writes"`
}

type tomlFSPath struct {
	Path string `toml:"path"`
}
