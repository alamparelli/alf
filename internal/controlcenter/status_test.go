package controlcenter

import (
	"testing"
)

// mockUpdater implements UpdateChecker for testing.
type mockUpdater struct {
	version string
}

func (m *mockUpdater) LatestVersion() string { return m.version }

func TestDaemonStatus_NoUpdater(t *testing.T) {
	stats := NewStats()
	sp := NewStatusProvider(stats, "v1.0.0", nil)

	ds := sp.Status()
	if ds.UpdateAvailable != "" {
		t.Errorf("UpdateAvailable = %q, want empty (no updater)", ds.UpdateAvailable)
	}
}

func TestDaemonStatus_UpdateAvailable(t *testing.T) {
	stats := NewStats()
	sp := NewStatusProvider(stats, "v1.0.0", nil)
	sp.SetUpdater(&mockUpdater{version: "v2.0.0"})

	ds := sp.Status()
	if ds.UpdateAvailable != "v2.0.0" {
		t.Errorf("UpdateAvailable = %q, want %q", ds.UpdateAvailable, "v2.0.0")
	}
}

func TestDaemonStatus_UpdaterReturnsEmpty(t *testing.T) {
	stats := NewStats()
	sp := NewStatusProvider(stats, "v1.0.0", nil)
	sp.SetUpdater(&mockUpdater{version: ""})

	ds := sp.Status()
	if ds.UpdateAvailable != "" {
		t.Errorf("UpdateAvailable = %q, want empty (up-to-date)", ds.UpdateAvailable)
	}
}

func TestDaemonStatus_BasicFields(t *testing.T) {
	stats := NewStats()
	sp := NewStatusProvider(stats, "v1.2.3", nil)

	ds := sp.Status()
	if ds.Status != "running" {
		t.Errorf("Status = %q, want %q", ds.Status, "running")
	}
	if ds.Version != "v1.2.3" {
		t.Errorf("Version = %q, want %q", ds.Version, "v1.2.3")
	}
}
