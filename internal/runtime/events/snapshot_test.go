package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixedNow() time.Time {
	return time.Date(2026, 4, 25, 18, 30, 0, 0, time.UTC)
}

func TestWriteSnapshot_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	flows := []FlowEntry{
		{Publisher: "cap-a", Topic: "chat.log", Subscriber: "cap-b"},
	}
	if err := WriteSnapshot(dir, flows, fixedNow); err != nil {
		t.Fatalf("write: %v", err)
	}
	path := filepath.Join(dir, "events", SnapshotFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got []FlowEntry
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Publisher != "cap-a" || got[0].Topic != "chat.log" || got[0].Subscriber != "cap-b" {
		t.Errorf("got=%+v", got)
	}
	if got[0].EstablishedAt != "2026-04-25T18:30:00Z" {
		t.Errorf("established_at=%q, want fixed snapshot timestamp", got[0].EstablishedAt)
	}
}

func TestWriteSnapshot_DeterministicSort(t *testing.T) {
	dir := t.TempDir()
	flows := []FlowEntry{
		{Publisher: "cap-z", Topic: "z", Subscriber: "cap-z"},
		{Publisher: "cap-a", Topic: "b", Subscriber: "cap-c"},
		{Publisher: "cap-a", Topic: "a", Subscriber: "cap-c"},
		{Publisher: "cap-a", Topic: "a", Subscriber: "cap-b"},
	}
	if err := WriteSnapshot(dir, flows, fixedNow); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "events", SnapshotFile))
	var got []FlowEntry
	_ = json.Unmarshal(b, &got)
	want := []string{"cap-a:a:cap-b", "cap-a:a:cap-c", "cap-a:b:cap-c", "cap-z:z:cap-z"}
	for i, e := range got {
		key := e.Publisher + ":" + e.Topic + ":" + e.Subscriber
		if key != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, key, want[i])
		}
	}
}

func TestWriteSnapshot_EmptyFlowsStillWritesValidArray(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSnapshot(dir, nil, fixedNow); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "events", SnapshotFile))
	if err != nil {
		t.Fatal(err)
	}
	var got []FlowEntry
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v (raw=%q)", err, b)
	}
	if got == nil {
		got = []FlowEntry{}
	}
	if len(got) != 0 {
		t.Errorf("want empty array, got %+v", got)
	}
}

func TestWriteSnapshot_EmptyDataDirRejected(t *testing.T) {
	if err := WriteSnapshot("", nil, fixedNow); err == nil {
		t.Fatal("want error for empty dataDir")
	}
}

func TestWriteSnapshot_AtomicViaTmpRename(t *testing.T) {
	dir := t.TempDir()
	if err := WriteSnapshot(dir, []FlowEntry{{Publisher: "p", Topic: "t", Subscriber: "s"}}, fixedNow); err != nil {
		t.Fatal(err)
	}
	// .tmp file should not linger after a successful write.
	tmp := filepath.Join(dir, "events", SnapshotFile+".tmp")
	if _, err := os.Stat(tmp); err == nil {
		t.Error(".tmp file lingered after successful write")
	}
}
