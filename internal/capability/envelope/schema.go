package envelope

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// EnvelopeVersion0_8_0 is the manifest schema version shipped in 0.8.0.
// §7.10.4 pins this as the only recognised version; earlier or later
// versions are rejected so a rolling upgrade cannot silently accept a
// manifest built against a different semantic.
const EnvelopeVersion0_8_0 = 1

// Typed error sentinels. Every Validate failure maps to one of these so
// callers (and the test vector table in §7.10.7) can pattern-match.
var (
	ErrEnvelopeVersionMissing     = errors.New("envelope: manifest missing alf_envelope_version")
	ErrEnvelopeVersionUnsupported = errors.New("envelope: unsupported alf_envelope_version")
	ErrIDMissing                  = errors.New("envelope: manifest.id is required")
	ErrIDMalformed                = errors.New("envelope: manifest.id must match ^[a-z0-9][a-z0-9-]*$")
	ErrKindMissing                = errors.New("envelope: manifest.kind is required")
	ErrKindUnknown                = errors.New("envelope: manifest.kind is not a recognised value")
	ErrVersionMissing             = errors.New("envelope: manifest.version is required")
	ErrNameMissing                = errors.New("envelope: manifest.name is required")
	ErrUnknownField               = errors.New("envelope: manifest contains an unknown field")
	ErrBlockDeferred              = errors.New("envelope: manifest declares a block that is not wired in 0.8.0")
	ErrFSPathEmpty                = errors.New("envelope: fs path is empty")
	ErrFSPathAbsolute             = errors.New("envelope: fs path must be relative to bundle root")
	ErrFSPathTraversal            = errors.New("envelope: fs path contains '..' segment")
	ErrEventTopicEmpty            = errors.New("envelope: events topic is empty")
	ErrEventTopicMalformed        = errors.New("envelope: events topic must match ^[a-z0-9][a-z0-9._-]*$")
	ErrEventSubscribeFromEmpty    = errors.New("envelope: events.subscribes.from is empty")
	ErrEventSubscribeFromMalformed = errors.New("envelope: events.subscribes.from must match ^[a-z0-9][a-z0-9-]*$")
	ErrToolDeclareIDEmpty          = errors.New("envelope: tools.declares.id is empty")
	ErrToolDeclareIDMalformed      = errors.New("envelope: tools.declares.id must match ^[a-z0-9][a-z0-9-]*$")
	ErrToolDeclareDuplicate        = errors.New("envelope: tools.declares contains duplicate id")
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// topicPattern allows lowercase, digits, dot/underscore/hyphen — matches
// common topic-name conventions (e.g. "chat.log", "email.new", "task_done").
// Wildcards / glob patterns are intentionally NOT permitted in 0.8.0
// per the §3.3 promise of explicit cross-flows.
var topicPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// knownKinds is the enum of accepted manifest.kind values (MANIFEST-SCHEMA §3.3).
var knownKinds = map[string]ManifestKind{
	"wasm-tool":       KindWASMTool,
	"wasm-app":        KindWASMApp,
	"skill":           KindSkill,
	"provider":        KindProvider,
	"marketplace-app": KindMarketplaceApp,
}

// knownTopLevelKeys is the allowlist for unknown-field detection. Any
// top-level TOML key outside this set fails validation with
// ErrUnknownField — a new field is a deliberate schema change that
// bumps the envelope version.
var knownTopLevelKeys = map[string]struct{}{
	"alf_envelope_version": {},
	"id":                   {},
	"kind":                 {},
	"version":              {},
	"name":                 {},
	"description":          {},
	"fs":                   {},
	// Deferred blocks — recognised so we can produce a helpful error,
	// but their presence fails validation.
	"http":    {},
	"exec":    {},
	"secrets": {},
	"events":  {},
	"tools":   {},
	"memory":  {},
}

// Validate parses a manifest.toml byte sequence and returns a typed
// Manifest iff every MANIFEST-SCHEMA §3 rule holds. First failure wins
// — the caller only sees the earliest error so messages stay focused.
//
// Validate is separate from Canonicalize on purpose: the verify path
// (capability.Verify, step 5) calls both — Validate to reject invalid
// manifests, Canonicalize to derive the signed bytes. A manifest that
// fails validation is rejected before any cryptographic step.
func Validate(tomlBytes []byte) (*Manifest, error) {
	// Unknown-field detection: parse into a generic map first, check
	// every top-level key against the allowlist. The strict decoder
	// below would fail on unknowns but loses fidelity about WHICH key
	// was unknown.
	var raw map[string]any
	if err := toml.Unmarshal(tomlBytes, &raw); err != nil {
		return nil, fmt.Errorf("envelope: parse TOML: %w", err)
	}
	for k := range raw {
		if _, ok := knownTopLevelKeys[k]; !ok {
			return nil, fmt.Errorf("%w: %q (bump alf_envelope_version to add fields)", ErrUnknownField, k)
		}
	}

	// Typed decode for field-level validation.
	var t tomlManifest
	if err := toml.Unmarshal(tomlBytes, &t); err != nil {
		return nil, fmt.Errorf("envelope: typed parse: %w", err)
	}

	// Deferred blocks are a parse-time error in 0.8.0. The presence of
	// the top-level key alone is enough — we don't care what's inside.
	if t.HTTP != nil {
		return nil, fmt.Errorf("%w: http (lands in 0.9.0+ alongside http.Handle)", ErrBlockDeferred)
	}
	if t.Exec != nil {
		return nil, fmt.Errorf("%w: exec (lands in 0.9.0+ alongside exec.Handle)", ErrBlockDeferred)
	}
	if t.Secrets != nil {
		return nil, fmt.Errorf("%w: secrets (lands in 0.9.0+ alongside secrets.Handle)", ErrBlockDeferred)
	}
	if t.Memory != nil {
		return nil, fmt.Errorf("%w: memory (lands under #400)", ErrBlockDeferred)
	}

	// Envelope version — required, must equal the 0.8.0 pin.
	if t.AlfEnvelopeVersion == nil {
		return nil, ErrEnvelopeVersionMissing
	}
	if *t.AlfEnvelopeVersion != EnvelopeVersion0_8_0 {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrEnvelopeVersionUnsupported, *t.AlfEnvelopeVersion, EnvelopeVersion0_8_0)
	}

	// Core identity.
	if t.ID == "" {
		return nil, ErrIDMissing
	}
	if !idPattern.MatchString(t.ID) {
		return nil, fmt.Errorf("%w: got %q", ErrIDMalformed, t.ID)
	}
	if t.Kind == "" {
		return nil, ErrKindMissing
	}
	kind, ok := knownKinds[t.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: got %q", ErrKindUnknown, t.Kind)
	}
	if t.Version == "" {
		return nil, ErrVersionMissing
	}
	if t.Name == "" {
		return nil, ErrNameMissing
	}

	// fs paths.
	fs, err := validateFSBlock(t.FS)
	if err != nil {
		return nil, err
	}

	// events block (#399).
	events, err := validateEventsBlock(t.Events)
	if err != nil {
		return nil, err
	}

	// tools block (#389).
	tools, err := validateToolsBlock(t.Tools)
	if err != nil {
		return nil, err
	}

	return &Manifest{
		EnvelopeVersion: *t.AlfEnvelopeVersion,
		ID:              t.ID,
		Kind:            kind,
		Version:         t.Version,
		Name:            t.Name,
		Description:     t.Description,
		FS:              fs,
		Events:          events,
		Tools:           tools,
	}, nil
}

