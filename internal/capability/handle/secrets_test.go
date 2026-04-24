package handle

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// stubReader is a minimal in-memory SecretsReader for tests. Lookup is
// by exact name; missing entries produce an empty string so the handle
// surfaces ErrSecretNotFound uniformly.
type stubReader map[string]string

func (s stubReader) GetSecret(name string) (string, error) { return s[name], nil }

// erroringReader simulates a vault transport error so we can verify the
// handle surfaces it verbatim (doesn't swallow).
type erroringReader struct{ err error }

func (e erroringReader) GetSecret(string) (string, error) { return "", e.err }

func TestSecretsScope_ExactMatch(t *testing.T) {
	s := SecretsScope{Names: []string{"github_token"}}
	if !s.Allows("github_token") {
		t.Error("exact match denied")
	}
	if s.Allows("other_token") {
		t.Error("non-matching accepted")
	}
	if s.Allows("") {
		t.Error("empty name accepted")
	}
}

func TestSecretsScope_WildcardSuffix(t *testing.T) {
	s := SecretsScope{Names: []string{"github_*"}}
	cases := []struct {
		name string
		want bool
	}{
		{"github_token", true},
		{"github_user", true},
		{"github_", true},   // empty body after prefix — still matches "github_" the prefix
		{"github", false},   // no trailing underscore
		{"not_github_", false},
	}
	for _, tc := range cases {
		got := s.Allows(tc.name)
		if got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSecretsScope_EmptyDeniesAll(t *testing.T) {
	s := SecretsScope{}
	if s.Allows("anything") {
		t.Error("empty Names must deny all")
	}
}

func TestSecretsScope_BareWildcardRejected(t *testing.T) {
	// A pattern of just "*" would match everything — we refuse that
	// degenerate case by requiring a non-empty prefix.
	s := SecretsScope{Names: []string{"*"}}
	if s.Allows("anything") {
		t.Error("bare * pattern must not match — prefix must be non-empty")
	}
}

func TestSecretsHandle_GetInScope(t *testing.T) {
	reader := stubReader{"github_token": "ghp_xxx"}
	h := NewSecretsHandle("cap", SecretsScope{Names: []string{"github_token"}}, reader)
	inst := NewInstance(context.Background(), "cap", Grants{Secrets: h})
	defer inst.Close()

	val, err := inst.Secrets.Get(context.Background(), "github_token")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "ghp_xxx" {
		t.Errorf("got %q, want ghp_xxx", val)
	}
}

func TestSecretsHandle_OutOfScope(t *testing.T) {
	reader := stubReader{"github_token": "ghp_xxx", "api_key": "secret"}
	h := NewSecretsHandle("cap", SecretsScope{Names: []string{"github_token"}}, reader)
	inst := NewInstance(context.Background(), "cap", Grants{Secrets: h})
	defer inst.Close()

	_, err := inst.Secrets.Get(context.Background(), "api_key")
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("want ErrOutOfScope, got %v", err)
	}
}

func TestSecretsHandle_WildcardMatch(t *testing.T) {
	reader := stubReader{
		"github_token": "ghp_xxx",
		"github_user":  "alice",
		"gitlab_token": "glp_yyy",
	}
	h := NewSecretsHandle("cap", SecretsScope{Names: []string{"github_*"}}, reader)
	inst := NewInstance(context.Background(), "cap", Grants{Secrets: h})
	defer inst.Close()

	if v, err := inst.Secrets.Get(context.Background(), "github_token"); err != nil || v != "ghp_xxx" {
		t.Errorf("github_token: v=%q err=%v", v, err)
	}
	if v, err := inst.Secrets.Get(context.Background(), "github_user"); err != nil || v != "alice" {
		t.Errorf("github_user: v=%q err=%v", v, err)
	}
	if _, err := inst.Secrets.Get(context.Background(), "gitlab_token"); !errors.Is(err, ErrOutOfScope) {
		t.Errorf("gitlab_token: want ErrOutOfScope, got %v", err)
	}
}

func TestSecretsHandle_MissingSecret(t *testing.T) {
	h := NewSecretsHandle("cap", SecretsScope{Names: []string{"absent"}}, stubReader{})
	inst := NewInstance(context.Background(), "cap", Grants{Secrets: h})
	defer inst.Close()

	_, err := inst.Secrets.Get(context.Background(), "absent")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("want ErrSecretNotFound, got %v", err)
	}
}

func TestSecretsHandle_ReaderErrorSurfaced(t *testing.T) {
	sentinel := errors.New("vault down")
	h := NewSecretsHandle("cap", SecretsScope{Names: []string{"k"}}, erroringReader{err: sentinel})
	inst := NewInstance(context.Background(), "cap", Grants{Secrets: h})
	defer inst.Close()

	_, err := inst.Secrets.Get(context.Background(), "k")
	if !errors.Is(err, sentinel) {
		t.Fatalf("reader error must be surfaced, got %v", err)
	}
}

func TestSecretsHandle_Revocation(t *testing.T) {
	reader := stubReader{"k": "v"}
	h := NewSecretsHandle("cap", SecretsScope{Names: []string{"k"}}, reader)
	inst := NewInstance(context.Background(), "cap", Grants{Secrets: h})

	start := time.Now()
	inst.Close()

	_, err := inst.Secrets.Get(context.Background(), "k")
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("want ErrRevoked, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("revocation took %v, want <100ms", elapsed)
	}
}

func TestSecretsHandle_CtxCancellation(t *testing.T) {
	reader := stubReader{"k": "v"}
	h := NewSecretsHandle("cap", SecretsScope{Names: []string{"k"}}, reader)
	inst := NewInstance(context.Background(), "cap", Grants{Secrets: h})
	defer inst.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := inst.Secrets.Get(ctx, "k")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestSecretsHandle_NonSerializable(t *testing.T) {
	h := NewSecretsHandle("cap", SecretsScope{}, stubReader{})
	if _, err := json.Marshal(h); err == nil {
		t.Fatal("SecretsHandle must not be JSON-serializable")
	}
}

func TestSecretsHandle_Owner(t *testing.T) {
	h := NewSecretsHandle("cap-xyz", SecretsScope{}, nil)
	if got := h.Owner(); string(got) != "cap-xyz" {
		t.Errorf("Owner()=%q, want cap-xyz", got)
	}
}
