package handle

import (
	"context"
	"errors"
	"testing"

	"github.com/alamparelli/alf/internal/capability"
)

// resetMintStateForTest clears the package-level mint state so each test
// starts from a clean slate. Tests in this file must not run in parallel.
func resetMintStateForTest(t *testing.T) {
	t.Helper()
	mintLock.Store(false)
	mintedOK.Store(false)
	mintedToken = RuntimeToken{}
}

func TestForge_MintAndValidate(t *testing.T) {
	resetMintStateForTest(t)

	tok := MintRuntimeToken()
	inst, err := ForgeInstance(tok, context.Background(), capability.ID("cap-ok"), nil)
	if err != nil {
		t.Fatalf("ForgeInstance with valid token: got err=%v, want nil", err)
	}
	if inst == nil {
		t.Fatal("ForgeInstance returned nil Instance with valid token")
	}
	if inst.Owner != capability.ID("cap-ok") {
		t.Errorf("Instance.Owner=%q, want cap-ok", inst.Owner)
	}
}

func TestForge_ZeroTokenRejected(t *testing.T) {
	resetMintStateForTest(t)

	// Mint a valid token so the minter state is populated, then present a
	// zero-valued token — it must still be rejected.
	_ = MintRuntimeToken()

	inst, err := ForgeInstance(RuntimeToken{}, context.Background(), "cap", nil)
	if !errors.Is(err, ErrInvalidRuntimeToken) {
		t.Fatalf("ForgeInstance with zero token: err=%v, want ErrInvalidRuntimeToken", err)
	}
	if inst != nil {
		t.Error("ForgeInstance returned non-nil Instance when token was invalid")
	}
}

func TestForge_BeforeMintRejected(t *testing.T) {
	resetMintStateForTest(t)

	// No mint has occurred — even a token-shaped value must be rejected.
	inst, err := ForgeInstance(RuntimeToken{}, context.Background(), "cap", nil)
	if !errors.Is(err, ErrInvalidRuntimeToken) {
		t.Fatalf("ForgeInstance before Mint: err=%v, want ErrInvalidRuntimeToken", err)
	}
	if inst != nil {
		t.Error("ForgeInstance before Mint returned non-nil Instance")
	}
}

func TestForge_SecondMintPanics(t *testing.T) {
	resetMintStateForTest(t)

	_ = MintRuntimeToken()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("second MintRuntimeToken call did not panic")
		}
	}()
	_ = MintRuntimeToken()
}

func TestForge_TamperedTokenRejected(t *testing.T) {
	resetMintStateForTest(t)

	_ = MintRuntimeToken()

	// Tests share the package, so we can construct a RuntimeToken with a
	// hand-picked key — simulating an attacker who bypassed archtest and
	// tried to synthesise a token with all zeros or a guessed pattern.
	var fake RuntimeToken
	for i := range fake.key {
		fake.key[i] = 0xAA
	}

	inst, err := ForgeInstance(fake, context.Background(), "cap", nil)
	if !errors.Is(err, ErrInvalidRuntimeToken) {
		t.Fatalf("ForgeInstance with tampered token: err=%v, want ErrInvalidRuntimeToken", err)
	}
	if inst != nil {
		t.Error("ForgeInstance with tampered token returned non-nil Instance")
	}
}

func TestForge_RejectionLeavesMintStateIntact(t *testing.T) {
	resetMintStateForTest(t)

	tok := MintRuntimeToken()

	// A rejected forge attempt must not disturb subsequent valid forges —
	// this checks we have no side effect on the minter state from a bad
	// token comparison.
	_, _ = ForgeInstance(RuntimeToken{}, context.Background(), "cap-bad", nil)

	inst, err := ForgeInstance(tok, context.Background(), "cap-good", nil)
	if err != nil {
		t.Fatalf("valid forge after rejected attempt: err=%v, want nil", err)
	}
	if inst == nil {
		t.Fatal("valid forge after rejected attempt returned nil Instance")
	}
}
