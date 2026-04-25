// Archtest rules for #382 — pin the "facets carry identity, not authority"
// invariants from docs/ARCHITECTURE-SECURITY.md §3.1 + §9 hard rule #10.
//
// Background: pre-0.8.0 sandbox stashed an authority-bearing Policy on ctx
// via PolicyFrom(ctx). #406 razed that surface; #391 made handles the only
// authority carriers; this archtest pins those decisions so a future
// refactor cannot silently re-introduce policy-on-ctx.
package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoPolicyFromCtx enforces the absence of any "extract authority from
// ctx" pattern in the codebase. Under the ocap model, ctx carries Identity
// (audit only) and never Policy (allow/deny rules). Re-introducing
// `PolicyFrom(ctx)` — or any ctx.Value-keyed Policy retrieval — would let
// any code that holds the ctx forge or alter authority. §3.1 + §9.10.
//
// Allowed names: `IdentityFrom(ctx)` (no authority surface). Forbidden:
// `PolicyFrom`, `policyCtxKey`, `WithPolicy(...) context.Context`.
func TestNoPolicyFromCtx(t *testing.T) {
	root := repoRoot()

	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`\bPolicyFrom\s*\(`),
		regexp.MustCompile(`\bpolicyCtxKey\b`),
		regexp.MustCompile(`\bWithPolicy\s*\([^)]*\)\s*context\.Context\b`),
	}

	var violations []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipOcapDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Skip lines that are clearly historical commentary referencing
		// the old surface (e.g. derive.go's "Narrowed from the pre-0.8.0
		// policyCtxKey" docstring). Production code lines that match are
		// what we care about.
		for i, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, re := range forbidden {
				if re.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					violations = append(violations, formatViolation(rel, i+1, line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	for _, v := range violations {
		t.Errorf("policy-on-ctx pattern found: %s\nUnder §3.1 + §9.10, ctx carries identity only. Authority lives in handles forged by Runtime.Instantiate.", v)
	}
}

// TestSandboxIdentityHasNoAuthorityFields enforces that sandbox.Identity —
// the only struct stashed on a sandboxed ctx by Sandbox.Apply — carries
// no allow/deny fields. Adding e.g. an AllowedPaths slice to Identity
// would silently re-introduce policy-on-ctx; this test catches that drift.
//
// Allowed fields (audit + correlation only): CapID, Tier. Adding a new
// field requires updating this test AND justifying why the new field is
// not authority-bearing.
func TestSandboxIdentityHasNoAuthorityFields(t *testing.T) {
	root := repoRoot()
	target := filepath.Join(root, "internal", "sandbox", "sandbox.go")

	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}

	// Match the Identity struct body; reject if any field name suggests
	// authority. The regex captures everything between `type Identity struct {`
	// and the closing brace.
	structRe := regexp.MustCompile(`(?s)type\s+Identity\s+struct\s*\{(.*?)\n\}`)
	m := structRe.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("could not locate Identity struct in sandbox.go — refactor likely; update this test")
	}
	body := m[1]

	// Authority-suggesting field names. Any of these inside Identity is a
	// violation regardless of declared type — semantically they do not
	// belong on identity.
	forbiddenFieldRe := regexp.MustCompile(`(?im)^\s*(Allow\w*|Deny\w*|Permission\w*|Scope\w*|Policy\w*|FilePaths|Networks|Secrets|Rules)\b`)

	for _, line := range strings.Split(body, "\n") {
		// Skip comment-only lines inside the struct body.
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if forbiddenFieldRe.MatchString(line) {
			t.Errorf("sandbox.Identity has an authority-bearing field: %q\n"+
				"Identity is for audit + correlation only. Authority lives in handles, not on ctx.\n"+
				"§3.1: \"It contains NO authority surface — no allow/deny fields, no permissions, no policy.\"",
				strings.TrimSpace(line))
		}
	}
}

// TestMarketplaceHasPermissionNotUsedAsSandboxEnforcement enforces that
// `marketplace.Manager.HasPermission` is not consulted as an enforcement
// gate from inside the sandbox / capability / runtime packages. It may
// legitimately appear in HTTP authorisation layers (controlcenter
// handlers gate which AppSlug can hit which endpoint) — that is auth
// for the request, not sandbox enforcement for the in-process call.
//
// The split exists because pre-0.8.0 the sandbox facets called
// HasPermission inside Apply / Derive paths, and the 0.7.9 audit flagged
// the resulting double-source-of-truth as a confusion attack vector.
// Authority must derive from the verified manifest at forge time; the
// marketplace store is a UX surface for users, not a runtime gate.
func TestMarketplaceHasPermissionNotUsedAsSandboxEnforcement(t *testing.T) {
	root := repoRoot()

	// Packages where HasPermission would mean "policy enforcement" rather
	// than "HTTP request authorisation".
	enforcementSubtrees := []string{
		filepath.Join("internal", "sandbox"),
		filepath.Join("internal", "capability"),
		filepath.Join("internal", "runtime"),
	}

	pat := regexp.MustCompile(`\.HasPermission\s*\(`)

	var violations []string
	for _, sub := range enforcementSubtrees {
		dir := filepath.Join(root, sub)
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if skipOcapDir(info.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for i, line := range strings.Split(string(b), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				if pat.MatchString(line) {
					rel, _ := filepath.Rel(root, path)
					violations = append(violations, formatViolation(rel, i+1, line))
				}
			}
			return nil
		})
	}
	for _, v := range violations {
		t.Errorf("HasPermission used as sandbox enforcement: %s\n"+
			"Authority derives from the verified manifest at forge time (§3.1).\n"+
			"marketplace.HasPermission is a UX surface for the marketplace UI; using it inside sandbox/capability/runtime\n"+
			"creates a parallel auth system in violation of §9 hard rule #10.", v)
	}
}

// formatViolation packages a file/line/source triplet for a t.Errorf message.
func formatViolation(rel string, lineNo int, line string) string {
	src := strings.TrimSpace(line)
	if len(src) > 120 {
		src = src[:117] + "..."
	}
	return fmt.Sprintf("%s:%d: %s", rel, lineNo, src)
}
