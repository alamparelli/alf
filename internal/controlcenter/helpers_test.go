package controlcenter

import (
	"encoding/json"
	"os"
	"testing"
)

// writeJSON writes v as JSON to path. Test helper.
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

type mockNotifier struct {
	events []ReloadEvent
}

func (m *mockNotifier) Notify(e ReloadEvent) {
	m.events = append(m.events, e)
}
