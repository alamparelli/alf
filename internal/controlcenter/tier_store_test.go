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

func TestFileTierStore_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiers.json")
	store := NewFileTierStore(path)

	tiers := &TiersConfig{
		Tiers: []Tier{
			{Name: "fast", Model: "haiku", Priority: 1, Enabled: true},
			{Name: "smart", Model: "opus", Priority: 2, Enabled: false},
		},
	}

	if err := store.Save(tiers); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(loaded.Tiers) != 2 {
		t.Fatalf("expected 2 tiers, got %d", len(loaded.Tiers))
	}
	if loaded.Tiers[0].Name != "fast" {
		t.Errorf("tier 0 name: got %q, want 'fast'", loaded.Tiers[0].Name)
	}
	if loaded.Tiers[1].Model != "opus" {
		t.Errorf("tier 1 model: got %q, want 'opus'", loaded.Tiers[1].Model)
	}
}

func TestFileTierStore_Current(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiers.json")
	store := NewFileTierStore(path)

	// Before any save, Current returns default.
	cur := store.Current()
	if len(cur.Tiers) != 1 || cur.Tiers[0].Name != "default" {
		t.Error("Current() should return default before any save")
	}

	// After save, Current returns saved value.
	custom := &TiersConfig{
		Tiers: []Tier{{Name: "custom", Model: "haiku", Priority: 0, Enabled: true}},
	}
	store.Save(custom)

	cur = store.Current()
	if cur.Tiers[0].Name != "custom" {
		t.Errorf("Current() after Save: got %q, want 'custom'", cur.Tiers[0].Name)
	}
}

func TestFileTierStore_Reload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiers.json")
	store := NewFileTierStore(path)

	// Save initial tiers.
	store.Save(&TiersConfig{
		Tiers: []Tier{{Name: "v1", Model: "sonnet", Priority: 0, Enabled: true}},
	})

	// Create a second store pointing to same file (simulates external write).
	store2 := NewFileTierStore(path)
	store2.Save(&TiersConfig{
		Tiers: []Tier{{Name: "v2", Model: "opus", Priority: 0, Enabled: true}},
	})

	// Original store still has v1 in memory.
	if store.Current().Tiers[0].Name != "v1" {
		t.Error("Current() should still be v1 before reload")
	}

	// After reload, picks up v2.
	if err := store.Reload(); err != nil {
		t.Fatalf("Reload() error: %v", err)
	}
	if store.Current().Tiers[0].Name != "v2" {
		t.Errorf("Current() after Reload: got %q, want 'v2'", store.Current().Tiers[0].Name)
	}
}

func TestValidateTiers(t *testing.T) {
	tests := []struct {
		name    string
		tiers   TiersConfig
		wantErr bool
	}{
		{"valid", TiersConfig{Tiers: []Tier{{Name: "a", Model: "sonnet"}}}, false},
		{"empty name", TiersConfig{Tiers: []Tier{{Name: "", Model: "sonnet"}}}, true},
		{"bad model", TiersConfig{Tiers: []Tier{{Name: "a", Model: "gpt4"}}}, true},
		{"empty list", TiersConfig{Tiers: []Tier{}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTiers(&tt.tiers)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTiers() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
