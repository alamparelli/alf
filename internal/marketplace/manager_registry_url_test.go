package marketplace

import (
	"errors"
	"testing"
)

// Regression guard for #385-2: the marketplace registry URL must enforce
// HTTPS. Plain http:// is only accepted with the
// ALF_MARKETPLACE_INSECURE=1 dev override; unknown schemes are refused.
func TestValidateRegistryURL(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		insecure string
		want     string
		wantErr  error
	}{
		{name: "empty", raw: "", insecure: "", want: ""},
		{name: "https accepted", raw: "https://market.example", insecure: "", want: "https://market.example"},
		{name: "https with override still fine", raw: "https://market.example", insecure: "1", want: "https://market.example"},
		{name: "http rejected by default", raw: "http://market.example", insecure: "", wantErr: errInsecureRegistry},
		{name: "http rejected when override not 1", raw: "http://market.example", insecure: "true", wantErr: errInsecureRegistry},
		{name: "http accepted with override", raw: "http://market.example", insecure: "1", want: "http://market.example"},
		{name: "whitespace trimmed then accepted", raw: "  https://m.example  ", insecure: "", want: "https://m.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateRegistryURL(tc.raw, tc.insecure)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if got != "" {
					t.Fatalf("on error got = %q, want empty", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateRegistryURL_RejectsBadSchemes(t *testing.T) {
	cases := []string{
		"ftp://market.example",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"://nohost",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			got, err := validateRegistryURL(raw, "1")
			if err == nil {
				t.Fatalf("expected err for %q, got url=%q", raw, got)
			}
			if got != "" {
				t.Fatalf("on error got = %q, want empty", got)
			}
		})
	}
}

func TestValidateRegistryURL_RejectsMissingHost(t *testing.T) {
	got, err := validateRegistryURL("https://", "")
	if err == nil {
		t.Fatalf("expected err for scheme-only URL, got url=%q", got)
	}
}
