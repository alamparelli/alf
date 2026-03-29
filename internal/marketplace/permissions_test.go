package marketplace

import "testing"

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
