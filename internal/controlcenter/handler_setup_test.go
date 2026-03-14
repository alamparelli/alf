package controlcenter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupMockConfigStore implements ConfigStore for setup handler tests.
type setupMockConfigStore struct {
	cfg   *Config
	err   error
	saved *Config // captures last Save call
}

func (m *setupMockConfigStore) Load() (*Config, error) { return m.cfg, m.err }
func (m *setupMockConfigStore) Save(cfg *Config) error {
	m.saved = cfg
	m.cfg = cfg // update so subsequent Load returns saved value
	return nil
}

func newSetupHandler(t *testing.T, cfg *Config, tiersDir, presetsDir string) *SetupHandler {
	t.Helper()
	if cfg == nil {
		cfg = DefaultConfig()
	}

	tierPath := filepath.Join(tiersDir, "tiers.json")
	ts := NewFileTierStore(tierPath)
	// Try to load if file exists; ignore error for "no file" tests.
	_ = ts.Reload()

	return &SetupHandler{
		ConfigStore: &setupMockConfigStore{cfg: cfg},
		TierStore:   ts,
		Vault:       nil,
		PresetsDir:  presetsDir,
	}
}

func doSetupGet(t *testing.T, h *SetupHandler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	h.ServeHTTP(rec, req)
	return rec
}

// --- Status tests ---

func TestSetupStatus_FreshInstall(t *testing.T) {
	dir := t.TempDir()
	// Set HOME to a temp dir so Claude auth check finds nothing.
	t.Setenv("HOME", dir)
	// Clear telegram env vars.
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_BOT_TOKEN_FILE", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID_FILE", "")

	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))
	rec := doSetupGet(t, h, "/api/setup/status")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var status SetupStatus
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if status.Steps.Backend {
		t.Error("expected backend=false")
	}
	if status.Steps.ClaudeAuth {
		t.Error("expected claude_auth=false")
	}
	if status.Steps.Telegram {
		t.Error("expected telegram=false")
	}
	// Default tiers are always loaded (embedded defaults), so tiers=true.
	if !status.Steps.Tiers {
		t.Error("expected tiers=true (defaults loaded)")
	}
	// Still not completed: no backend configured.
	if status.Completed {
		t.Error("expected completed=false")
	}
}

func TestSetupStatus_BackendConfigured(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_BOT_TOKEN_FILE", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID_FILE", "")

	cfg := DefaultConfig()
	cfg.Backends = map[string]BackendConfig{
		"openrouter": {BaseURL: "https://openrouter.ai/api/v1"},
	}

	h := newSetupHandler(t, cfg, dir, filepath.Join(dir, "presets"))
	rec := doSetupGet(t, h, "/api/setup/status")

	var status SetupStatus
	json.NewDecoder(rec.Body).Decode(&status)

	if !status.Steps.Backend {
		t.Error("expected backend=true when backends configured")
	}
}

func TestSetupStatus_ClaudeAuth(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_BOT_TOKEN_FILE", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID_FILE", "")

	// Create fake .claude.json.
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"token":"abc"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))
	rec := doSetupGet(t, h, "/api/setup/status")

	var status SetupStatus
	json.NewDecoder(rec.Body).Decode(&status)

	if !status.Steps.ClaudeAuth {
		t.Error("expected claude_auth=true")
	}
	if !status.Steps.Backend {
		t.Error("expected backend=true (claude auth implies backend)")
	}
}

func TestSetupStatus_TelegramEnvVars(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:ABC")
	t.Setenv("TELEGRAM_BOT_TOKEN_FILE", "")
	t.Setenv("TELEGRAM_CHAT_ID", "456")
	t.Setenv("TELEGRAM_CHAT_ID_FILE", "")

	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))
	rec := doSetupGet(t, h, "/api/setup/status")

	var status SetupStatus
	json.NewDecoder(rec.Body).Decode(&status)

	if !status.Steps.Telegram {
		t.Error("expected telegram=true")
	}
}

