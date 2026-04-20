package marketplace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alamparelli/alf/internal/capability"
)

func writeAppFixture(t *testing.T, dataDir string, m Manifest) {
	t.Helper()
	appDir := filepath.Join(dataDir, "apps", m.Slug)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app: %v", err)
	}
	data, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(appDir, "manifest.json"), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestAppCapability_Manifest(t *testing.T) {
	app := AppInfo{
		Manifest: Manifest{
			Slug: "xpost", Name: "XPost", Version: "2.1", Description: "post to X",
			Services: []string{"openrouter", "twitter"},
		},
		State: StateEnabled,
	}
	c := asCapability(app)
	m := c.Manifest()
	if m.ID != capability.ID("xpost") {
		t.Fatalf("ID: got %q", m.ID)
	}
	if m.Kind != capability.KindApp {
		t.Fatalf("Kind: got %v", m.Kind)
	}
	if m.Name != "XPost" || m.Version != "2.1" || m.Description != "post to X" {
		t.Fatalf("Manifest: %+v", m)
	}
	if got := m.Permissions.Secrets; len(got) != 2 || got[0] != "openrouter" || got[1] != "twitter" {
		t.Fatalf("Permissions.Secrets: got %v", got)
	}
}

func TestAppCapability_ExecuteReturnsAppInfo(t *testing.T) {
	app := AppInfo{Manifest: Manifest{Slug: "s", Name: "N"}, State: StateInstalled}
	out, err := asCapability(app).Execute(context.Background(), capability.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got, ok := out.Data.(AppInfo)
	if !ok {
		t.Fatalf("Output.Data should be AppInfo; got %T", out.Data)
	}
	if got.Slug != "s" || got.State != StateInstalled {
		t.Fatalf("AppInfo roundtrip: %+v", got)
	}
}

func TestMirrorInto_RegistersTrackedApps(t *testing.T) {
	dataDir := t.TempDir()
	writeAppFixture(t, dataDir, Manifest{Slug: "a", Name: "A", Version: "1"})
	writeAppFixture(t, dataDir, Manifest{Slug: "b", Name: "B", Version: "2"})
	mgr := NewManager(dataDir)
	// Track both apps — Manager.List only returns tracked (has state).
	mgr.states = map[string]AppState{"a": StateEnabled, "b": StateInstalled}
	mgr.perms = map[string][]string{"a": nil, "b": nil}

	reg := capability.NewRegistry()
	if err := MirrorInto(mgr, reg); err != nil {
		t.Fatalf("MirrorInto: %v", err)
	}
	if reg.Len() != 2 {
		t.Fatalf("Len: want 2, got %d", reg.Len())
	}
	apps := reg.ByKind(capability.KindApp)
	if len(apps) != 2 {
		t.Fatalf("ByKind(App): want 2, got %d", len(apps))
	}
	if _, ok := reg.Get("a"); !ok {
		t.Error("a not mirrored")
	}
	if _, ok := reg.Get("b"); !ok {
		t.Error("b not mirrored")
	}
}

func TestMirrorInto_SkipsUntrackedLocalApps(t *testing.T) {
	dataDir := t.TempDir()
	writeAppFixture(t, dataDir, Manifest{Slug: "local-only", Name: "Local"})
	mgr := NewManager(dataDir) // no state entries → not tracked
	reg := capability.NewRegistry()
	if err := MirrorInto(mgr, reg); err != nil {
		t.Fatalf("MirrorInto: %v", err)
	}
	if reg.Len() != 0 {
		t.Fatalf("Len: want 0 (untracked app should not mirror), got %d", reg.Len())
	}
}

func TestMirrorInto_IdempotentOnReinstall(t *testing.T) {
	dataDir := t.TempDir()
	writeAppFixture(t, dataDir, Manifest{Slug: "s", Name: "Old", Version: "1"})
	mgr := NewManager(dataDir)
	mgr.states = map[string]AppState{"s": StateEnabled}
	mgr.perms = map[string][]string{"s": nil}

	reg := capability.NewRegistry()
	if err := MirrorInto(mgr, reg); err != nil {
		t.Fatalf("first MirrorInto: %v", err)
	}

	// Simulate an in-place update: rewrite the manifest, re-mirror.
	writeAppFixture(t, dataDir, Manifest{Slug: "s", Name: "New", Version: "2"})
	if err := MirrorInto(mgr, reg); err != nil {
		t.Fatalf("second MirrorInto: %v", err)
	}
	if reg.Len() != 1 {
		t.Fatalf("Len: want 1, got %d", reg.Len())
	}
	c, _ := reg.Get("s")
	if c.Manifest().Name != "New" || c.Manifest().Version != "2" {
		t.Fatalf("Replace should pick up manifest update: %+v", c.Manifest())
	}
}

func TestMirrorInto_NilSafe(t *testing.T) {
	reg := capability.NewRegistry()
	if err := MirrorInto(nil, reg); err != nil {
		t.Fatalf("nil mgr: %v", err)
	}
	if err := MirrorInto(NewManager(t.TempDir()), nil); err != nil {
		t.Fatalf("nil reg: %v", err)
	}
}
