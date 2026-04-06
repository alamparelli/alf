package controlcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/tooling"
)

// mockTierStore implements TierStore for testing.
type mockTierStore struct {
	tiers *TiersConfig
}

func (m *mockTierStore) Load() (*TiersConfig, error)  { return m.tiers, nil }
func (m *mockTierStore) Save(_ *TiersConfig) error     { return nil }
func (m *mockTierStore) Current() *TiersConfig          { return m.tiers }
func (m *mockTierStore) Reload() error                  { return nil }
func (m *mockTierStore) SetPath(_ string) error         { return nil }
func (m *mockTierStore) Path() string                   { return "" }

// deadlineCaptureTool is a NativeTool that records the context deadline.
type deadlineCaptureTool struct {
	deadline  time.Time
	hasDeadline bool
}

func (d *deadlineCaptureTool) ToolName() string          { return "llm" }
func (d *deadlineCaptureTool) Schema() tooling.ToolSchema { return tooling.ToolSchema{Name: "llm"} }
func (d *deadlineCaptureTool) Run(ctx context.Context, _ string) (string, error) {
	dl, ok := ctx.Deadline()
	d.deadline = dl
	d.hasDeadline = ok
	return "ok", nil
}

func newLLMRequest(tier, prompt string) *http.Request {
	body, _ := json.Marshal(llmInvokeRequest{Tier: tier, Prompt: prompt})
	return httptest.NewRequest(http.MethodPost, "/api/llm/invoke", strings.NewReader(string(body)))
}

func TestLLMInvokeHandler_TimeoutFromTier(t *testing.T) {
	capture := &deadlineCaptureTool{}
	reg := tooling.NewRegistry(t.TempDir())
	reg.RegisterNative(capture)

	h := &LLMInvokeHandler{
		ToolRegistry: reg,
		TierStore: &mockTierStore{
			tiers: &TiersConfig{
				Tiers: []Tier{
					{Name: "fast", Model: "haiku", Enabled: true, TimeoutMin: 2},
				},
			},
		},
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newLLMRequest("fast", "hello"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !capture.hasDeadline {
		t.Fatal("expected context to have a deadline")
	}

	// The deadline should be ~2 minutes from now, not ~5 minutes.
	remaining := time.Until(capture.deadline)
	if remaining > 2*time.Minute+5*time.Second {
		t.Errorf("expected deadline ~2m, got %v remaining", remaining)
	}
	if remaining < 1*time.Minute+50*time.Second {
		t.Errorf("expected deadline ~2m, got %v remaining (too short)", remaining)
	}
}

func TestLLMInvokeHandler_TimeoutFallback_ZeroTimeoutMin(t *testing.T) {
	capture := &deadlineCaptureTool{}
	reg := tooling.NewRegistry(t.TempDir())
	reg.RegisterNative(capture)

	h := &LLMInvokeHandler{
		ToolRegistry: reg,
		TierStore: &mockTierStore{
			tiers: &TiersConfig{
				Tiers: []Tier{
					{Name: "default-tier", Model: "sonnet", Enabled: true, TimeoutMin: 0},
				},
			},
		},
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newLLMRequest("default-tier", "hello"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	remaining := time.Until(capture.deadline)
	if remaining < 9*time.Minute+50*time.Second || remaining > 10*time.Minute+5*time.Second {
		t.Errorf("expected deadline ~10m (fallback), got %v remaining", remaining)
	}
}

func TestLLMInvokeHandler_TimeoutFallback_NilTierStore(t *testing.T) {
	capture := &deadlineCaptureTool{}
	reg := tooling.NewRegistry(t.TempDir())
	reg.RegisterNative(capture)

	h := &LLMInvokeHandler{
		ToolRegistry: reg,
		TierStore:    nil,
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newLLMRequest("any-tier", "hello"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	remaining := time.Until(capture.deadline)
	if remaining < 9*time.Minute+50*time.Second || remaining > 10*time.Minute+5*time.Second {
		t.Errorf("expected deadline ~10m (fallback), got %v remaining", remaining)
	}
}

func TestLLMInvokeHandler_TimeoutFallback_TierNotFound(t *testing.T) {
	capture := &deadlineCaptureTool{}
	reg := tooling.NewRegistry(t.TempDir())
	reg.RegisterNative(capture)

	h := &LLMInvokeHandler{
		ToolRegistry: reg,
		TierStore: &mockTierStore{
			tiers: &TiersConfig{
				Tiers: []Tier{
					{Name: "other-tier", Model: "haiku", Enabled: true, TimeoutMin: 10},
				},
			},
		},
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newLLMRequest("nonexistent", "hello"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	remaining := time.Until(capture.deadline)
	if remaining < 9*time.Minute+50*time.Second || remaining > 10*time.Minute+5*time.Second {
		t.Errorf("expected deadline ~10m (fallback), got %v remaining", remaining)
	}
}

func TestLLMInvokeHandler_TimeoutFallback_DisabledTier(t *testing.T) {
	capture := &deadlineCaptureTool{}
	reg := tooling.NewRegistry(t.TempDir())
	reg.RegisterNative(capture)

	h := &LLMInvokeHandler{
		ToolRegistry: reg,
		TierStore: &mockTierStore{
			tiers: &TiersConfig{
				Tiers: []Tier{
					{Name: "disabled-tier", Model: "sonnet", Enabled: false, TimeoutMin: 10},
				},
			},
		},
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newLLMRequest("disabled-tier", "hello"))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Disabled tier should not match, so fallback to 10m.
	remaining := time.Until(capture.deadline)
	if remaining < 9*time.Minute+50*time.Second || remaining > 10*time.Minute+5*time.Second {
		t.Errorf("expected deadline ~10m (fallback for disabled tier), got %v remaining", remaining)
	}
}

func TestLLMInvokeHandler_MethodNotAllowed(t *testing.T) {
	h := &LLMInvokeHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/llm/invoke", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestLLMInvokeHandler_MissingFields(t *testing.T) {
	h := &LLMInvokeHandler{}

	tests := []struct {
		name string
		body string
	}{
		{"empty tier", `{"tier":"","prompt":"hello"}`},
		{"empty prompt", `{"tier":"fast","prompt":""}`},
		{"both empty", `{"tier":"","prompt":""}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/llm/invoke", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", rec.Code)
			}
		})
	}
}

func TestLLMInvokeHandler_LLMToolNotRegistered(t *testing.T) {
	reg := tooling.NewRegistry(t.TempDir()) // no native tools registered

	h := &LLMInvokeHandler{ToolRegistry: reg}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newLLMRequest("fast", "hello"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}

	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["error"] != "llm tool not registered" {
		t.Errorf("unexpected error: %q", resp["error"])
	}
}