func TestSetupStatus_TelegramPartial(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:ABC")
	t.Setenv("TELEGRAM_BOT_TOKEN_FILE", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID_FILE", "")

	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))
	rec := doSetupGet(t, h, "/api/setup/status")

	var status SetupStatus
	json.NewDecoder(rec.Body).Decode(&status)

	if status.Steps.Telegram {
		t.Error("expected telegram=false when only token set")
	}
}

func TestSetupStatus_TiersEnabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_BOT_TOKEN_FILE", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID_FILE", "")

	tiersJSON := `{"tiers":[{"name":"test","model":"haiku","priority":1,"enabled":true,"routable":true}]}`
	os.WriteFile(filepath.Join(dir, "tiers.json"), []byte(tiersJSON), 0o644)

	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))
	rec := doSetupGet(t, h, "/api/setup/status")

	var status SetupStatus
	json.NewDecoder(rec.Body).Decode(&status)

	if !status.Steps.Tiers {
		t.Error("expected tiers=true")
	}
}

func TestSetupStatus_Completed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_BOT_TOKEN_FILE", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID_FILE", "")

	// Backend via config.
	cfg := DefaultConfig()
	cfg.Backends = map[string]BackendConfig{
		"openrouter": {BaseURL: "https://openrouter.ai/api/v1"},
	}

	// Tiers on disk.
	tiersJSON := `{"tiers":[{"name":"t1","model":"haiku","priority":1,"enabled":true,"routable":true}]}`
	os.WriteFile(filepath.Join(dir, "tiers.json"), []byte(tiersJSON), 0o644)

	h := newSetupHandler(t, cfg, dir, filepath.Join(dir, "presets"))
	rec := doSetupGet(t, h, "/api/setup/status")

	var status SetupStatus
	json.NewDecoder(rec.Body).Decode(&status)

	if !status.Completed {
		t.Error("expected completed=true")
	}
}

func TestSetupStatus_VaultNil(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_BOT_TOKEN_FILE", "")
	t.Setenv("TELEGRAM_CHAT_ID", "")
	t.Setenv("TELEGRAM_CHAT_ID_FILE", "")

	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))
	rec := doSetupGet(t, h, "/api/setup/status")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with nil vault, got %d", rec.Code)
	}
}

func TestSetupStatus_MethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/status", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// --- Presets tests ---

func TestSetupPresets_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	presetsDir := filepath.Join(dir, "setup-presets")
	os.MkdirAll(presetsDir, 0o755)

	h := newSetupHandler(t, nil, dir, presetsDir)
	rec := doSetupGet(t, h, "/api/setup/presets")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Presets map[string][]TierPreset `json:"presets"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	if len(resp.Presets) != 0 {
		t.Errorf("expected empty presets, got %d backends", len(resp.Presets))
	}
}

func TestSetupPresets_NonExistentDir(t *testing.T) {
	dir := t.TempDir()
	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "does-not-exist"))
	rec := doSetupGet(t, h, "/api/setup/presets")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even if dir missing, got %d", rec.Code)
	}
}

func TestSetupPresets_LoadsFiles(t *testing.T) {
	dir := t.TempDir()
	presetsDir := filepath.Join(dir, "setup-presets")
	os.MkdirAll(presetsDir, 0o755)

	p1 := TierPreset{
		ID:      "claude-default",
		Name:    "Claude Default",
		Backend: "claude",
		Tiers: []Tier{
			{Name: "haiku", Model: "haiku", Priority: 1, Enabled: true},
		},
	}
	p2 := TierPreset{
		ID:      "openrouter-eco",
		Name:    "Economic",
		Backend: "openrouter",
		Tiers: []Tier{
			{Name: "or-haiku", Model: "anthropic/claude-3-5-haiku-latest", Priority: 1, Enabled: true, Backend: "openrouter"},
		},
	}

	writePreset(t, presetsDir, "claude-default.json", p1)
	writePreset(t, presetsDir, "openrouter-eco.json", p2)

	h := newSetupHandler(t, nil, dir, presetsDir)
	rec := doSetupGet(t, h, "/api/setup/presets")

	var resp struct {
		Presets map[string][]TierPreset `json:"presets"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	if len(resp.Presets) != 2 {
		t.Fatalf("expected 2 backend groups, got %d", len(resp.Presets))
	}
	if len(resp.Presets["claude"]) != 1 {
		t.Errorf("expected 1 claude preset, got %d", len(resp.Presets["claude"]))
	}
	if len(resp.Presets["openrouter"]) != 1 {
		t.Errorf("expected 1 openrouter preset, got %d", len(resp.Presets["openrouter"]))
	}
	if resp.Presets["claude"][0].ID != "claude-default" {
		t.Errorf("expected id claude-default, got %s", resp.Presets["claude"][0].ID)
	}
}

