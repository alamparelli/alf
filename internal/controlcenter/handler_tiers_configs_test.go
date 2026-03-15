package controlcenter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTierConfigsHandler(t *testing.T) (*TierConfigsHandler, string) {
	t.Helper()
	configDir := t.TempDir()
	tierStorePath := filepath.Join(configDir, "tiers.json")
	configStorePath := filepath.Join(configDir, "config.json")

	return &TierConfigsHandler{
		ConfigDir:   configDir,
		TierStore:   NewFileTierStore(tierStorePath),
		ConfigStore: NewFileConfigStore(configStorePath),
		Notifier:    &mockNotifier{},
	}, configDir
}

// writeTierFile creates a JSON tier config file under configDir/tiers/<name>.json.
func writeTierFile(t *testing.T, configDir, name string, tiers []Tier) {
	t.Helper()
	dir := filepath.Join(configDir, "tiers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir tiers: %v", err)
	}
	writeJSON(t, filepath.Join(dir, name+".json"), &TiersConfig{Tiers: tiers})
}

func TestTierConfigsHandler_List_NoDirectory(t *testing.T) {
	h, _ := newTierConfigsHandler(t)

	req := httptest.NewRequest("GET", "/api/tiers/configs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var entries []tierConfigEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty array, got %d entries", len(entries))
	}
}

func TestTierConfigsHandler_List_WithFiles(t *testing.T) {
	h, configDir := newTierConfigsHandler(t)

	writeTierFile(t, configDir, "grok", []Tier{
		{Name: "fast", Model: "grok-1"},
		{Name: "smart", Model: "grok-2"},
	})
	writeTierFile(t, configDir, "openai", []Tier{
		{Name: "gpt4", Model: "gpt-4o"},
	})
	// Non-JSON file should be ignored.
	os.WriteFile(filepath.Join(configDir, "tiers", "readme.txt"), []byte("ignore me"), 0o644)

	req := httptest.NewRequest("GET", "/api/tiers/configs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var entries []tierConfigEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	byName := map[string]tierConfigEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	grok, ok := byName["grok"]
	if !ok {
		t.Fatal("missing grok entry")
	}
	if grok.Tiers != 2 {
		t.Errorf("grok tiers: want 2, got %d", grok.Tiers)
	}
	if grok.Path != filepath.Join("tiers", "grok.json") {
		t.Errorf("grok path: want tiers/grok.json, got %s", grok.Path)
	}

	openai, ok := byName["openai"]
	if !ok {
		t.Fatal("missing openai entry")
	}
	if openai.Tiers != 1 {
		t.Errorf("openai tiers: want 1, got %d", openai.Tiers)
	}
}

func TestTierConfigsHandler_List_ActiveMarker(t *testing.T) {
	h, configDir := newTierConfigsHandler(t)

	writeTierFile(t, configDir, "alpha", []Tier{{Name: "a"}})
	writeTierFile(t, configDir, "beta", []Tier{{Name: "b"}})

	// Point the tier store to the alpha config so it becomes the active one.
	alphaPath := filepath.Join(configDir, "tiers", "alpha.json")
	if err := h.TierStore.SetPath(alphaPath); err != nil {
		t.Fatalf("SetPath: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/tiers/configs", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var entries []tierConfigEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	byName := map[string]tierConfigEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}

	if !byName["alpha"].Active {
		t.Error("alpha should be active")
	}
	if byName["beta"].Active {
		t.Error("beta should not be active")
	}
}

func TestTierConfigsHandler_Switch_Valid(t *testing.T) {
	h, configDir := newTierConfigsHandler(t)
	notifier := h.Notifier.(*mockNotifier)

	writeTierFile(t, configDir, "grok", []Tier{{Name: "fast", Model: "grok-1"}})

	body := `{"name":"grok.json"}`
	req := httptest.NewRequest("POST", "/api/tiers/configs/switch", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify config.json was updated with tiers_file (just filename).
	cfg, err := h.ConfigStore.Load()
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	if cfg.TiersFile != "grok.json" {
		t.Errorf("config TiersFile: want grok.json, got %q", cfg.TiersFile)
	}

	// Verify tier store path was updated.
	wantPath := filepath.Join(configDir, "tiers", "grok.json")
	if h.TierStore.Path() != wantPath {
		t.Errorf("TierStore.Path(): want %q, got %q", wantPath, h.TierStore.Path())
	}

	// Verify notifications were sent.
	if len(notifier.events) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(notifier.events))
	}
	if notifier.events[0] != ReloadTiers {
		t.Errorf("first notification: want ReloadTiers, got %v", notifier.events[0])
	}
	if notifier.events[1] != ReloadConfig {
		t.Errorf("second notification: want ReloadConfig, got %v", notifier.events[1])
	}
}

func TestTierConfigsHandler_Switch_MissingName(t *testing.T) {
	h, _ := newTierConfigsHandler(t)

	req := httptest.NewRequest("POST", "/api/tiers/configs/switch", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTierConfigsHandler_Switch_InvalidName(t *testing.T) {
	h, _ := newTierConfigsHandler(t)

	tests := []struct {
		label string
		body  string
	}{
		{"path traversal", `{"name":"../../../etc/passwd"}`},
		{"slash in name", `{"name":"tiers/grok.json"}`},
		{"not json extension", `{"name":"grok.txt"}`},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/tiers/configs/switch", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestTierConfigsHandler_Switch_NonExistent(t *testing.T) {
	h, _ := newTierConfigsHandler(t)

	body := `{"name":"ghost.json"}`
	req := httptest.NewRequest("POST", "/api/tiers/configs/switch", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTiersPathFromConfig_TiersSubdir(t *testing.T) {
	configDir := t.TempDir()
	tiersDir := filepath.Join(configDir, "tiers")
	os.MkdirAll(tiersDir, 0o755)
	os.WriteFile(filepath.Join(tiersDir, "grok.json"), []byte(`{}`), 0o644)

	cfg := &Config{TiersFile: "grok.json"}
	got := TiersPathFromConfig(configDir, cfg)
	want := filepath.Join(configDir, "tiers", "grok.json")
	if got != want {
		t.Errorf("TiersPathFromConfig: want %q, got %q", want, got)
	}
}

func TestTiersPathFromConfig_FallbackRoot(t *testing.T) {
	configDir := t.TempDir()
	// No tiers/ subdir — file at root.
	os.WriteFile(filepath.Join(configDir, "custom.json"), []byte(`{}`), 0o644)

	cfg := &Config{TiersFile: "custom.json"}
	got := TiersPathFromConfig(configDir, cfg)
	want := filepath.Join(configDir, "custom.json")
	if got != want {
		t.Errorf("TiersPathFromConfig: want %q, got %q", want, got)
	}
}
