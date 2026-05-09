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

	// #392 Stage 1 — provider / depends / raw_imports.
	ErrProviderBlockNotAllowedHere = errors.New("envelope: [provider] block requires kind = \"capability-provider\"")
	ErrProviderExportIDEmpty       = errors.New("envelope: provider.exports.id is empty")
	ErrProviderExportIDMalformed   = errors.New("envelope: provider.exports.id must match ^[a-z0-9][a-z0-9.-]*$")
	ErrProviderExportDuplicate     = errors.New("envelope: provider.exports contains duplicate id")

	// #392 Stage 4 — scope schema declared on each provider export.
	ErrScopeFieldNameEmpty     = errors.New("envelope: provider.exports.scope_fields.name is empty")
	ErrScopeFieldNameMalformed = errors.New("envelope: provider.exports.scope_fields.name must match ^[a-z][a-z0-9_]*$")
	ErrScopeFieldTypeEmpty     = errors.New("envelope: provider.exports.scope_fields.type is empty")
	ErrScopeFieldTypeUnknown   = errors.New("envelope: provider.exports.scope_fields.type is not in the closed enum (string|int|bool|string-list|int-list)")
	ErrScopeFieldDuplicate     = errors.New("envelope: provider.exports.scope_fields contains duplicate name")
	ErrDependsHandleEmpty          = errors.New("envelope: depends.handle is empty")
	ErrDependsHandleMalformed      = errors.New("envelope: depends.handle must match ^<ns>:<id>$ where <ns> is [a-z0-9][a-z0-9-]* and <id> is [a-z0-9][a-z0-9.-]*")
	ErrDependsHandleNamespaceReserved = errors.New("envelope: depends.handle uses reserved namespace alf: but the id is not a known core handle kind")
	ErrDependsDuplicate            = errors.New("envelope: depends contains duplicate handle reference")
	ErrRawImportModuleEmpty        = errors.New("envelope: raw_imports.module is empty")
	ErrRawImportModuleMalformed    = errors.New("envelope: raw_imports.module must match ^wasi:[a-z0-9][a-z0-9/_-]*$")
	ErrRawImportFunctionEmpty      = errors.New("envelope: raw_imports.function is empty")
	ErrRawImportFunctionMalformed  = errors.New("envelope: raw_imports.function must match ^[a-z0-9][a-z0-9_-]*$")
	ErrRawImportJustificationEmpty = errors.New("envelope: raw_imports.justification is empty (operators see this string at install)")
	ErrRawImportForbidden          = errors.New("envelope: raw_imports module is forbidden — use a scoped handle instead")
	ErrRawImportNotInAllowlist     = errors.New("envelope: raw_imports module is not in the §3.4 allowlist")
)

// reservedNamespaceALF is the namespace prefix that only the daemon may
// claim. Capability providers cannot export a handle ID under "alf:";
// the runtime registry pre-populates "alf:fs", "alf:http", "alf:exec",
// "alf:secrets", "alf:events.pub", "alf:events.sub", "alf:tool".
//
// Stage 1 of #392 only checks that NO provider manifest tries to export
// under this namespace (provider exports use bare ids, namespacing is
// applied at install time). Consumer-side `[[depends]]` validation
// against the registry of known core handles lands in Stage 3 (forge
// integration); Stage 1 only checks the format `alf:<id>` and a
// closed allowlist of known core handle ids.
const reservedNamespaceALF = "alf"

// coreHandleIDs is the closed set of handle kinds the daemon ships with
// under the `alf:` namespace. Stage 1 of #392 uses this for a static
// allowlist check on `[[depends]].handle = "alf:<id>"`. Stage 3 will
// promote this to a runtime-populated registry once HandleRegistry
// (deliverable 1 of #392) lands; the strings stay the canonical names.
var coreHandleIDs = map[string]struct{}{
	"fs":         {},
	"http":       {},
	"exec":       {},
	"secrets":    {},
	"events.pub": {},
	"events.sub": {},
	"tool":       {},
}

