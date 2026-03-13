package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	cc "github.com/alamparelli/alf/internal/controlcenter"
)

// collectEvents drains reloadCh for the given duration and returns all received events.
func collectEvents(ch <-chan cc.ReloadEvent, wait time.Duration) []cc.ReloadEvent {
	var events []cc.ReloadEvent
	deadline := time.After(wait)
	for {
		select {
		case e := <-ch:
			events = append(events, e)
		case <-deadline:
			return events
		}
	}
}

func hasEvent(events []cc.ReloadEvent, target cc.ReloadEvent) bool {
	for _, e := range events {
		if e == target {
			return true
		}
	}
	return false
}

func TestWatchConfigFiles_ConfigChange(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()

	// Create initial config.json so the watcher seeds its modtime.
	cfgPath := filepath.Join(configDir, "config.json")
	os.WriteFile(cfgPath, []byte(`{}`), 0o644)

	// Create teams dir.
	os.MkdirAll(filepath.Join(dataDir, "agents", "teams"), 0o755)

	reloadCh := make(chan cc.ReloadEvent, 10)
	tiersPath := filepath.Join(configDir, "tiers.json")
	os.WriteFile(tiersPath, []byte(`{}`), 0o644)

	go watchConfigFiles(configDir, dataDir, func() string { return tiersPath }, reloadCh)

	// Let the watcher seed initial modtimes (first tick at 2s).
	time.Sleep(3 * time.Second)

	// Modify config.json.
	os.WriteFile(cfgPath, []byte(`{"updated": true}`), 0o644)

	events := collectEvents(reloadCh, 4*time.Second)
	if !hasEvent(events, cc.ReloadConfig) {
		t.Errorf("expected ReloadConfig event after config.json change, got %v", events)
	}
}

func TestWatchConfigFiles_TiersChange(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()

	os.MkdirAll(filepath.Join(dataDir, "agents", "teams"), 0o755)

	reloadCh := make(chan cc.ReloadEvent, 10)
	tiersPath := filepath.Join(configDir, "tiers.json")
	os.WriteFile(tiersPath, []byte(`{}`), 0o644)

	go watchConfigFiles(configDir, dataDir, func() string { return tiersPath }, reloadCh)

	time.Sleep(3 * time.Second)

	// Modify tiers.json.
	os.WriteFile(tiersPath, []byte(`{"changed": true}`), 0o644)

	events := collectEvents(reloadCh, 4*time.Second)
	if !hasEvent(events, cc.ReloadTiers) {
		t.Errorf("expected ReloadTiers event after tiers.json change, got %v", events)
	}
}

func TestWatchConfigFiles_TeamsNewFile(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()

	teamsDir := filepath.Join(dataDir, "agents", "teams")
	os.MkdirAll(teamsDir, 0o755)

	reloadCh := make(chan cc.ReloadEvent, 10)
	tiersPath := filepath.Join(configDir, "tiers.json")
	os.WriteFile(tiersPath, []byte(`{}`), 0o644)

	go watchConfigFiles(configDir, dataDir, func() string { return tiersPath }, reloadCh)

	time.Sleep(3 * time.Second)

	// Add a new team file - simulates ALF writing via tool call.
	os.WriteFile(filepath.Join(teamsDir, "crypto-bot-team.json"), []byte(`{"name":"crypto"}`), 0o644)

	events := collectEvents(reloadCh, 4*time.Second)
	if !hasEvent(events, cc.ReloadAgents) {
		t.Errorf("expected ReloadAgents event after new team file, got %v", events)
	}
}

func TestWatchConfigFiles_TeamsModifyFile(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()

	teamsDir := filepath.Join(dataDir, "agents", "teams")
	os.MkdirAll(teamsDir, 0o755)

	// Create an existing team file.
	teamFile := filepath.Join(teamsDir, "existing.json")
	os.WriteFile(teamFile, []byte(`{"name":"old"}`), 0o644)

	reloadCh := make(chan cc.ReloadEvent, 10)
	tiersPath := filepath.Join(configDir, "tiers.json")
	os.WriteFile(tiersPath, []byte(`{}`), 0o644)

	go watchConfigFiles(configDir, dataDir, func() string { return tiersPath }, reloadCh)

	time.Sleep(3 * time.Second)

	// Modify existing team file.
	os.WriteFile(teamFile, []byte(`{"name":"updated"}`), 0o644)

	events := collectEvents(reloadCh, 4*time.Second)
	if !hasEvent(events, cc.ReloadAgents) {
		t.Errorf("expected ReloadAgents event after team file modification, got %v", events)
	}
}

func TestWatchConfigFiles_TeamsDeleteFile(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()

	teamsDir := filepath.Join(dataDir, "agents", "teams")
	os.MkdirAll(teamsDir, 0o755)

	// Create a team file to delete later.
	teamFile := filepath.Join(teamsDir, "to-delete.json")
	os.WriteFile(teamFile, []byte(`{"name":"doomed"}`), 0o644)

	reloadCh := make(chan cc.ReloadEvent, 10)
	tiersPath := filepath.Join(configDir, "tiers.json")
	os.WriteFile(tiersPath, []byte(`{}`), 0o644)

	go watchConfigFiles(configDir, dataDir, func() string { return tiersPath }, reloadCh)

	time.Sleep(3 * time.Second)

	// Delete the team file.
	os.Remove(teamFile)

	events := collectEvents(reloadCh, 4*time.Second)
	if !hasEvent(events, cc.ReloadAgents) {
		t.Errorf("expected ReloadAgents event after team file deletion, got %v", events)
	}
}

func TestWatchConfigFiles_NoSpuriousEvents(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()

	cfgPath := filepath.Join(configDir, "config.json")
	os.WriteFile(cfgPath, []byte(`{}`), 0o644)
	os.MkdirAll(filepath.Join(dataDir, "agents", "teams"), 0o755)

	reloadCh := make(chan cc.ReloadEvent, 10)
	tiersPath := filepath.Join(configDir, "tiers.json")
	os.WriteFile(tiersPath, []byte(`{}`), 0o644)

	go watchConfigFiles(configDir, dataDir, func() string { return tiersPath }, reloadCh)

	// Wait long enough for multiple ticks with no changes.
	events := collectEvents(reloadCh, 5*time.Second)
	if len(events) > 0 {
		t.Errorf("expected no events when nothing changed, got %v", events)
	}
}

func TestWatchConfigFiles_FirewallChange(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()

	fwPath := filepath.Join(configDir, "firewall.json")
	os.WriteFile(fwPath, []byte(`{}`), 0o644)
	os.MkdirAll(filepath.Join(dataDir, "agents", "teams"), 0o755)

	reloadCh := make(chan cc.ReloadEvent, 10)
	tiersPath := filepath.Join(configDir, "tiers.json")
	os.WriteFile(tiersPath, []byte(`{}`), 0o644)

	go watchConfigFiles(configDir, dataDir, func() string { return tiersPath }, reloadCh)

	time.Sleep(3 * time.Second)

	os.WriteFile(fwPath, []byte(`{"mode":"enforce"}`), 0o644)

	events := collectEvents(reloadCh, 4*time.Second)
	if !hasEvent(events, cc.ReloadFirewall) {
		t.Errorf("expected ReloadFirewall event after firewall.json change, got %v", events)
	}
}
