package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// SignedManifest is the input to Instantiator.Instantiate. Today it wraps
// capability.Manifest only; the Signature / canonical envelope fields
// spec'd in #397 slot in here once the parser lands. Keeping the type
// now means call sites don't churn later.
type SignedManifest struct {
	Manifest capability.Manifest

	// BaseDir is the on-disk directory the capability was loaded from.
	// FSHandle scope paths are resolved against this root so manifests
	// can use relative paths. Empty when not applicable (e.g., native
	// Go-kind tools that have no bundle dir).
	BaseDir string
}

// TrustVerifier checks that a SignedManifest's signature matches the
// trust store. The real implementation lands in #388; for now a no-op
// verifier ships so Instantiator can be exercised end-to-end without
// blocking on the trust chain.
type TrustVerifier interface {
	Verify(signed SignedManifest) error
}

// nopVerifier accepts every manifest. Wire point for the real verifier
// (#388); production Instantiator will replace this with a TrustStore-
// backed implementation once the trust spec is impl'd.
type nopVerifier struct{}

func (nopVerifier) Verify(_ SignedManifest) error { return nil }

// ErrManifestID is returned by Instantiate when the manifest has an
// empty ID — a forge result without an owner identity cannot be scoped
// to anything and is treated as a programming error.
var ErrManifestID = errors.New("runtime: SignedManifest has empty Manifest.ID")

// Instantiator is the sole forge of capability.handle.Instance under
// the Tier 3.1 ocap model (ARCHITECTURE-SECURITY.md §3.1, §4.3). It
// holds the process-wide RuntimeToken minted once at construction and
// is the only code path that reaches handle.ForgeInstance.
//
// The existing Runtime (Chat / Invoke / Converse) is untouched —
// Instantiator coexists with it during the migration window. Once
// capabilities start receiving Instance values (deferred to
// #398/#399/#400), the orchestrator methods will call Instantiate
// before dispatch instead of resolving via the Registry directly.
type Instantiator struct {
	token    handle.RuntimeToken
	verifier TrustVerifier

	// Bus and CrossFlow are the optional events wiring (#399). When
	// both are non-nil, InstantiateVerified forges EventPub/EventSub
	// handles per the manifest's events block + cross-flow registry.
	// When either is nil (default for tests + legacy callers), no
	// events handles are forged.
	bus       handle.EventPublisher
	subscribe handle.EventSubscriber
	crossFlow CrossFlowQuerier

	// invoker is the optional inter-capability invoker (#389). When
	// non-nil, InstantiateVerified forges a ToolHandle scoped to the
	// manifest's [[tools.declares]] entries. When nil (tests + legacy
	// callers), Tool handles stay nil and capabilities cannot reach
	// other capabilities even if their manifest declared the link —
	// matching the §3.1 invariant "no invoker, no authority".
	invoker handle.ToolInvoker

	// handleRegistry is the runtime registry populated at boot with
	// alf: core kinds and at install with each provider's exports
	// (#392). When non-nil, InstantiateVerified validates every
	// [[depends]] entry against it (fail closed on unregistered
	// handle) AND registers a capability-provider's [[provider.exports]]
	// under the publisher's fingerprint short. When nil (tests +
	// legacy callers that don't exercise depends), validation +
	// registration are skipped — manifests with [[depends]] that
	// reach the forge with no registry wired pass through unchecked,
	// matching the "no registry, no authority" pattern of the other
	// optional fields above.
	handleRegistry *handle.HandleRegistry

	// Live registry — populated by InstantiateVerified after every
	// successful forge. RevokeByKey walks this list to close every
	// Instance signed by a given key. Entries self-prune via a
	// watcher goroutine when the Instance's lifecycle ctx cancels.
	// See revocation.go for the wiring; #396 deliverable 3.
	liveMu           sync.Mutex
	live             []liveEntry
	revocationLogger func(format string, args ...any)
}

// CrossFlowQuerier is the narrow read-side of internal/runtime/events
// CrossFlowRegistry that the Instantiator needs at forge time. Pulling
// the type out as an interface keeps runtime → events a one-way
// dependency (the forge never reaches into the bus impl).
type CrossFlowQuerier interface {
	HasExport(publisher capability.ID, topic string) bool
}

