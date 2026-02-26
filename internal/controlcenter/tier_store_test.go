package controlcenter

import (
	"path/filepath"
	"testing"
)

func TestFileTierStore_LoadDefault(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTierStore(filepath.Join(dir, "tiers.json"))

	tiers, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(tiers.Tiers) != 1 {
		t.Fatalf("expected 1 default tier, got %d", len(tiers.Tiers))
	}
	if tiers.Tiers[0].Name != "default" {
		t.Errorf("expected default tier name, got %q", tiers.Tiers[0].Name)
	}
}

func TestFileTierStore_Current(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiers.json")
	store := NewFileTierStore(path)

	// Before any file, Current returns default.
	cur := store.Current()
	if len(cur.Tiers) != 1 || cur.Tiers[0].Name != "default" {
		t.Error("Current() should return default before any file write")
	}
}

func TestFileTierStore_Reload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiers.json")
	store := NewFileTierStore(path)

	// Write tiers file directly (simulating user/CLI edit).
	writeJSON(t, path, &TiersConfig{
		Tiers: []Tier{{Name: "v1", Model: "sonnet", Priority: 0, Enabled: true}},
	})

	if err := store.Reload(); err != nil {
		t.Fatalf("Reload() error: %v", err)
	}
	if store.Current().Tiers[0].Name != "v1" {
		t.Errorf("Current() after Reload: got %q, want 'v1'", store.Current().Tiers[0].Name)
	}

	// Write v2 externally and reload.
	writeJSON(t, path, &TiersConfig{
		Tiers: []Tier{{Name: "v2", Model: "opus", Priority: 0, Enabled: true}},
	})

	if err := store.Reload(); err != nil {
		t.Fatalf("Reload() error: %v", err)
	}
	if store.Current().Tiers[0].Name != "v2" {
		t.Errorf("Current() after second Reload: got %q, want 'v2'", store.Current().Tiers[0].Name)
	}
}

func TestFileTierStore_Save(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiers.json")
	store := NewFileTierStore(path)

	cfg := &TiersConfig{
		Tiers: []Tier{
			{Name: "fast", Model: "haiku", Priority: 1, Enabled: true},
			{Name: "smart", Model: "opus", Priority: 2, Enabled: false},
		},
	}

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Current() should reflect saved data immediately.
	cur := store.Current()
	if len(cur.Tiers) != 2 {
		t.Fatalf("expected 2 tiers, got %d", len(cur.Tiers))
	}
	if cur.Tiers[0].Name != "fast" || cur.Tiers[1].Name != "smart" {
		t.Errorf("unexpected tier names: %+v", cur.Tiers)
	}

	// Load() from disk should match.
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() after Save error: %v", err)
	}
	if len(loaded.Tiers) != 2 || loaded.Tiers[0].Model != "haiku" {
		t.Errorf("Load() after Save mismatch: %+v", loaded.Tiers)
	}
}