// forbiddenRawImportModules is the §3.4 of MANIFEST-SCHEMA + #392 spec
// list of WASI Preview 2 modules that MUST be expressed via a scoped
// handle, never via raw import. A manifest declaring `[[raw_imports]]`
// with a module starting with one of these prefixes fails validation
// with ErrRawImportForbidden — there is no override or warning prompt.
//
// The list is matched as PREFIX, so e.g. `wasi:filesystem/types` covers
// `wasi:filesystem/types/descriptor` and any future sub-interface.
var forbiddenRawImportModules = []string{
	"wasi:filesystem/",   // must use fs.Handle
	"wasi:sockets/",      // must use a provider or scoped network handle
	"wasi:random/random", // must use a future crypto.Handle (safe RNG)
	"wasi:io/streams",    // arbitrary fd — must go through scoped handle
}

// allowedRawImportModules is the §3.4 + #392 spec list of WASI modules
// safe to import directly. The install UX still warns the operator
// (Stage 5 of #392), but validation accepts them. Matched as PREFIX.
//
// Rationale per #392 §M9: the truly dangerous imports have scoped
// alternatives. The truly harmless ones (low-resolution clock, scoped
// env vars, pure compute) stay accessible to keep the escape hatch
// usable.
var allowedRawImportModules = []string{
	"wasi:clocks/monotonic-clock", // low-res only — daemon clamps resolution at runtime
	"wasi:clocks/wall-clock",      // low-res only — same clamp
	"wasi:cli/environment",        // explicitly scoped env vars per manifest
	"wasi:cli/exit",               // exit code — pure terminal signal
	"wasi:cli/stdin",              // pure compute — no host fs
	"wasi:cli/stdout",             // pure compute — no host fs
	"wasi:cli/stderr",             // pure compute — no host fs
	"wasi:cli/terminal-input",     // tty mode — guest can't escalate
	"wasi:cli/terminal-output",    // tty mode — guest can't escalate
}

// rawImportModulePattern enforces the WASI Preview 2 module syntax —
// `wasi:<package>/<interface>` with lowercase alphanumeric, slash, dot,
// underscore, hyphen. A guest manifest using anything else (e.g. ad-hoc
// "host:fs", a custom scheme, an empty fragment) fails validation.
var rawImportModulePattern = regexp.MustCompile(`^wasi:[a-z0-9][a-z0-9/._-]*$`)

// rawImportFunctionPattern enforces the WASI Preview 2 function name
// syntax — lowercase alphanumeric, underscore, hyphen.
var rawImportFunctionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// providerExportIDPattern allows lowercase, digits, dot/hyphen — the
// canonical handle-kind name shape (e.g. "bluetooth.scan",
// "gpu.compute", "image-classify"). Hyphens and dots both legal so
// providers can match common conventions without forcing a single
// separator.
var providerExportIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

// scopeFieldNamePattern enforces the TOML-key shape for scope field
// names. Lowercase, digits, underscore — matches Go-struct-tag
// conventions and avoids the dot/hyphen tokens that would force a
// quoted-key in TOML at the consumer side.
var scopeFieldNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// validScopeFieldTypes is the closed enum the validator accepts for
// [[provider.exports.scope_fields]].type. Adding a new type means a
// new runtime validator branch (see resolveDepends in
// internal/runtime/instantiator_verified.go) AND an update to
// MANIFEST-SCHEMA.md §3.4.
var validScopeFieldTypes = map[string]ScopeFieldType{
	"string":      ScopeFieldTypeString,
	"int":         ScopeFieldTypeInt,
	"bool":        ScopeFieldTypeBool,
	"string-list": ScopeFieldTypeStringList,
	"int-list":    ScopeFieldTypeIntList,
}

