package updater

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultCosignIssuer is the OIDC issuer used by GitHub Actions for
// keyless cosign signatures — pinned to the Sigstore-recommended URL.
// All `cosign sign --yes …` invocations from a GitHub Actions workflow
// produce a Fulcio cert whose `Issuer` extension equals this value.
const DefaultCosignIssuer = "https://token.actions.githubusercontent.com"

// DefaultCosignIdentityRegex matches the certificate-identity field
// produced when alf's release workflow signs an image. The cert
// subject takes the shape:
//
//	https://github.com/alamparelli/alf/.github/workflows/release.yml@refs/tags/v0.8.0
//
// The regex anchors the prefix and matches every tag-driven release
// ref. Matching the prefix is sufficient: cosign already verifies
// the cert's Fulcio chain + the OIDC issuer, so a different repo's
// workflow producing a Fulcio cert cannot pass this regex.
const DefaultCosignIdentityRegex = `^https://github\.com/alamparelli/alf/\.github/workflows/release\.yml@`

// ErrCosignBinaryNotFound is returned when the cosign binary is
// absent from PATH (or the configured Binary path). Distinct from
// "verification failed" so the operator sees a clear remediation
// path: "the runtime image needs cosign installed".
var ErrCosignBinaryNotFound = errors.New("cosign: binary not found on PATH")

// ErrCosignVerifyFailed is returned when cosign exits non-zero for
// a verify call. The error message embeds cosign's stderr so the
// operator can diagnose (untrusted signer, missing signature,
// expired cert, etc.) without re-running the command manually.
var ErrCosignVerifyFailed = errors.New("cosign: verify failed")

// PermissiveCosignVerifier returns a verifier whose Verify always
// succeeds without spawning cosign. This is the documented escape
// hatch for operators who explicitly opted out of signature
// verification via ALF_DISABLE_COSIGN_VERIFY=1 (homelab dev only).
//
// Rationale (SEC-080-004): the Checker now refuses to notify when
// its verifier is nil — fail-closed against a silent wiring
// regression. The opt-out path must therefore wire an explicit
// permissive verifier rather than leaving the field nil, so the
// operator's choice is visible in the wiring and a future refactor
// that drops the wiring fails closed instead of silently
// proceeding without verification.
func PermissiveCosignVerifier() *CosignVerifier {
	return &CosignVerifier{
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return nil, nil
		},
	}
}

// CosignVerifier wraps the cosign CLI for image signature
// verification. The exec seam is injectable so tests stub the
// command without spawning processes.
//
// The verifier owns no state beyond config — every Verify call is
// independent. Construct once at daemon boot, reuse across update
// checks.
type CosignVerifier struct {
	// Binary is the cosign executable path. Empty defaults to
	// "cosign" on PATH (resolved at exec time, not construction).
	Binary string

	// Issuer is the OIDC issuer URL that the signing cert must
	// declare. Empty defaults to DefaultCosignIssuer.
	Issuer string

	// IdentityRegex is the regex passed to
	// --certificate-identity-regexp. Empty defaults to
	// DefaultCosignIdentityRegex.
	IdentityRegex string

	// Run is the exec seam. Nil = real exec.CommandContext +
	// CombinedOutput. Tests inject a stub that returns canned
	// stdout/err pairs without touching the OS.
	//
	// Contract: returns the combined stdout/stderr output and any
	// exit error. A non-zero exit is signalled via err != nil;
	// stdout is captured for diagnostics.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Verify checks that repo@digest carries a Sigstore-signed
// signature whose Fulcio cert chains to the configured Issuer +
// IdentityRegex.
//
// Returns:
//   - nil on successful verification.
//   - ErrCosignBinaryNotFound when cosign is absent (image lacks
//     the install). Operator action: install cosign.
//   - ErrCosignVerifyFailed wrapping cosign's stderr otherwise
//     (untrusted signer, missing signature, expired cert, network
//     to Rekor). Operator action: investigate.
func (v *CosignVerifier) Verify(ctx context.Context, repo, digest string) error {
	bin := v.Binary
	if bin == "" {
		bin = "cosign"
	}
	issuer := v.Issuer
	if issuer == "" {
		issuer = DefaultCosignIssuer
	}
	idRe := v.IdentityRegex
	if idRe == "" {
		idRe = DefaultCosignIdentityRegex
	}

	args := []string{
		"verify",
		"--certificate-oidc-issuer", issuer,
		"--certificate-identity-regexp", idRe,
		repo + "@" + digest,
	}

	out, err := v.run(ctx, bin, args...)
	if err == nil {
		return nil
	}

	// Distinguish "binary missing" from "verify failed". exec.ErrNotFound
	// is what os/exec returns when LookPath fails; surface as a typed
	// error so the daemon's update path can log a clearer remediation.
	if errors.Is(err, exec.ErrNotFound) {
		return ErrCosignBinaryNotFound
	}
	// Verify outcomes (untrusted signer, missing sig, etc.) all bubble
	// up as exit errors with diagnostic stderr. Wrap with the typed
	// sentinel so callers can branch on errors.Is.
	return fmt.Errorf("%w: %s", ErrCosignVerifyFailed, strings.TrimSpace(string(out)))
}

// run dispatches to the configured Run hook or the real exec
// implementation. Kept as a method so the public field stays
// optional and the default path is one path-of-light.
func (v *CosignVerifier) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if v.Run != nil {
		return v.Run(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}
