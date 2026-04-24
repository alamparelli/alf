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
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

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
	if t.Events != nil {
		return nil, fmt.Errorf("%w: events (lands under #399)", ErrBlockDeferred)
	}
	if t.Tools != nil {
		return nil, fmt.Errorf("%w: tools (lands under #389)", ErrBlockDeferred)
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

	return &Manifest{
		EnvelopeVersion: *t.AlfEnvelopeVersion,
		ID:              t.ID,
		Kind:            kind,
		Version:         t.Version,
		Name:            t.Name,
		Description:     t.Description,
		FS:              fs,
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
