package envelope

import (
	"errors"
	"testing"
)

// TestValidate_HTTPScopes_HappyPath pins #421 Wave 1's headline:
// [[http.scopes]] now parses instead of returning ErrBlockDeferred,
// and a well-formed scope round-trips into Manifest.HTTP.Scopes.
func TestValidate_HTTPScopes_HappyPath(t *testing.T) {
	input := validManifest() + `
[[http.scopes]]
host = "openlibrary.org"

[[http.scopes]]
host        = "www.googleapis.com"
path_prefix = "/books/v1"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got, want := len(m.HTTP.Scopes), 2; got != want {
		t.Fatalf("len(HTTP.Scopes) = %d, want %d", got, want)
	}
	if m.HTTP.Scopes[0].Host != "openlibrary.org" {
		t.Errorf("Scopes[0].Host = %q, want %q", m.HTTP.Scopes[0].Host, "openlibrary.org")
	}
	if m.HTTP.Scopes[0].PathPrefix != "" {
		t.Errorf("Scopes[0].PathPrefix = %q, want empty", m.HTTP.Scopes[0].PathPrefix)
	}
	if m.HTTP.Scopes[1].Host != "www.googleapis.com" {
		t.Errorf("Scopes[1].Host = %q, want %q", m.HTTP.Scopes[1].Host, "www.googleapis.com")
	}
	if m.HTTP.Scopes[1].PathPrefix != "/books/v1" {
		t.Errorf("Scopes[1].PathPrefix = %q, want %q", m.HTTP.Scopes[1].PathPrefix, "/books/v1")
	}
}

// TestValidate_HTTPScopes_EmptyBlockIsOK pins that a manifest with
// [http] but no [[http.scopes]] entries is accepted (equivalent to
// omitting the block entirely — Manifest.HTTP.Scopes is nil/empty).
func TestValidate_HTTPScopes_EmptyBlockIsOK(t *testing.T) {
	input := validManifest() + "\n[http]\n"
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.HTTP.Scopes) != 0 {
		t.Errorf("len(HTTP.Scopes) = %d, want 0", len(m.HTTP.Scopes))
	}
}

// TestValidate_HTTPScopes_HostLowercaseNormalised pins the case-insensitive
// normalisation decision: a manifest declaring "OpenLibrary.org" is
// canonicalised to "openlibrary.org" at parse time so that the forge
// (Wave 3) and the runtime (Wave 2) compare lowercase end-to-end.
func TestValidate_HTTPScopes_HostLowercaseNormalised(t *testing.T) {
	input := validManifest() + `
[[http.scopes]]
host = "OpenLibrary.ORG"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := m.HTTP.Scopes[0].Host; got != "openlibrary.org" {
		t.Errorf("Host = %q, want lowercase %q", got, "openlibrary.org")
	}
}

// TestValidate_HTTPScopes_HostWithPort pins the port-custom decision:
// hosts may carry a ":port" suffix in the 1..65535 range.
func TestValidate_HTTPScopes_HostWithPort(t *testing.T) {
	input := validManifest() + `
[[http.scopes]]
host = "homelab.local:8443"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got := m.HTTP.Scopes[0].Host; got != "homelab.local:8443" {
		t.Errorf("Host = %q, want %q", got, "homelab.local:8443")
	}
}

// TestValidate_HTTPScopes_HostMissing pins that an empty host is a
// parse-time error (a scope with no host is a programmer mistake).
func TestValidate_HTTPScopes_HostMissing(t *testing.T) {
	input := validManifest() + `
[[http.scopes]]
host = ""
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrHTTPScopeHostEmpty) {
		t.Errorf("got %v, want ErrHTTPScopeHostEmpty", err)
	}
}

// TestValidate_HTTPScopes_RejectsWildcardHost pins #421's "no wildcard
// hosts" rule: any `*` in the host is a parse error, period. The
// existing handle.HTTPScope code happens to support "*.example.com"
// patterns but the envelope never populates them — the gate is here.
func TestValidate_HTTPScopes_RejectsWildcardHost(t *testing.T) {
	for _, host := range []string{"*.example.com", "*example.com", "ex*.com"} {
		input := validManifest() + "\n[[http.scopes]]\nhost = \"" + host + "\"\n"
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrHTTPScopeHostMalformed) {
			t.Errorf("host=%q: got %v, want ErrHTTPScopeHostMalformed", host, err)
		}
	}
}

// TestValidate_HTTPScopes_RejectsSchemeOrPath pins that the host field
// is a bare hostname — full URLs (with scheme or path) are rejected.
// Forces the operator to declare scheme and path through the
// path_prefix field; the scheme is implicit (HTTPS-only at runtime).
func TestValidate_HTTPScopes_RejectsSchemeOrPath(t *testing.T) {
	for _, host := range []string{
		"https://openlibrary.org",
		"http://openlibrary.org",
		"openlibrary.org/books",
		"/openlibrary.org",
	} {
		input := validManifest() + "\n[[http.scopes]]\nhost = \"" + host + "\"\n"
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrHTTPScopeHostMalformed) {
			t.Errorf("host=%q: got %v, want ErrHTTPScopeHostMalformed", host, err)
		}
	}
}

