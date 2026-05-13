package updater

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// TestCosignVerifier_DefaultsApplied pins that an empty
// CosignVerifier picks up the documented defaults — issuer +
// identity-regexp must not silently miss when the daemon's wiring
// forgets to set them.
func TestCosignVerifier_DefaultsApplied(t *testing.T) {
	var captured []string
	v := &CosignVerifier{
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			captured = append([]string{name}, args...)
			return nil, nil
		},
	}
	if err := v.Verify(context.Background(), "ghcr.io/alamparelli/alf", "sha256:abc"); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// Argument plumbing — the issuer + identity defaults must reach
	// the cosign command.
	joined := strings.Join(captured, " ")
	if !strings.Contains(joined, DefaultCosignIssuer) {
		t.Errorf("default issuer missing from cosign args: %s", joined)
	}
	if !strings.Contains(joined, DefaultCosignIdentityRegex) {
		t.Errorf("default identity regex missing: %s", joined)
	}
	if !strings.Contains(joined, "ghcr.io/alamparelli/alf@sha256:abc") {
		t.Errorf("repo@digest missing: %s", joined)
	}
	if captured[0] != "cosign" {
		t.Errorf("default binary: got %q, want cosign", captured[0])
	}
}

// TestCosignVerifier_CustomBinary pins that a non-empty Binary
// field overrides the "cosign" default — useful for non-PATH
// installs (e.g. the Dockerfile baking it under /usr/local/bin/cosign).
func TestCosignVerifier_CustomBinary(t *testing.T) {
	var captured string
	v := &CosignVerifier{
		Binary: "/opt/cosign",
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			captured = name
			return nil, nil
		},
	}
	v.Verify(context.Background(), "r", "d")
	if captured != "/opt/cosign" {
		t.Errorf("got %q, want /opt/cosign", captured)
	}
}

// TestCosignVerifier_BinaryNotFoundIsTyped pins that the missing-
// binary path returns ErrCosignBinaryNotFound rather than wrapping
// the cosign output. The daemon branches on this to log a
// remediation pointer.
func TestCosignVerifier_BinaryNotFoundIsTyped(t *testing.T) {
	v := &CosignVerifier{
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return nil, exec.ErrNotFound
		},
	}
	err := v.Verify(context.Background(), "r", "d")
	if !errors.Is(err, ErrCosignBinaryNotFound) {
		t.Errorf("got %v, want ErrCosignBinaryNotFound", err)
	}
}

// TestCosignVerifier_VerifyFailureCaptured pins that any non-zero
// exit (untrusted signer, missing signature, etc.) is wrapped with
// ErrCosignVerifyFailed AND embeds cosign's stderr for diagnostics.
func TestCosignVerifier_VerifyFailureCaptured(t *testing.T) {
	v := &CosignVerifier{
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte("Error: no matching signatures found\n"), errors.New("exit 1")
		},
	}
	err := v.Verify(context.Background(), "r", "d")
	if !errors.Is(err, ErrCosignVerifyFailed) {
		t.Errorf("got %v, want ErrCosignVerifyFailed", err)
	}
	if !strings.Contains(err.Error(), "no matching signatures") {
		t.Errorf("stderr not captured in error: %v", err)
	}
}

// TestCheckOnce_CosignVerifyFailureBlocksNotify pins #403's
// primary acceptance: a stubbed registry returns a tag, the cosign
// stub returns an error, the notify callback is NOT called and
// LatestVersion stays empty. The audit log surfaces the failure
// for the operator.
func TestCheckOnce_CosignVerifyFailureBlocksNotify(t *testing.T) {
	srv := httptest.NewServer(registryHandler([]string{"1.0.0", "2.0.0", "latest"}))
	defer srv.Close()

	notifyCalled := false
	c := newTestChecker(t, "1.0.0", srv)
	c.notify = func(cur, lat, dig string) { notifyCalled = true }

	c.SetCosignVerifier(&CosignVerifier{
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte("Error: signature verification failed"), errors.New("exit 1")
		},
	})

	c.CheckOnce()

	if notifyCalled {
		t.Error("notify fired despite cosign verify failure — #403 says it must be blocked")
	}
	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion=%q, want empty (refused after verify failure)", got)
	}
	if got := c.LatestDigest(); got != "" {
		t.Errorf("LatestDigest=%q, want empty (not committed without verification)", got)
	}
}