// dependsHandlePattern enforces the namespace-scoped handle reference
// format `<ns>:<id>` per #392 §H2. <ns> is a fingerprint short or the
// reserved "alf"; <id> is the handle kind. Captures both halves so the
// validator can apply per-namespace rules.
var dependsHandlePattern = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*):([a-z0-9][a-z0-9.-]*)$`)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// topicPattern allows lowercase, digits, dot/underscore/hyphen — matches
// common topic-name conventions (e.g. "chat.log", "email.new", "task_done").
// Wildcards / glob patterns are intentionally NOT permitted in 0.8.0
// per the §3.3 promise of explicit cross-flows.
var topicPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// knownKinds is the enum of accepted manifest.kind values (MANIFEST-SCHEMA §3.3).
//
// #392 split the legacy "provider" kind into two: "llm-provider" (LLM
// backend bundles) and "capability-provider" (new authority surfaces
// via [[provider.exports]]). The bare "provider" string is intentionally
// absent from this map — it would have been ambiguous and Stage 3
// onwards (forge integration) needs the kind to disambiguate at parse
// time, not via post-hoc inspection of the [provider] block.
var knownKinds = map[string]ManifestKind{
	"wasm-tool":           KindWASMTool,
	"wasm-app":            KindWASMApp,
	"skill":               KindSkill,
	"llm-provider":        KindLLMProvider,
	"capability-provider": KindCapabilityProvider,
	"marketplace-app":     KindMarketplaceApp,
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
	"events":               {},
	"tools":                {},
	"provider":             {}, // #392 — only valid when kind = capability-provider
	"depends":              {}, // #392 — namespace-scoped handle deps
	"raw_imports":          {}, // #392 — escape-hatch raw WASI access
	// Deferred blocks — recognised so we can produce a helpful error,
	// but their presence fails validation.
	"http":    {},
	"exec":    {},
	"secrets": {},
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

	// provider block (#392 Stage 1) — only valid when kind ==
	// capability-provider. We pass the typed kind so the validator
	// can refuse [provider] in any other context with a clear error
	// rather than silently ignoring the block.
	providerBlock, err := validateProviderBlock(t.Provider, kind)
	if err != nil {
		return nil, err
	}

	// depends block (#392 Stage 1).
	depends, err := validateDependsBlock(t.Depends)
	if err != nil {
		return nil, err
	}

	// raw_imports block (#392 Stage 1).
	rawImports, err := validateRawImportsBlock(t.RawImports)
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
		Provider:        providerBlock,
		Depends:         depends,
		RawImports:      rawImports,
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

// validateProviderBlock walks `[provider]` and rejects malformed
// entries. The block is only valid when the manifest's kind is
// `capability-provider`; declaring [[provider.exports]] on any other
// kind is a parse-time error so the schema doesn't depend on runtime
// behavior to disambiguate "this is a provider" from "this is a tool
// that happens to have a [provider] block leftover from a copy-paste".
func validateProviderBlock(raw tomlProviderBlock, kind ManifestKind) (ProviderBlock, error) {
	if len(raw.Exports) == 0 {
		// Empty [provider] block is treated as absent; no error even
		// for non-provider kinds. Only declared exports trigger the
		// kind check.
		return ProviderBlock{}, nil
	}
	if kind != KindCapabilityProvider {
		return ProviderBlock{}, fmt.Errorf("%w: got kind=%q", ErrProviderBlockNotAllowedHere, kind)
	}
	out := make([]ProviderExport, 0, len(raw.Exports))
	seen := make(map[string]struct{}, len(raw.Exports))
	for i, e := range raw.Exports {
		if e.ID == "" {
			return ProviderBlock{}, fmt.Errorf("%w: provider.exports[%d]", ErrProviderExportIDEmpty, i)
		}
		if !providerExportIDPattern.MatchString(e.ID) {
			return ProviderBlock{}, fmt.Errorf("%w: provider.exports[%d].id=%q", ErrProviderExportIDMalformed, i, e.ID)
		}
		if _, dup := seen[e.ID]; dup {
			return ProviderBlock{}, fmt.Errorf("%w: provider.exports[%d].id=%q", ErrProviderExportDuplicate, i, e.ID)
		}
		seen[e.ID] = struct{}{}
		fields, err := validateScopeFields(e.ScopeFields, i)
		if err != nil {
			return ProviderBlock{}, err
		}
		out = append(out, ProviderExport{ID: e.ID, ScopeFields: fields})
	}
	return ProviderBlock{Exports: out}, nil
}

// validateScopeFields walks the scope_fields array under one provider
// export and checks each entry's name + type + uniqueness. Empty
// scope_fields list is legal (the export takes no scope). The
// exportIdx parameter feeds error messages so authors can find the
// offending entry by index.
func validateScopeFields(raw []tomlScopeField, exportIdx int) ([]ScopeField, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]ScopeField, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for j, f := range raw {
		if f.Name == "" {
			return nil, fmt.Errorf("%w: provider.exports[%d].scope_fields[%d]", ErrScopeFieldNameEmpty, exportIdx, j)
		}
		if !scopeFieldNamePattern.MatchString(f.Name) {
			return nil, fmt.Errorf("%w: provider.exports[%d].scope_fields[%d].name=%q", ErrScopeFieldNameMalformed, exportIdx, j, f.Name)
		}
		if f.Type == "" {
			return nil, fmt.Errorf("%w: provider.exports[%d].scope_fields[%d].name=%q", ErrScopeFieldTypeEmpty, exportIdx, j, f.Name)
		}
		typed, ok := validScopeFieldTypes[f.Type]
		if !ok {
			return nil, fmt.Errorf("%w: provider.exports[%d].scope_fields[%d].type=%q", ErrScopeFieldTypeUnknown, exportIdx, j, f.Type)
		}
		if _, dup := seen[f.Name]; dup {
			return nil, fmt.Errorf("%w: provider.exports[%d].scope_fields[%d].name=%q", ErrScopeFieldDuplicate, exportIdx, j, f.Name)
		}
		seen[f.Name] = struct{}{}
		out = append(out, ScopeField{Name: f.Name, Type: typed, Required: f.Required})
	}
	return out, nil
}

// validateDependsBlock walks `[[depends]]` and rejects malformed
// entries per #392. Each entry's `handle` field must follow the
// `<ns>:<id>` namespace-scoped format. The reserved `alf:` namespace
// is restricted to a closed allowlist of known core handle kinds —
// providers cannot claim a core kind via collision (the only path to
// `alf:fs` is via the daemon's bundled forge code, not via a
// `[[provider.exports]]` declaration).
//
// Stage 1 only validates format + closed-allowlist for `alf:`; Stage 3
// (forge integration) will look up the concrete provider behind a
// non-`alf:` namespace at load time.
//
// The Scope map is copied through unchanged — it's opaque at this
// stage. Stage 4 of #392 will validate the scope against the
// provider's exported scope schema (M8 audit finding: scope checks
// happen Runtime-side, not in the provider).
func validateDependsBlock(raw []tomlDependsEntry) ([]DependsEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]DependsEntry, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for i, d := range raw {
		if d.Handle == "" {
			return nil, fmt.Errorf("%w: depends[%d]", ErrDependsHandleEmpty, i)
		}
		m := dependsHandlePattern.FindStringSubmatch(d.Handle)
		if m == nil {
			return nil, fmt.Errorf("%w: depends[%d].handle=%q", ErrDependsHandleMalformed, i, d.Handle)
		}
		ns, id := m[1], m[2]
		if ns == reservedNamespaceALF {
			if _, ok := coreHandleIDs[id]; !ok {
				return nil, fmt.Errorf("%w: depends[%d].handle=%q (id %q is not a core handle)", ErrDependsHandleNamespaceReserved, i, d.Handle, id)
			}
		}
		if _, dup := seen[d.Handle]; dup {
			return nil, fmt.Errorf("%w: depends[%d].handle=%q", ErrDependsDuplicate, i, d.Handle)
		}
		seen[d.Handle] = struct{}{}

		entry := DependsEntry{Handle: d.Handle}
		if len(d.Scope) > 0 {
			entry.Scope = make(map[string]any, len(d.Scope))
			for k, v := range d.Scope {
				entry.Scope[k] = v
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// validateRawImportsBlock walks `[[raw_imports]]` and applies the §3.4
// allowlist. A module that matches forbiddenRawImportModules is
// rejected with ErrRawImportForbidden (the spec says "must use a
// scoped handle instead" — there is no override). A module that
// matches allowedRawImportModules passes; a module in neither list
// fails with ErrRawImportNotInAllowlist (default-deny — adding a new
// allowed import is a deliberate schema change).
//
// The justification field is required and must be non-empty: operators
// see it at install time (Stage 5 of #392) before approving raw access.
// An empty justification means the bundle author either didn't think
// about why they need raw access or doesn't want to surface it — both
// are reasons to refuse the manifest.
func validateRawImportsBlock(raw []tomlRawImport) ([]RawImport, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]RawImport, 0, len(raw))
	for i, r := range raw {
		if r.Module == "" {
			return nil, fmt.Errorf("%w: raw_imports[%d]", ErrRawImportModuleEmpty, i)
		}
		if !rawImportModulePattern.MatchString(r.Module) {
			return nil, fmt.Errorf("%w: raw_imports[%d].module=%q", ErrRawImportModuleMalformed, i, r.Module)
		}
		if r.Function == "" {
			return nil, fmt.Errorf("%w: raw_imports[%d]", ErrRawImportFunctionEmpty, i)
		}
		if !rawImportFunctionPattern.MatchString(r.Function) {
			return nil, fmt.Errorf("%w: raw_imports[%d].function=%q", ErrRawImportFunctionMalformed, i, r.Function)
		}
		if classifyRawImport(r.Module) == rawImportForbidden {
			return nil, fmt.Errorf("%w: raw_imports[%d].module=%q (declare a scoped handle in [[depends]] or use [fs]/[events]/[tools] instead)", ErrRawImportForbidden, i, r.Module)
		}
		if classifyRawImport(r.Module) == rawImportUnknown {
			return nil, fmt.Errorf("%w: raw_imports[%d].module=%q (allowed list lives in MANIFEST-SCHEMA §3.4 + #392; new entries require a schema bump)", ErrRawImportNotInAllowlist, i, r.Module)
		}
		// Justification: spec says non-empty. Trim incidental
		// whitespace before checking so a tab-indented multi-line
		// TOML literal counts as empty and the operator-facing
		// install prompt isn't filled with "   ".
		if strings.TrimSpace(r.Justification) == "" {
			return nil, fmt.Errorf("%w: raw_imports[%d].module=%q", ErrRawImportJustificationEmpty, i, r.Module)
		}
		out = append(out, RawImport{
			Module:        r.Module,
			Function:      r.Function,
			Justification: r.Justification,
		})
	}
	return out, nil
}

// rawImportClass is the classifier output for a manifest-declared
// raw_import module. Kept as an unexported enum so external callers
// always go through validateRawImportsBlock — they can't bypass the
// allowlist by writing a "looks classified" check of their own.
type rawImportClass int

const (
	rawImportUnknown rawImportClass = iota
	rawImportAllowed
	rawImportForbidden
)

// classifyRawImport is the prefix-match classifier used by
// validateRawImportsBlock and the archtest pin (raw_import_classification_test.go).
// Forbidden takes priority over allowed (defence in depth — a future
// allowed prefix that incidentally subsumes a forbidden one would
// otherwise let the forbidden import slip through).
func classifyRawImport(module string) rawImportClass {
	for _, p := range forbiddenRawImportModules {
		if strings.HasPrefix(module, p) {
			return rawImportForbidden
		}
	}
	for _, p := range allowedRawImportModules {
		if strings.HasPrefix(module, p) {
			return rawImportAllowed
		}
	}
	return rawImportUnknown
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