// TestValidate_HTTPScopes_RejectsPortOutOfRange pins that ports must
// be in 1..65535 and parse cleanly (no leading zero, no "+0", no
// non-digit chars).
func TestValidate_HTTPScopes_RejectsPortOutOfRange(t *testing.T) {
	for _, host := range []string{
		"homelab.local:0",
		"homelab.local:65536",
		"homelab.local:99999",
		"homelab.local:-1",
		"homelab.local:abc",
		"homelab.local:8443x",
		"homelab.local:08443",
		"homelab.local:",
	} {
		input := validManifest() + "\n[[http.scopes]]\nhost = \"" + host + "\"\n"
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrHTTPScopePortOutOfRange) {
			t.Errorf("host=%q: got %v, want ErrHTTPScopePortOutOfRange", host, err)
		}
	}
}

// TestValidate_HTTPScopes_PathPrefixHappyPath pins the documented
// shape — must start with "/", literal prefix only.
func TestValidate_HTTPScopes_PathPrefixHappyPath(t *testing.T) {
	for _, prefix := range []string{"/", "/books", "/books/v1", "/api/v2/users"} {
		input := validManifest() + "\n[[http.scopes]]\nhost = \"example.com\"\npath_prefix = \"" + prefix + "\"\n"
		m, err := Validate([]byte(input))
		if err != nil {
			t.Errorf("prefix=%q: Validate failed: %v", prefix, err)
			continue
		}
		if got := m.HTTP.Scopes[0].PathPrefix; got != prefix {
			t.Errorf("prefix=%q: got %q", prefix, got)
		}
	}
}

// TestValidate_HTTPScopes_RejectsPathPrefixWithoutLeadingSlash pins
// that path_prefix without "/" is a parse error — defensive against
// operators copy-pasting from URL bars (which may strip leading "/").
func TestValidate_HTTPScopes_RejectsPathPrefixWithoutLeadingSlash(t *testing.T) {
	input := validManifest() + `
[[http.scopes]]
host        = "example.com"
path_prefix = "books/v1"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrHTTPScopePathPrefixMalformed) {
		t.Errorf("got %v, want ErrHTTPScopePathPrefixMalformed", err)
	}
}

// TestValidate_HTTPScopes_RejectsPathPrefixGlobChars pins that glob
// and regex metacharacters are refused in path_prefix — the schema
// promises literal prefix matching, so meta chars must not sneak in.
//
// Backslash is tested via a TOML literal string ('…') because basic
// strings ("…") interpret escape sequences and would fail at the TOML
// layer rather than reach our validator.
func TestValidate_HTTPScopes_RejectsPathPrefixGlobChars(t *testing.T) {
	cases := []struct {
		name   string
		toml   string
		prefix string
	}{
		{"star", `path_prefix = "/books/*"`, "/books/*"},
		{"question", `path_prefix = "/books/?"`, "/books/?"},
		{"brackets", `path_prefix = "/books/[abc]"`, "/books/[abc]"},
		{"braces", `path_prefix = "/books/{a,b}"`, "/books/{a,b}"},
		{"backslash", `path_prefix = '/books\v1'`, `/books\v1`},
	}
	for _, tc := range cases {
		input := validManifest() + "\n[[http.scopes]]\nhost = \"example.com\"\n" + tc.toml + "\n"
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrHTTPScopePathPrefixMalformed) {
			t.Errorf("%s (prefix=%q): got %v, want ErrHTTPScopePathPrefixMalformed", tc.name, tc.prefix, err)
		}
	}
}

// TestValidate_HTTPScopes_RejectsPathPrefixTraversal pins symmetry
// with the FS path traversal rule: ".." segments are refused.
func TestValidate_HTTPScopes_RejectsPathPrefixTraversal(t *testing.T) {
	for _, prefix := range []string{"/..", "/api/../admin", "/../etc"} {
		input := validManifest() + "\n[[http.scopes]]\nhost = \"example.com\"\npath_prefix = \"" + prefix + "\"\n"
		_, err := Validate([]byte(input))
		if !errors.Is(err, ErrHTTPScopePathPrefixTraversal) {
			t.Errorf("prefix=%q: got %v, want ErrHTTPScopePathPrefixTraversal", prefix, err)
		}
	}
}

// TestValidate_HTTPScopes_RejectsDuplicates pins that (host, path_prefix)
// pairs are unique within a manifest — duplicate declarations are a
// parse-time error so operators can't accidentally double-declare a
// scope and confuse later audits.
func TestValidate_HTTPScopes_RejectsDuplicates(t *testing.T) {
	input := validManifest() + `
[[http.scopes]]
host        = "example.com"
path_prefix = "/api"

[[http.scopes]]
host        = "example.com"
path_prefix = "/api"
`
	_, err := Validate([]byte(input))
	if !errors.Is(err, ErrHTTPScopeDuplicate) {
		t.Errorf("got %v, want ErrHTTPScopeDuplicate", err)
	}
}

// TestValidate_HTTPScopes_SameHostDifferentPathOK pins the converse:
// the same host with different path_prefix values is two distinct
// scopes — neither subsumes the other, both must be authorised
// individually.
func TestValidate_HTTPScopes_SameHostDifferentPathOK(t *testing.T) {
	input := validManifest() + `
[[http.scopes]]
host        = "api.github.com"
path_prefix = "/repos"

[[http.scopes]]
host        = "api.github.com"
path_prefix = "/users"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.HTTP.Scopes) != 2 {
		t.Errorf("len(Scopes) = %d, want 2", len(m.HTTP.Scopes))
	}
}
