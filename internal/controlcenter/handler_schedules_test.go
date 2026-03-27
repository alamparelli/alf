package controlcenter

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postSchedule(h *SchedulesHandler, body map[string]any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/schedules", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newSchedulesHandlerForTest() *SchedulesHandler {
	return &SchedulesHandler{Engine: &mockScheduleEngine{}}
}

// ---------------------------------------------------------------------------
// SEC-009: Prompt injection length limits
// ---------------------------------------------------------------------------

func TestSchedules_PromptMaxLength(t *testing.T) {
	h := newSchedulesHandlerForTest()
	rec := postSchedule(h, map[string]any{
		"name":     "test",
		"schedule": "0 * * * *",
		"prompt":   strings.Repeat("x", 4097),
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("prompt > 4096 chars: expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "prompt") {
		t.Error("error message should mention 'prompt'")
	}
}

func TestSchedules_PromptAtMaxLength_Accepted(t *testing.T) {
	h := newSchedulesHandlerForTest()
	rec := postSchedule(h, map[string]any{
		"name":     "test",
		"schedule": "0 * * * *",
		"prompt":   strings.Repeat("x", 4096),
	})
	if rec.Code == http.StatusBadRequest && strings.Contains(rec.Body.String(), "prompt exceeds") {
		t.Errorf("prompt of exactly 4096 chars should be accepted")
	}
}

func TestSchedules_ReasonMaxLength(t *testing.T) {
	h := newSchedulesHandlerForTest()
	rec := postSchedule(h, map[string]any{
		"name":     "test",
		"schedule": "0 * * * *",
		"prompt":   "do something",
		"reason":   strings.Repeat("r", 257),
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("reason > 256 chars: expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "reason") {
		t.Error("error message should mention 'reason'")
	}
}

func TestSchedules_NameMaxLength(t *testing.T) {
	h := newSchedulesHandlerForTest()
	rec := postSchedule(h, map[string]any{
		"name":     strings.Repeat("n", 129),
		"schedule": "0 * * * *",
		"prompt":   "do something",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("name > 128 chars: expected 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "name") {
		t.Error("error message should mention 'name'")
	}
}

func TestSchedules_ValidRequest_Accepted(t *testing.T) {
	h := newSchedulesHandlerForTest()
	rec := postSchedule(h, map[string]any{
		"name":     "daily-digest",
		"schedule": "0 8 * * *",
		"prompt":   "Summarize today's news",
		"reason":   "Daily briefing",
	})
	if rec.Code != http.StatusCreated {
		t.Errorf("valid schedule: expected 201, got %d — body: %s", rec.Code, rec.Body.String())
	}
}

func TestSchedules_MissingRequiredFields(t *testing.T) {
	h := newSchedulesHandlerForTest()
	rec := postSchedule(h, map[string]any{
		"prompt": "do something",
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing name+schedule: expected 400, got %d", rec.Code)
	}
}