func validateFSBlock(raw tomlFSBlock) (FSBlock, error) {
	reads, err := validateFSPaths(raw.Reads, "fs.reads")
	if err != nil {
		return FSBlock{}, err
	}
	writes, err := validateFSPaths(raw.Writes, "fs.writes")
	if err != nil {
		return FSBlock{}, err
	}
	return FSBlock{Reads: reads, Writes: writes}, nil
}

// validateEventsBlock walks events.exports + events.subscribes and
// rejects malformed entries. Per §3.3, both sides of a cross-flow are
// declarative and must reference well-formed identifiers; the runtime
// loader's two-pass cross-flow resolver enforces that the named
// publisher exists, so this validation is purely format-level.
func validateEventsBlock(raw tomlEventsBlock) (EventsBlock, error) {
	exports := make([]EventExport, 0, len(raw.Exports))
	for i, e := range raw.Exports {
		if e.Topic == "" {
			return EventsBlock{}, fmt.Errorf("%w: events.exports[%d]", ErrEventTopicEmpty, i)
		}
		if !topicPattern.MatchString(e.Topic) {
			return EventsBlock{}, fmt.Errorf("%w: events.exports[%d].topic=%q", ErrEventTopicMalformed, i, e.Topic)
		}
		exports = append(exports, EventExport{Topic: e.Topic})
	}

	subs := make([]EventSubscription, 0, len(raw.Subscribes))
	for i, s := range raw.Subscribes {
		if s.From == "" {
			return EventsBlock{}, fmt.Errorf("%w: events.subscribes[%d]", ErrEventSubscribeFromEmpty, i)
		}
		if !idPattern.MatchString(s.From) {
			return EventsBlock{}, fmt.Errorf("%w: events.subscribes[%d].from=%q", ErrEventSubscribeFromMalformed, i, s.From)
		}
		if s.Topic == "" {
			return EventsBlock{}, fmt.Errorf("%w: events.subscribes[%d]", ErrEventTopicEmpty, i)
		}
		if !topicPattern.MatchString(s.Topic) {
			return EventsBlock{}, fmt.Errorf("%w: events.subscribes[%d].topic=%q", ErrEventTopicMalformed, i, s.Topic)
		}
		subs = append(subs, EventSubscription{From: s.From, Topic: s.Topic})
	}
	return EventsBlock{Exports: exports, Subscribes: subs}, nil
}

