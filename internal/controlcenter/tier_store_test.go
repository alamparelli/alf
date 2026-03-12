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
	if len(tiers.Tiers) < 4 {
		t.Fatalf("expected at least 4 default tiers, got %d", len(tiers.Tiers))
	}
	if tiers.Tiers[0].Name != "haiku" {
		t.Errorf("expected first tier 'haiku', got %q", tiers.Tiers[0].Name)
	}
	if tiers.Tiers[0].Effort != "low" {
		t.Errorf("expected effort 'low', got %q", tiers.Tiers[0].Effort)
	}
	if !tiers.Tiers[0].Routable {
		t.Error("expected haiku tier to be routable")
	}
}

func TestFileTierStore_Current(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tiers.json")
	store := NewFileTierStore(path)

	// Before any file, Current returns default.
	cur := store.Current()
	if len(cur.Tiers) < 4 || cur.Tiers[0].Name != "haiku" {
		t.Error("Current() should return default tiers before any file write")
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

func TestTiersPath(t *testing.T) {
	got := TiersPath("/opt/alf/config.d")
	want := "/opt/alf/config.d/tiers.json"
	if got != want {
		t.Errorf("TiersPath() = %q, want %q", got, want)
	}
}

func TestTiersPathFromConfig(t *testing.T) {
	configDir := "/opt/alf/config.d"

	tests := []struct {
		name     string
		cfg      *Config
		wantPath string
	}{
		{"nil config", nil, "/opt/alf/config.d/tiers.json"},
		{"empty TiersFile", &Config{}, "/opt/alf/config.d/tiers.json"},
		{"relative TiersFile", &Config{TiersFile: "tiers2.json"}, "/opt/alf/config.d/tiers2.json"},
		{"absolute TiersFile", &Config{TiersFile: "/custom/path/tiers.json"}, "/custom/path/tiers.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TiersPathFromConfig(configDir, tt.cfg)
			if got != tt.wantPath {
				t.Errorf("TiersPathFromConfig() = %q, want %q", got, tt.wantPath)
			}
		})
	}
}

func TestFileTierStore_PathAndSetPath(t *testing.T) {
	dir := t.TempDir()
	path1 := filepath.Join(dir, "tiers.json")
	path2 := filepath.Join(dir, "tiers2.json")

	store := NewFileTierStore(path1)
	if got := store.Path(); got != path1 {
		t.Fatalf("Path() = %q, want %q", got, path1)
	}

	// Write different tiers to tiers2.json.
	writeJSON(t, path2, &TiersConfig{
		Tiers: []Tier{{Name: "alt", Model: "sonnet", Priority: 0, Enabled: true}},
	})

	if err := store.SetPath(path2); err != nil {
		t.Fatalf("SetPath() error: %v", err)
	}
	if got := store.Path(); got != path2 {
		t.Fatalf("Path() after SetPath = %q, want %q", got, path2)
	}
	if store.Current().Tiers[0].Name != "alt" {
		t.Errorf("Current() after SetPath: expected tier 'alt', got %q", store.Current().Tiers[0].Name)
	}
}

func TestFileTierStore_SetPath_NonExistentFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	store := NewFileTierStore(filepath.Join(dir, "tiers.json"))

	// SetPath to a non-existent file: should succeed and load defaults.
	if err := store.SetPath(filepath.Join(dir, "ghost.json")); err != nil {
		t.Fatalf("SetPath() to non-existent file should not error: %v", err)
	}
	cur := store.Current()
	if len(cur.Tiers) < 4 {
		t.Errorf("expected default tiers after SetPath to missing file, got %d tiers", len(cur.Tiers))
	}
}

func TestFileTierStore_SaveCreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.d", "tiers.json")
	store := NewFileTierStore(path)

	cfg := &TiersConfig{
		Tiers: []Tier{{Name: "test", Model: "sonnet", Priority: 0, Enabled: true}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() should create parent dir: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() after Save: %v", err)
	}
	if len(loaded.Tiers) != 1 || loaded.Tiers[0].Name != "test" {
		t.Errorf("unexpected tiers after save: %+v", loaded.Tiers)
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