func TestSetupPresets_SkipsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	presetsDir := filepath.Join(dir, "setup-presets")
	os.MkdirAll(presetsDir, 0o755)

	// Valid preset.
	p := TierPreset{ID: "valid", Name: "Valid", Backend: "claude"}
	writePreset(t, presetsDir, "valid.json", p)

	// Invalid JSON.
	os.WriteFile(filepath.Join(presetsDir, "broken.json"), []byte("{invalid"), 0o644)

	// Missing required fields.
	os.WriteFile(filepath.Join(presetsDir, "empty.json"), []byte(`{"name":"no-id"}`), 0o644)

	h := newSetupHandler(t, nil, dir, presetsDir)
	rec := doSetupGet(t, h, "/api/setup/presets")

	var resp struct {
		Presets map[string][]TierPreset `json:"presets"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	total := 0
	for _, ps := range resp.Presets {
		total += len(ps)
	}
	if total != 1 {
		t.Errorf("expected 1 valid preset (skipping broken + empty), got %d", total)
	}
}

func TestSetupPresets_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	presetsDir := filepath.Join(dir, "setup-presets")
	os.MkdirAll(filepath.Join(presetsDir, "subdir"), 0o755)

	p := TierPreset{ID: "ok", Name: "OK", Backend: "claude"}
	writePreset(t, presetsDir, "ok.json", p)

	h := newSetupHandler(t, nil, dir, presetsDir)
	rec := doSetupGet(t, h, "/api/setup/presets")

	var resp struct {
		Presets map[string][]TierPreset `json:"presets"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	total := 0
	for _, ps := range resp.Presets {
		total += len(ps)
	}
	if total != 1 {
		t.Errorf("expected 1 preset, got %d", total)
	}
}

// --- PR2: Validation endpoint tests ---

func doSetupPost(t *testing.T, h *SetupHandler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func TestSetupBackendTest_Success(t *testing.T) {
	// Mock backend that returns 200 on /models.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dir := t.TempDir()
	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))

	body := fmt.Sprintf(`{"type":"custom","base_url":"%s/v1","api_key":"test-key"}`, srv.URL)
	rec := doSetupPost(t, h, "/api/setup/backend/test", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp)
	}
}

func TestSetupBackendTest_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	dir := t.TempDir()
	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))

	body := fmt.Sprintf(`{"type":"openrouter","base_url":"%s/v1","api_key":"bad-key"}`, srv.URL)
	rec := doSetupPost(t, h, "/api/setup/backend/test", body)

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["ok"] != false {
		t.Errorf("expected ok=false for 401, got %v", resp)
	}
	if !strings.Contains(resp["error"].(string), "authentication") {
		t.Errorf("expected auth error message, got %s", resp["error"])
	}
}

func TestSetupBackendTest_ConnectionFailure(t *testing.T) {
	dir := t.TempDir()
	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))

	body := `{"type":"custom","base_url":"http://127.0.0.1:1"}`
	rec := doSetupPost(t, h, "/api/setup/backend/test", body)

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["ok"] != false {
		t.Errorf("expected ok=false for unreachable, got %v", resp)
	}
}

