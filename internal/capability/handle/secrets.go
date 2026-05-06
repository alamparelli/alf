package handle

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/alamparelli/alf/internal/capability"
)

// ErrSecretNotFound is returned by SecretsHandle.Get when the backing
// vault has no entry for the requested key. It is distinct from
// ErrOutOfScope: the scope allowed the read, but no secret exists.
var ErrSecretNotFound = errors.New("handle: secret not found")

// SecretsReader is the narrow interface SecretsHandle uses to reach the
// vault. The production implementation is internal/sandbox/secrets.Manager,
// wrapped at forge time; tests pass an in-memory stub. Intentionally
// minimal — the handle enforces scope, the reader only fetches.
type SecretsReader interface {
	GetSecret(name string) (string, error)
}

// SecretsScope lists the secret names a capability may read. Entries are
// either exact names ("github_token") or a prefix pattern ending in "*"
// ("github_*"). Empty list = nothing allowed (secure default).
type SecretsScope struct {
	Names []string
}

// Allows reports whether name is in scope.
func (s SecretsScope) Allows(name string) bool {
	if name == "" {
		return false
	}
	for _, pattern := range s.Names {
		if pattern == name {
			return true
		}
		if strings.HasSuffix(pattern, "*") {
			prefix := pattern[:len(pattern)-1]
			if prefix != "" && strings.HasPrefix(name, prefix) {
				return true
			}
		}
	}
	return false
}

// SecretsHandle grants scoped read access to the capability-scope vault
// (see ARCHITECTURE-SECURITY.md §7.5.1). Write is intentionally absent:
// secret writes cross the admin boundary and require TTY ratification
// (§6), never available via a handle.
type SecretsHandle struct {
	_ [0]noSerialize

	owner        capability.ID
	scope        SecretsScope
	reader       SecretsReader
	lifecycleCtx context.Context
	revoked      atomic.Bool
}

// NewSecretsHandle constructs a secrets handle scoped to the given names.
func NewSecretsHandle(owner capability.ID, scope SecretsScope, reader SecretsReader) *SecretsHandle {
	return &SecretsHandle{owner: owner, scope: scope, reader: reader}
}

// Get returns the secret value for name if scope allows it and the handle
// has not been revoked. ctx is honoured for cancellation before the vault
// call; the underlying reader is not itself context-aware in today's
// implementation, so a hung vault would block here. Future: surface ctx
// into the reader interface.
//
// The returned SecretValue redacts on String / GoString / MarshalJSON /
// MarshalBinary / MarshalText so an accidental %v / json.Marshal /
// log line cannot surface the plaintext (#395 Stage 2 chunk 4). Use
// SecretValue.ConsumeInto(w) for the trusted path (HTTP header
// injection, HMAC seed) or SecretValue.Reveal() when grep-auditable
// exposure is acceptable.
func (h *SecretsHandle) Get(ctx context.Context, name string) (SecretValue, error) {
	if h.revoked.Load() {
		return SecretValue{}, ErrRevoked
	}
	if !h.scope.Allows(name) {
		return SecretValue{}, ErrOutOfScope
	}
	if err := ctx.Err(); err != nil {
		return SecretValue{}, err
	}
	if h.lifecycleCtx != nil {
		if err := h.lifecycleCtx.Err(); err != nil {
			return SecretValue{}, ErrRevoked
		}
	}
	if h.reader == nil {
		return SecretValue{}, ErrSecretNotFound
	}
	val, err := h.reader.GetSecret(name)
	if err != nil {
		return SecretValue{}, err
	}
	if val == "" {
		return SecretValue{}, ErrSecretNotFound
	}
	return NewSecretValueFromString(val), nil
}

// Owner returns the capability ID this handle was forged for.
func (h *SecretsHandle) Owner() capability.ID { return h.owner }

// MarshalJSON implements §4.2 invariant 1.
func (h *SecretsHandle) MarshalJSON() ([]byte, error) {
	return nil, ErrHandleNonSerializable
}

// attachLifecycle binds the handle to the Instance lifecycle context.
func (h *SecretsHandle) attachLifecycle(ctx context.Context) { h.lifecycleCtx = ctx }