// validateToolsBlock walks tools.declares and rejects malformed entries.
// Per ARCHITECTURE-SECURITY §3.1 + #389, each declared id is the
// capability ID of another tool the holder is authorised to invoke.
// Exact match only — wildcards are intentionally not supported so the
// install-time UI can surface every coupling and revocation can cascade.
func validateToolsBlock(raw tomlToolsBlock) (ToolsBlock, error) {
	if len(raw.Declares) == 0 {
		return ToolsBlock{}, nil
	}
	out := make([]ToolDeclaration, 0, len(raw.Declares))
	seen := make(map[string]struct{}, len(raw.Declares))
	for i, d := range raw.Declares {
		if d.ID == "" {
			return ToolsBlock{}, fmt.Errorf("%w: tools.declares[%d]", ErrToolDeclareIDEmpty, i)
		}
		if !idPattern.MatchString(d.ID) {
			return ToolsBlock{}, fmt.Errorf("%w: tools.declares[%d].id=%q", ErrToolDeclareIDMalformed, i, d.ID)
		}
		if _, dup := seen[d.ID]; dup {
			return ToolsBlock{}, fmt.Errorf("%w: tools.declares[%d].id=%q", ErrToolDeclareDuplicate, i, d.ID)
		}
		seen[d.ID] = struct{}{}
		out = append(out, ToolDeclaration{ID: d.ID})
	}
	return ToolsBlock{Declares: out}, nil
}

func validateFSPaths(raw []tomlFSPath, block string) ([]FSPath, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]FSPath, 0, len(raw))
	for i, p := range raw {
		if p.Path == "" {
			return nil, fmt.Errorf("%w: %s[%d]", ErrFSPathEmpty, block, i)
		}
		if strings.HasPrefix(p.Path, "/") {
			return nil, fmt.Errorf("%w: %s[%d]=%q", ErrFSPathAbsolute, block, i, p.Path)
		}
		// Reject any ".." segment anywhere in the path. TOML encoding
		// doesn't normalise, so we do it here.
		for _, seg := range strings.Split(p.Path, "/") {
			if seg == ".." {
				return nil, fmt.Errorf("%w: %s[%d]=%q", ErrFSPathTraversal, block, i, p.Path)
			}
		}
		out = append(out, FSPath{Path: p.Path})
	}
	return out, nil
}