func TestSetupBackendTest_MissingBaseURL(t *testing.T) {
	dir := t.TempDir()
	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))

	rec := doSetupPost(t, h, "/api/setup/backend/test", `{"type":"custom"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing base_url, got %d", rec.Code)
	}
}

func TestSetupTelegramValidate_Success(t *testing.T) {
	// Mock Telegram API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/getMe") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true,"result":{"username":"test_bot"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	// We can't easily override the Telegram API URL in validateBotTokenHTTP,
	// so we test the handler structure and error path instead.
	dir := t.TempDir()
	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))

	// Test with empty token → should return 400.
	rec := doSetupPost(t, h, "/api/setup/telegram/validate", `{"bot_token":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty token, got %d", rec.Code)
	}

	// Test with invalid token → validateBotTokenHTTP returns "" (Telegram API unreachable with fake token).
	rec = doSetupPost(t, h, "/api/setup/telegram/validate", `{"bot_token":"invalid:token"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["ok"] != false {
		t.Errorf("expected ok=false for invalid token, got %v", resp)
	}
}

func TestSetupTelegramValidate_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))

	rec := doSetupPost(t, h, "/api/setup/telegram/validate", `{invalid`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON, got %d", rec.Code)
	}
}

func TestSetupClaudeCheck_Authenticated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"token":"abc"}`), 0o644)

	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))
	rec := doSetupGet(t, h, "/api/setup/claude/check")

	var resp map[string]bool
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp["authenticated"] {
		t.Error("expected authenticated=true")
	}
}

func TestSetupClaudeCheck_NotAuthenticated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))
	rec := doSetupGet(t, h, "/api/setup/claude/check")

	var resp map[string]bool
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["authenticated"] {
		t.Error("expected authenticated=false")
	}
}

