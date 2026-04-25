package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SnapshotFile is the filename written under <dataDir>/events/ each
// time the loader completes a scan. The file is the data path that
// #395 (CC ratification page) will read to render an interactive
// review surface; for now it is the only persistent record of which
// cross-flows are active in this daemon process.
const SnapshotFile = "active-flows.json"

// FlowEntry is one cross-flow record — publisher / topic / subscriber
// triple plus the boot-time timestamp at which the flow was forged.
// Sorted deterministically (publisher, topic, subscriber) so JSON diffs
// across boots are meaningful.
type FlowEntry struct {
	Publisher     string `json:"publisher"`
	Topic         string `json:"topic"`
	Subscriber    string `json:"subscriber"`
	EstablishedAt string `json:"established_at"`
}

// WriteSnapshot serialises flows to <dataDir>/events/active-flows.json
// with deterministic sort + 0o644 perms. Atomic via tmp-then-rename so
// a crash mid-write cannot leave a half-written file.
//
// An empty flows slice still writes a valid empty array — observers
// can distinguish "loader ran, zero flows" from "loader did not run".
func WriteSnapshot(dataDir string, flows []FlowEntry, now func() time.Time) error {
	if dataDir == "" {
		return fmt.Errorf("events: WriteSnapshot requires non-empty dataDir")
	}
	if now == nil {
		now = time.Now
	}
	dir := filepath.Join(dataDir, "events")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("events: mkdir %s: %w", dir, err)
	}

	// Stable sort + populate empty timestamps with the snapshot time
	// (the loader passes empty for fresh flows so we backfill here).
	sort.SliceStable(flows, func(i, j int) bool {
		if flows[i].Publisher != flows[j].Publisher {
			return flows[i].Publisher < flows[j].Publisher
		}
		if flows[i].Topic != flows[j].Topic {
			return flows[i].Topic < flows[j].Topic
		}
		return flows[i].Subscriber < flows[j].Subscriber
	})
	stamp := now().UTC().Format(time.RFC3339)
	for i := range flows {
		if flows[i].EstablishedAt == "" {
			flows[i].EstablishedAt = stamp
		}
	}

	out, err := json.MarshalIndent(flows, "", "  ")
	if err != nil {
		return fmt.Errorf("events: marshal snapshot: %w", err)
	}
	out = append(out, '\n')

	final := filepath.Join(dir, SnapshotFile)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("events: write tmp snapshot: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("events: rename snapshot: %w", err)
	}
	return nil
}
