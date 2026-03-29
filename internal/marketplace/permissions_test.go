package marketplace

import (
	"os"
	"testing"
)

func TestValidatePermissions_Valid(t *testing.T) {
	err := ValidatePermissions([]string{"storage", "bash", "upload", "clipboard", "events"})
	if err != nil {
		t.Errorf("expected nil error for valid perms, got: %v", err)
	}
}

func TestValidatePermissions_Empty(t *testing.T) {
	err := ValidatePermissions([]string{})
	if err != nil {
		t.Errorf("expected nil error for empty perms, got: %v", err)
	}
}

func TestValidatePermissions_Invalid(t *testing.T) {
	err := ValidatePermissions([]string{"storage", "hack-the-planet"})
	if err == nil {
		t.Error("expected error for unknown permission")
	}
}

func TestValidatePermissions_Nil(t *testing.T) {
	err := ValidatePermissions(nil)
	if err != nil {
		t.Errorf("expected nil error for nil perms, got: %v", err)
	}
}

func TestManager_HasPermission_NoRestrictions(t *testing.T) {
	m := &Manager{
		states: map[string]AppState{"my-app": StateEnabled},
		perms:  make(map[string][]string),
	}
	// App not in perms cache = no restrictions
	if !m.HasPermission("my-app", "bash") {
		t.Error("expected true for app without permission restrictions")
	}
}

func TestManager_HasPermission_Granted(t *testing.T) {
	m := &Manager{
		states: map[string]AppState{"my-app": StateEnabled},
		perms:  map[string][]string{"my-app": {"storage", "bash"}},
	}
	if !m.HasPermission("my-app", "storage") {
		t.Error("expected true for granted permission")
	}
	if !m.HasPermission("my-app", "bash") {
		t.Error("expected true for granted bash permission")
	}
}

func TestManager_HasPermission_Denied(t *testing.T) {
	m := &Manager{
		states: map[string]AppState{"my-app": StateEnabled},
		perms:  map[string][]string{"my-app": {"storage"}},
	}
	if m.HasPermission("my-app", "bash") {
		t.Error("expected false for denied bash permission")
	}
	if m.HasPermission("my-app", "upload") {
		t.Error("expected false for denied upload permission")
	}
}

func TestManager_HasPermission_EmptyPerms(t *testing.T) {
	m := &Manager{
		states: map[string]AppState{"my-app": StateEnabled},
		perms:  map[string][]string{"my-app": {}}, // explicitly empty = deny all
	}
	if m.HasPermission("my-app", "storage") {
		t.Error("expected false for empty permissions list")
	}
}

func TestManager_HasPermission_UnknownApp(t *testing.T) {
	m := &Manager{
		states: make(map[string]AppState),
		perms:  make(map[string][]string),
	}
	// Unknown app = not tracked = allow (internal app)
	if !m.HasPermission("unknown-app", "bash") {
		t.Error("expected true for unknown app (not tracked by marketplace)")
	}
}

func TestManager_GetPermissions(t *testing.T) {
	m := &Manager{
		perms: map[string][]string{"my-app": {"storage", "bash"}},
	}
	perms := m.GetPermissions("my-app")
	if len(perms) != 2 {
		t.Fatalf("expected 2 permissions, got %d", len(perms))
	}

	// Verify it's a copy, not a reference
	perms[0] = "hacked"
	orig := m.GetPermissions("my-app")
	if orig[0] == "hacked" {
		t.Error("GetPermissions should return a copy, not a reference")
	}
}

func TestManager_GetPermissions_Nil(t *testing.T) {
	m := &Manager{
		perms: make(map[string][]string),
	}
	perms := m.GetPermissions("untracked-app")
	if perms != nil {
		t.Errorf("expected nil for untracked app, got %v", perms)
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
		{"nil passthrough", nil, -1}, // nil means no restrictions
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

func TestManager_HasPermission_UntrustedCapped(t *testing.T) {
	// Simulate an untrusted app that declared bash+storage but got capped
	m := &Manager{
		states:  map[string]AppState{"untrusted-app": StateEnabled},
		perms:   map[string][]string{"untrusted-app": CapPermissionsForUntrusted([]string{"storage", "bash"})},
		trusted: map[string]bool{},
	}
	if !m.HasPermission("untrusted-app", "storage") {
		t.Error("storage should be allowed for untrusted app")
	}
	if m.HasPermission("untrusted-app", "bash") {
		t.Error("bash should be denied for untrusted app (capped)")
	}
}

func TestManager_HasPermission_UntrustedNilPerms(t *testing.T) {
	// SEC-002: Untrusted app with nil permissions must get safe defaults, not all-allow
	m := &Manager{
		states:  map[string]AppState{"evil-app": StateEnabled},
		perms:   map[string][]string{"evil-app": {"storage", "events", "clipboard"}}, // what Enable() would set
		trusted: map[string]bool{}, // not trusted
	}
	if m.HasPermission("evil-app", "bash") {
		t.Error("bash should be denied for untrusted app with nil permissions")
	}
	if m.HasPermission("evil-app", "upload") {
		t.Error("upload should be denied for untrusted app with nil permissions")
	}
	if !m.HasPermission("evil-app", "storage") {
		t.Error("storage should be allowed for untrusted app")
	}
}

func TestLoadManifest_StripsTrusted(t *testing.T) {
	// SEC-001: Trusted field must be stripped from manifest on load
	dir := t.TempDir()
	path := dir + "/manifest.json"
	os.WriteFile(path, []byte(`{"name":"evil","slug":"evil","trusted":true}`), 0o644)

	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Trusted {
		t.Error("LoadManifest should strip Trusted field from file")
	}
}
