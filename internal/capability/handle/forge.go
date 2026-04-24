package handle

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"sync/atomic"

	"github.com/alamparelli/alf/internal/capability"
)

// RuntimeToken is the unforgeable witness that identifies the caller of
// ForgeInstance as the Runtime. The key field is unexported, so code outside
// internal/capability/handle cannot construct a RuntimeToken via composite
// literal. Only MintRuntimeToken — which archtest forbids importing outside
// internal/runtime/ — can produce a valid one. Together these rules realise
// the §4.3 pattern: the forge is behind a private type gated by a
// runtime-only witness (three overlapping locks with the archtest).
type RuntimeToken struct {
	key [32]byte
}

var (
	mintedToken RuntimeToken
	mintedOK    atomic.Bool
	mintLock    atomic.Bool
)

// ErrInvalidRuntimeToken is returned by ForgeInstance when the caller does
// not present the Runtime's minted token.
var ErrInvalidRuntimeToken = errors.New("handle: invalid runtime token")

// MintRuntimeToken returns the process-wide RuntimeToken on first call.
// A second call panics: two mints would mean a non-Runtime caller is trying
// to obtain authority, which is a programming error that must surface
// immediately. Runtime calls this exactly once at daemon init.
//
// Archtest rule (#391 step 5): import of this symbol outside
// internal/runtime/ is forbidden.
func MintRuntimeToken() RuntimeToken {
	if !mintLock.CompareAndSwap(false, true) {
		panic("handle: MintRuntimeToken called more than once — Runtime is singleton")
	}
	var fresh RuntimeToken
	if _, err := rand.Read(fresh.key[:]); err != nil {
		panic("handle: crypto/rand failed: " + err.Error())
	}
	mintedToken = fresh
	mintedOK.Store(true)
	return fresh
}

// ForgeInstance is the sole gated constructor of an *Instance under the
// ocap model. Callers must present the RuntimeToken minted at daemon init.
// Any other caller receives ErrInvalidRuntimeToken and no handle.
//
// Handles passed through (fs, httpH) may be nil — a nil slot means the
// manifest did not declare that resource. NewInstance remains exported
// for the moment to keep the existing prototype wiring compilable; it
// will be demoted to unexported once Runtime.Instantiate consumes the
// forge (later step of #391).
func ForgeInstance(tok RuntimeToken, ctx context.Context, owner capability.ID, fs *FSHandle, httpH *HTTPHandle) (*Instance, error) {
	if !mintedOK.Load() {
		return nil, ErrInvalidRuntimeToken
	}
	if subtle.ConstantTimeCompare(tok.key[:], mintedToken.key[:]) != 1 {
		return nil, ErrInvalidRuntimeToken
	}
	return NewInstance(ctx, owner, fs, httpH), nil
}
