package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// recordingRevokeLogger captures every revocation log line so tests
// can assert on close reasons.
type recordingRevokeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (r *recordingRevokeLogger) log(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *recordingRevokeLogger) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.lines))
	copy(out, r.lines)
	return out
}

// TestRevokeByKey_DirectSignerOnly: legacy path — revoking a key
// closes the Instance signed by it. No cascade involved (Instance has
// no provider depends).
func TestRevokeByKey_DirectSignerOnly(t *testing.T) {
	handle.ResetMintForTesting()
	rec := &recordingRevokeLogger{}
	inst := NewInstantiator(WithRevocationLogger(rec.log))

	in, _ := signBundle(t, verifiedManifest, nil)
	vi, err := inst.InstantiateVerified(context.Background(), in, "")
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	closed := inst.RevokeByKey(vi.SignerID)
	if len(closed) != 1 {
		t.Fatalf("closed=%d, want 1", len(closed))
	}
	lines := rec.snapshot()
	if len(lines) != 1 {
		t.Fatalf("logger: lines=%d, want 1", len(lines))
	}
	// Direct path uses the "signed by revoked key" reason text.
	if !strings.Contains(lines[0], "signed by revoked key") {
		t.Errorf("expected 'signed by revoked key' reason, got %q", lines[0])
	}
}

// TestRevokeByKey_CascadeCloseDependentConsumer — the load-bearing
// #392 Stage 5 invariant: revoking a provider's key closes every
// consumer that depended on it, even though the consumer itself was
// signed by a different key.
func TestRevokeByKey_CascadeCloseDependentConsumer(t *testing.T) {
	handle.ResetMintForTesting()
	rec := &recordingRevokeLogger{}
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(
		WithHandleRegistry(reg),
		WithRevocationLogger(rec.log),
	)
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	provManifest := `alf_envelope_version = 1
id      = "cascade-prov"
kind    = "capability-provider"
version = "0.1.0"
name    = "P"

[[provider.exports]]
id = "kind"
`
	provIn, store := signBundle(t, provManifest, nil)
	provVI, err := inst.InstantiateVerified(context.Background(), provIn, "")
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	ns := provVI.SignerID.HexLower()
	consumerManifest := `alf_envelope_version = 1
id      = "cascade-consumer"
kind    = "wasm-tool"
version = "0.1.0"
name    = "C"

[[depends]]
handle = "` + ns + `:kind"
`
	consIn := signBundleWithStore(t, consumerManifest, nil, store)
	consVI, err := inst.InstantiateVerified(context.Background(), consIn, "")
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}

	// Sanity: pre-revoke, both Instances are live.
	if inst.LiveCount() != 2 {
		t.Fatalf("live before revoke=%d, want 2", inst.LiveCount())
	}

	// Revoke the provider's key. Cascade: the consumer should also
	// close because its [[depends]] referenced the revoked namespace.
	closed := inst.RevokeByKey(provVI.SignerID)
	if len(closed) != 2 {
		t.Fatalf("closed=%d, want 2 (provider + consumer)", len(closed))
	}

	// Both Instance contexts should now be cancelled.
	select {
	case <-provVI.Instance.Context().Done():
	default:
		t.Error("provider Instance context still active after RevokeByKey")
	}
	select {
	case <-consVI.Instance.Context().Done():
	default:
		t.Error("consumer Instance context still active after cascade RevokeByKey")
	}

	// Audit log carries both reasons — direct + cascade.
	lines := rec.snapshot()
	if len(lines) != 2 {
		t.Fatalf("logger: lines=%d, want 2", len(lines))
	}
	hasDirect := false
	hasCascade := false
	for _, ln := range lines {
		if strings.Contains(ln, "signed by revoked key") {
			hasDirect = true
		}
		if strings.Contains(ln, "depends on revoked provider key") {
			hasCascade = true
		}
	}
	if !hasDirect {
		t.Errorf("logger missing direct-revoke line: %v", lines)
	}
	if !hasCascade {
		t.Errorf("logger missing cascade-revoke line: %v", lines)
	}
}

// Cascading does NOT trigger when a consumer has no dependency on the
// revoked key. A wasm-tool that depends on alf:fs only is untouched
// when a different key is revoked.
func TestRevokeByKey_NoCascadeForUnrelatedConsumer(t *testing.T) {
	handle.ResetMintForTesting()
	reg := handle.NewHandleRegistry()
	inst := NewInstantiator(WithHandleRegistry(reg))
	if err := inst.SeedHandleRegistry(reg); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	consumerManifest := `alf_envelope_version = 1
id      = "alf-fs-consumer"
kind    = "wasm-tool"
version = "0.1.0"
name    = "C"

[[depends]]
handle = "alf:fs"
`
	in, _ := signBundle(t, consumerManifest, nil)
	consVI, err := inst.InstantiateVerified(context.Background(), in, "")
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}

	// Revoke an unrelated key — the consumer's depends are alf-only,
	// not provider-keyed, so cascade should not trigger.
	var randomKey envelope.KeyID
	for i := range randomKey {
		randomKey[i] = byte(0xee)
	}
	closed := inst.RevokeByKey(randomKey)
	if len(closed) != 0 {
		t.Errorf("closed=%d, want 0 (unrelated key)", len(closed))
	}
	if inst.LiveCount() != 1 {
		t.Errorf("live=%d, want 1 (consumer should remain)", inst.LiveCount())
	}
	consVI.Instance.Close()
}

// dependsOnKeys is the helper InstantiateVerified uses to compute
// the per-Instance provider-dependency set. Pin its behaviour
// directly:
//   - alf: namespace excluded
//   - duplicates collapsed
//   - non-hex namespace silently skipped (defensive — schema validation
//     should reject malformed handles, but the runtime must not panic)
func TestDependsOnKeys_PureFunction(t *testing.T) {
	mkManifest := func(deps ...string) string {
		s := `alf_envelope_version = 1
id      = "x"
kind    = "wasm-tool"
version = "0.1.0"
name    = "X"
`
		for _, d := range deps {
			s += "[[depends]]\nhandle = \"" + d + "\"\n"
		}
		return s
	}

	cases := []struct {
		name string
		deps []string
		want int
	}{
		{"no depends", nil, 0},
		{"alf only", []string{"alf:fs", "alf:tool"}, 0},
		{"single provider", []string{"abcdef0123456789:kind"}, 1},
		{"two providers", []string{"abcdef0123456789:k", "fedcba9876543210:k2"}, 2},
		{"duplicate provider", []string{"abcdef0123456789:k1", "abcdef0123456789:k2"}, 1},
		{"mixed alf + provider", []string{"alf:fs", "abcdef0123456789:k"}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toml := mkManifest(c.deps...)
			m, err := envelope.Validate([]byte(toml))
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			got := dependsOnKeys(m)
			if len(got) != c.want {
				t.Errorf("dependsOnKeys=%d entries, want %d", len(got), c.want)
			}
		})
	}
}
