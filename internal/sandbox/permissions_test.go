package sandbox

import "testing"

func TestValidatePermissions_Valid(t *testing.T) {
	if err := ValidatePermissions([]string{"storage", "bash", "upload", "clipboard", "events"}); err != nil {
		t.Errorf("expected nil error for valid perms, got: %v", err)
	}
}

func TestValidatePermissions_Empty(t *testing.T) {
	if err := ValidatePermissions([]string{}); err != nil {
		t.Errorf("expected nil error for empty perms, got: %v", err)
	}
}

func TestValidatePermissions_Invalid(t *testing.T) {
	if err := ValidatePermissions([]string{"storage", "hack-the-planet"}); err == nil {
		t.Error("expected error for unknown permission")
	}
}

func TestValidatePermissions_Nil(t *testing.T) {
	if err := ValidatePermissions(nil); err != nil {
		t.Errorf("expected nil error for nil perms, got: %v", err)
	}
}

func TestValidateServices_Valid(t *testing.T) {
	if err := ValidateServices([]string{"openrouter", "openai"}); err != nil {
		t.Errorf("expected nil error for valid services, got: %v", err)
	}
}

func TestValidateServices_Empty(t *testing.T) {
	if err := ValidateServices([]string{""}); err == nil {
		t.Error("expected error for empty service name")
	}
}

func TestValidateServices_PathSeparators(t *testing.T) {
	cases := []string{"bad/name", "bad.name", "bad\\name"}
	for _, s := range cases {
		if err := ValidateServices([]string{s}); err == nil {
			t.Errorf("expected error for service %q", s)
		}
	}
}

func TestCapPermissionsForUntrusted(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  int
	}{
		{"all safe", []string{"storage", "events", "clipboard"}, 3},
		{"mixed", []string{"storage", "bash", "upload", "events"}, 2}, // bash + upload stripped
		{"all dangerous", []string{"bash", "upload"}, 0},
		{"nil passthrough", nil, -1}, // nil means "not declared"
		{"empty", []string{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CapPermissionsForUntrusted(tt.input)
			if tt.want == -1 {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != tt.want {
				t.Errorf("expected %d perms, got %d: %v", tt.want, len(got), got)
			}
		})
	}
}

func TestUntrustedMaxPermissions_IsSubset(t *testing.T) {
	// Guarantees the init() invariant never silently drifts: every entry in the
	// untrusted allow-list MUST be a known permission.
	for p := range UntrustedMaxPermissions {
		if !ValidPermissions[p] {
			t.Errorf("UntrustedMaxPermissions[%q] not in ValidPermissions", p)
		}
	}
}