func TestSetupOllamaModels_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"models":[{"name":"llama3:latest"},{"name":"codellama:7b"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dir := t.TempDir()
	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/setup/ollama/models?base_url="+srv.URL, nil)
	h.ServeHTTP(rec, req)

	var resp struct {
		Models []string `json:"models"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	if len(resp.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(resp.Models))
	}
	if resp.Models[0] != "llama3:latest" {
		t.Errorf("expected llama3:latest, got %s", resp.Models[0])
	}
}

func TestSetupOllamaModels_Unreachable(t *testing.T) {
	dir := t.TempDir()
	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/setup/ollama/models?base_url=http://127.0.0.1:1", nil)
	h.ServeHTTP(rec, req)

	var resp struct {
		Models []string `json:"models"`
		Error  string   `json:"error"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	if len(resp.Models) != 0 {
		t.Errorf("expected empty models, got %d", len(resp.Models))
	}
	if resp.Error == "" {
		t.Error("expected error message")
	}
}

func TestSetupOllamaModels_InvalidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	dir := t.TempDir()
	h := newSetupHandler(t, nil, dir, filepath.Join(dir, "presets"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/setup/ollama/models?base_url="+srv.URL, nil)
	h.ServeHTTP(rec, req)

	var resp struct {
		Models []string `json:"models"`
		Error  string   `json:"error"`
	}
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Error == "" {
		t.Error("expected error for invalid JSON response")
	}
}

func writePreset(t *testing.T, dir, name string, p TierPreset) {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- PR3: Apply endpoint tests ---

// setupMockNotifier captures reload events for testing.
type setupMockNotifier struct {
	events []ReloadEvent
}

func (n *setupMockNotifier) Notify(e ReloadEvent) { n.events = append(n.events, e) }

// newApplyHandler creates a SetupHandler configured for apply tests.
// Vault is nil (tests vault-nil error paths). Use newApplyHandlerWithPreset for preset tests.
func newApplyHandler(t *testing.T, cfg *Config) (*SetupHandler, *setupMockConfigStore, *setupMockNotifier) {
	t.Helper()
	if cfg == nil {
		cfg = DefaultConfig()
	}
	dir := t.TempDir()
	presetsDir := filepath.Join(dir, "setup-presets")
	os.MkdirAll(presetsDir, 0o755)

	store := &setupMockConfigStore{cfg: cfg}
	tierPath := filepath.Join(dir, "tiers.json")
	ts := NewFileTierStore(tierPath)
	_ = ts.Reload()
	notifier := &setupMockNotifier{}

	h := &SetupHandler{
		ConfigStore: store,
		TierStore:   ts,
		Vault:       nil,
		PresetsDir:  presetsDir,
		Notifier:    notifier,
		ConfigDir:   dir,
	}
	return h, store, notifier
}

func TestSetupApply_TimezoneOnly(t *testing.T) {
	h, store, notifier := newApplyHandler(t, nil)

	body := `{"timezone":"Europe/Brussels"}`
	rec := doSetupPost(t, h, "/api/setup/apply", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if store.saved == nil {
		t.Fatal("expected config to be saved")
	}
	if store.saved.Timezone != "Europe/Brussels" {
		t.Errorf("expected timezone Europe/Brussels, got %s", store.saved.Timezone)
	}

	// Should emit ReloadConfig.
	if len(notifier.events) != 1 || notifier.events[0] != ReloadConfig {
		t.Errorf("expected [ReloadConfig], got %v", notifier.events)
	}
}

func TestSetupApply_InvalidTimezone(t *testing.T) {
	h, _, _ := newApplyHandler(t, nil)

	rec := doSetupPost(t, h, "/api/setup/apply", `{"timezone":"Invalid/Zone"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestSetupApply_OllamaBackendNoVault(t *testing.T) {
	h, store, notifier := newApplyHandler(t, nil)

	body := `{"backends":{"ollama":{"base_url":"http://localhost:11434/v1","auth":"none"}}}`
	rec := doSetupPost(t, h, "/api/setup/apply", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (ollama doesn't need vault), got %d: %s", rec.Code, rec.Body.String())
	}

	if store.saved == nil {
		t.Fatal("expected config to be saved")
	}
	if _, ok := store.saved.Backends["ollama"]; !ok {
		t.Error("expected ollama backend in saved config")
	}
	if store.saved.Backends["ollama"].Auth != "none" {
		t.Errorf("expected auth=none, got %s", store.saved.Backends["ollama"].Auth)
	}

	if len(notifier.events) != 1 || notifier.events[0] != ReloadConfig {
		t.Errorf("expected [ReloadConfig], got %v", notifier.events)
	}
}

func TestSetupApply_APIKeyWithoutVault(t *testing.T) {
	h, _, _ := newApplyHandler(t, nil)

	body := `{"backends":{"openrouter":{"base_url":"https://openrouter.ai/api/v1","api_key":"sk-or-test"}}}`
	rec := doSetupPost(t, h, "/api/setup/apply", body)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when vault nil and api_key provided, got %d", rec.Code)
	}
}

func TestSetupApply_TelegramWithoutVault(t *testing.T) {
	h, _, _ := newApplyHandler(t, nil)

	body := `{"telegram":{"bot_token":"123:ABC","chat_id":"456"}}`
	rec := doSetupPost(t, h, "/api/setup/apply", body)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when vault nil and telegram provided, got %d", rec.Code)
	}
}

func TestSetupApply_PresetNotFound(t *testing.T) {
	h, _, _ := newApplyHandler(t, nil)

	body := `{"preset_id":"nonexistent"}`
	rec := doSetupPost(t, h, "/api/setup/apply", body)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for nonexistent preset, got %d", rec.Code)
	}
}

func TestSetupApply_PresetWritesTiers(t *testing.T) {
	h, _, notifier := newApplyHandler(t, nil)

	// Create a preset file.
	preset := TierPreset{
		ID:      "test-preset",
		Name:    "Test",
		Backend: "claude",
		RouterConfig: &PresetRouterConfig{
			RouterModel:     "haiku",
			DefaultFallback: "haiku",
			Distinctions:    "test distinctions",
		},
		Tiers: []Tier{
			{Name: "t1", Model: "haiku", Priority: 1, Enabled: true, Routable: true},
			{Name: "t2", Model: "sonnet", Priority: 2, Enabled: true, Routable: true},
		},
	}
	writePreset(t, h.PresetsDir, "test-preset.json", preset)

	body := `{"preset_id":"test-preset"}`
	rec := doSetupPost(t, h, "/api/setup/apply", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify tiers were written.
	tc := h.TierStore.Current()
	if tc == nil {
		t.Fatal("expected tiers to be loaded")
	}
	if len(tc.Tiers) != 2 {
		t.Errorf("expected 2 tiers, got %d", len(tc.Tiers))
	}
	if tc.RouterModel != "haiku" {
		t.Errorf("expected router_model=haiku, got %s", tc.RouterModel)
	}
	if tc.RouterDistinctions != "test distinctions" {
		t.Errorf("expected router_distinctions to match preset")
	}

	// Should emit ReloadTiers (no config change).
	found := false
	for _, e := range notifier.events {
		if e == ReloadTiers {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ReloadTiers event, got %v", notifier.events)
	}
}

func TestSetupApply_Idempotent(t *testing.T) {
	h, _, _ := newApplyHandler(t, nil)

	preset := TierPreset{
		ID: "idempotent", Name: "Idempotent", Backend: "claude",
		Tiers: []Tier{{Name: "t1", Model: "haiku", Priority: 1, Enabled: true}},
	}
	writePreset(t, h.PresetsDir, "idempotent.json", preset)

	body := `{"preset_id":"idempotent","timezone":"UTC"}`

	rec1 := doSetupPost(t, h, "/api/setup/apply", body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first apply failed: %d", rec1.Code)
	}

	rec2 := doSetupPost(t, h, "/api/setup/apply", body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second apply failed: %d", rec2.Code)
	}
}

func TestSetupApply_MergesBackends(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Backends = map[string]BackendConfig{
		"existing": {BaseURL: "http://existing.com", Auth: "bearer"},
	}
	h, store, _ := newApplyHandler(t, cfg)

	body := `{"backends":{"ollama":{"base_url":"http://localhost:11434/v1","auth":"none"}}}`
	rec := doSetupPost(t, h, "/api/setup/apply", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Both backends should exist.
	if len(store.saved.Backends) != 2 {
		t.Errorf("expected 2 backends (existing + ollama), got %d", len(store.saved.Backends))
	}
	if _, ok := store.saved.Backends["existing"]; !ok {
		t.Error("existing backend was removed")
	}
	if _, ok := store.saved.Backends["ollama"]; !ok {
		t.Error("ollama backend was not added")
	}
}

func TestSetupApply_EmptyBody(t *testing.T) {
	h, _, notifier := newApplyHandler(t, nil)

	rec := doSetupPost(t, h, "/api/setup/apply", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for empty apply, got %d", rec.Code)
	}

	// No events should be emitted for empty apply.
	if len(notifier.events) != 0 {
		t.Errorf("expected no events for empty apply, got %v", notifier.events)
	}
}

func TestSetupApply_ResponseFields(t *testing.T) {
	h, _, _ := newApplyHandler(t, nil)

	rec := doSetupPost(t, h, "/api/setup/apply", `{"timezone":"UTC"}`)

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp["ok"] != true {
		t.Errorf("expected ok=true")
	}
	if resp["restart_required"] != false {
		t.Errorf("expected restart_required=false")
	}
	if resp["vault_unlocked"] != false {
		t.Errorf("expected vault_unlocked=false")
	}
}

func TestSetupApply_OllamaDefaultAuth(t *testing.T) {
	h, store, _ := newApplyHandler(t, nil)

	// Ollama without explicit auth should default to "none".
	body := `{"backends":{"ollama":{"base_url":"http://localhost:11434/v1"}}}`
	rec := doSetupPost(t, h, "/api/setup/apply", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if store.saved.Backends["ollama"].Auth != "none" {
		t.Errorf("expected ollama auth=none, got %s", store.saved.Backends["ollama"].Auth)
	}
}

func TestSetupApply_APIKeyEmptySkipsVault(t *testing.T) {
	h, store, _ := newApplyHandler(t, nil)

	// Backend with empty api_key should not require vault.
	body := `{"backends":{"openrouter":{"base_url":"https://openrouter.ai/api/v1"}}}`
	rec := doSetupPost(t, h, "/api/setup/apply", body)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (no api_key = no vault needed), got %d: %s", rec.Code, rec.Body.String())
	}

	if _, ok := store.saved.Backends["openrouter"]; !ok {
		t.Error("openrouter backend not saved")
	}
}