// InstantiatorOption configures an Instantiator at construction. Keeps
// NewInstantiator a one-argument-style call while leaving room for
// DI of the verifier, a custom secrets reader, etc.
type InstantiatorOption func(*Instantiator)

// WithTrustVerifier substitutes the default no-op verifier. Used in
// tests and by the production daemon once #388 lands.
func WithTrustVerifier(v TrustVerifier) InstantiatorOption {
	return func(i *Instantiator) { i.verifier = v }
}

// WithEventsBus wires the cross-capability event bus (#399). Both pub
// and sub interfaces typically come from the same *events.Bus instance;
// they are split here so a future bus split (separate publisher / queue
// router) can be wired without changing the Instantiator API.
func WithEventsBus(pub handle.EventPublisher, sub handle.EventSubscriber) InstantiatorOption {
	return func(i *Instantiator) {
		i.bus = pub
		i.subscribe = sub
	}
}

// WithCrossFlowRegistry wires the publisher-topic registry the loader
// populates in pass 1. Required (alongside WithEventsBus) for events
// handle forging; without it InstantiateVerified skips event handles.
func WithCrossFlowRegistry(r CrossFlowQuerier) InstantiatorOption {
	return func(i *Instantiator) { i.crossFlow = r }
}

// WithToolInvoker wires the inter-capability invoker that ToolHandle
// dispatches through (#389). Production daemons plug in a registry-
// backed invoker so a skill's declared tool calls reach the right
// capability; tests pass a stub that records invocations. When this
// option is omitted, [[tools.declares]] entries in a manifest are
// validated but no Tool handle is forged.
func WithToolInvoker(inv handle.ToolInvoker) InstantiatorOption {
	return func(i *Instantiator) { i.invoker = inv }
}

// WithHandleRegistry wires the runtime handle registry (#392 Stage 3).
// When set, InstantiateVerified consults the registry on every
// [[depends]] entry — an unregistered <ns>:<id> reference fails the
// instantiation with ErrDependsHandleNotRegistered. Capability-provider
// manifests have their [[provider.exports]] registered under the
// publisher's fingerprint short before the forge runs, so a downstream
// capability's [[depends]] resolves on a sibling registration in the
// same Boot scan IF the provider was loaded first.
//
// Tests + legacy callers that don't exercise depends pass nil; the
// validation + registration steps are skipped, matching the "no
// registry, no authority" precedent set by WithEventsBus / WithToolInvoker.
func WithHandleRegistry(reg *handle.HandleRegistry) InstantiatorOption {
	return func(i *Instantiator) { i.handleRegistry = reg }
}

// NewInstantiator constructs the singleton forge for a daemon process.
// Calls handle.MintRuntimeToken exactly once; a second call panics
// (§4.3 one-shot invariant). Tests construct many Instantiators and
// must call handle.ResetMintForTesting between cases.
func NewInstantiator(opts ...InstantiatorOption) *Instantiator {
	inst := &Instantiator{
		token:    handle.MintRuntimeToken(),
		verifier: nopVerifier{},
	}
	for _, o := range opts {
		o(inst)
	}
	return inst
}

// SeedHandleRegistry registers every alf-namespaced core handle kind
// into reg using the Instantiator's runtime token. Called from the
// daemon boot path after both the Instantiator and the registry have
// been constructed; one call seeds the entire alf: namespace.
//
// The token never escapes the Instantiator: this method is the only
// way an external caller can drive RegisterCore through the runtime's
// authority. Provider-installed exports go through
// RegisterProviderExports — same gating, different namespace.
//
// Returns the registry's first error if RegisterCore fails. The daemon
// boot path treats any failure as fatal; nothing is recoverable from
// here (a duplicate-register on first boot is a wiring bug).
func (i *Instantiator) SeedHandleRegistry(reg *handle.HandleRegistry) error {
	return reg.RegisterCore(i.token)
}