// TestCheckOnce_CosignVerifySuccessFiresNotifyWithDigest pins the
// success path: cosign returns clean, notify fires WITH the
// digest, LatestDigest is populated for the installer to consume.
func TestCheckOnce_CosignVerifySuccessFiresNotifyWithDigest(t *testing.T) {
	wantDigest := "sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	srv := httptest.NewServer(registryHandlerWithDigest([]string{"1.0.0", "2.0.0", "latest"}, wantDigest))
	defer srv.Close()

	var notifiedDigest string
	c := newTestChecker(t, "1.0.0", srv)
	c.notify = func(cur, lat, dig string) { notifiedDigest = dig }

	c.SetCosignVerifier(&CosignVerifier{
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte("Verification for ghcr.io/test/repo@sha256:... -- The following checks were performed...\n"), nil
		},
	})

	c.CheckOnce()

	if notifiedDigest != wantDigest {
		t.Errorf("notify digest=%q, want %q", notifiedDigest, wantDigest)
	}
	if got := c.LatestDigest(); got != wantDigest {
		t.Errorf("LatestDigest=%q, want %q", got, wantDigest)
	}
}

// TestCheckOnce_DigestResolveFailureBlocksNotify pins that a tag
// in /tags/list with no manifest at all (a registry inconsistency)
// is treated like a verify failure — refuse to notify rather than
// proceeding without a digest pin.
func TestCheckOnce_DigestResolveFailureBlocksNotify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/token"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"token":"x"}`))
		case strings.Contains(r.URL.Path, "/tags/list"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"name":"test/repo","tags":["2.0.0","latest"]}`))
		default:
			// /manifests/* returns 404 — registry inconsistency
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	notifyCalled := false
	c := newTestChecker(t, "1.0.0", srv)
	c.notify = func(cur, lat, dig string) { notifyCalled = true }

	c.CheckOnce()

	if notifyCalled {
		t.Error("notify fired despite digest resolution failure")
	}
	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion=%q, want empty", got)
	}
}

// TestCheckOnce_NoVerifierRefusesNotify pins the SEC-080-004
// fail-closed posture: a nil verifier is a wiring bug, not a
// degradation path. The checker refuses to notify and leaves
// latestVersion empty. The production opt-out path
// (ALF_DISABLE_COSIGN_VERIFY=1) wires PermissiveCosignVerifier
// explicitly — see cmd/alf-daemon/main.go — so the nil branch is
// never reached in a correctly-wired daemon.
func TestCheckOnce_NoVerifierRefusesNotify(t *testing.T) {
	srv := httptest.NewServer(registryHandler([]string{"1.0.0", "2.0.0", "latest"}))
	defer srv.Close()

	notifyCalled := false
	c := newTestChecker(t, "1.0.0", srv)
	c.notify = func(cur, lat, dig string) { notifyCalled = true }
	// Override the test default: clear the verifier to simulate
	// a daemon-wiring regression that left the field nil.
	c.SetCosignVerifier(nil)

	c.CheckOnce()

	if notifyCalled {
		t.Error("notify fired despite nil verifier — SEC-080-004 regression (fail-open)")
	}
	if got := c.LatestVersion(); got != "" {
		t.Errorf("LatestVersion=%q, want empty (nil verifier must refuse to notify)", got)
	}
	if got := c.LatestDigest(); got != "" {
		t.Errorf("LatestDigest=%q, want empty (nil verifier must not commit)", got)
	}
}

// TestCheckOnce_PermissiveVerifierNotifies pins the documented
// opt-out path (ALF_DISABLE_COSIGN_VERIFY=1): PermissiveCosignVerifier
// makes the check proceed without spawning cosign while keeping the
// "verifier is always wired" invariant intact.
func TestCheckOnce_PermissiveVerifierNotifies(t *testing.T) {
	srv := httptest.NewServer(registryHandler([]string{"1.0.0", "2.0.0", "latest"}))
	defer srv.Close()

	notifiedTag := ""
	c := newTestChecker(t, "1.0.0", srv)
	c.notify = func(cur, lat, dig string) { notifiedTag = lat }
	c.SetCosignVerifier(PermissiveCosignVerifier())

	c.CheckOnce()

	if notifiedTag != "2.0.0" {
		t.Errorf("notify tag=%q, want 2.0.0 (permissive verifier path)", notifiedTag)
	}
}