// RegisterProviderExports registers each entry of `exports` into reg
// under the publisher's fingerprint short namespace (#392 Stage 3).
// Used at install time of a capability-provider bundle: the verify
// path produces a SignerID + the manifest's [[provider.exports]];
// this method bridges them into registry entries the forge can later
// resolve [[depends]] against.
//
// signerID is the trust-store KeyID of the bundle's signer — the
// `<ns>` part of `<ns>:<id>` references in dependent manifests is
// `signerID.HexLower()`. Two providers signed by different keys can
// both export the same handle id (e.g. `bluetooth.scan`); a consumer
// disambiguates by picking which fingerprint to depend on.
//
// The runtime token never escapes the Instantiator. Returns on the
// first registry error; partial state (e.g. some exports registered
// before a duplicate fires) is left intact — the caller treats
// install as one transaction and either retries cleanly or rolls
// back externally.
func (i *Instantiator) RegisterProviderExports(reg *handle.HandleRegistry, signerID envelope.KeyID, exports []envelope.ProviderExport) error {
	if len(exports) == 0 {
		return nil
	}
	ns := signerID.HexLower()
	for _, e := range exports {
		k := handle.HandleKind{Namespace: ns, ID: e.ID}
		if err := reg.Register(i.token, k); err != nil {
			return fmt.Errorf("provider %s: register %s: %w", ns, k.FullName(), err)
		}
	}
	return nil
}

// Instantiate verifies the signed manifest, forges the handle set it
// requests, and returns an *handle.Instance whose fields carry only
// the authority the manifest declared. Every other slot is nil — the
// capability literally has no way to reach what it did not request.
//
// ctx is the parent context for the instance's lifecycle: when it (or
// the returned Instance.Close) cancels, every handle revokes
// structurally, aborting in-flight ops through context.AfterFunc.
func (i *Instantiator) Instantiate(ctx context.Context, signed SignedManifest) (*handle.Instance, error) {
	if signed.Manifest.ID == "" {
		return nil, ErrManifestID
	}
	if err := i.verifier.Verify(signed); err != nil {
		return nil, err
	}
	grants := i.forgeGrants(signed)
	return handle.ForgeInstance(i.token, ctx, signed.Manifest.ID, grants)
}

// forgeGrants derives the handle set from the verified manifest. The
// mapping here reflects today's capability.Manifest schema (FilePaths /
// Networks / Secrets); it grows when #397 canonicalises the envelope
// and adds Exec + Tool declarations. Unset permission fields mean the
// corresponding handle is nil — the capability cannot reach that
// resource.
//
// Scope semantics compiled here are authoritative for the lifetime of
// the Instance. The manifest is verified BEFORE this runs, so the
// fields we read are trusted input.
func (i *Instantiator) forgeGrants(signed SignedManifest) handle.Grants {
	m := signed.Manifest
	g := handle.Grants{}

	// FilePaths: today's schema does not distinguish read vs write —
	// treat every entry as read-capable. Write mapping arrives with
	// the #397 envelope (entries will carry a Mode field).
	if len(m.Permissions.FilePaths) > 0 {
		g.FS = handle.NewFSHandle(m.ID, signed.BaseDir, handle.FSScope{
			Reads: m.Permissions.FilePaths,
		})
	}

	// Networks: treated as hostname patterns. Exact match or wildcard
	// subdomain pattern ("*.example.com"). CIDR parsing defer until
	// #397 specifies the format.
	if len(m.Permissions.Networks) > 0 {
		g.HTTP = handle.NewHTTPHandle(m.ID, handle.HTTPScope{
			Hosts: m.Permissions.Networks,
		}, nil)
	}

	// Secrets: key-pattern allowlist. Reader is nil at this step —
	// wiring to sandbox/secrets.Manager happens when Manager exposes
	// a narrow per-capability ReaderFor(id) helper. Until then, a
	// SecretsHandle with a nil reader returns ErrSecretNotFound on
	// every lookup (defensive default, never succeeds silently).
	if len(m.Permissions.Secrets) > 0 {
		g.Secrets = handle.NewSecretsHandle(m.ID, handle.SecretsScope{
			Names: m.Permissions.Secrets,
		}, nil)
	}

	// Exec: no field on today's legacy Manifest — stays nil.
	// Tool: forged in InstantiateVerified from envelope.Manifest.Tools
	// when an invoker is wired, mirroring how event handles are forged.
	// Going through the legacy SignedManifest path always yields a nil
	// Tool handle.

	return g
}
