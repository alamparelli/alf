package scheduler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseHeartbeatFull_NoFrontmatter(t *testing.T) {
	tier, schedule, body := parseHeartbeatFull("Just some prose without frontmatter.")
	if tier != "" || schedule != "" {
		t.Errorf("expected empty tier/schedule, got %q / %q", tier, schedule)
	}
	if body != "Just some prose without frontmatter." {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestParseHeartbeatFull_WithFrontmatter(t *testing.T) {
	content := `---
tier: fast
schedule: "*/5 * * * *"
---

The heartbeat body content.
`
	tier, schedule, body := parseHeartbeatFull(content)
	if tier != "fast" {
		t.Errorf("tier: got %q, want %q", tier, "fast")
	}
	if schedule != "*/5 * * * *" {
		t.Errorf("schedule: got %q, want %q", schedule, "*/5 * * * *")
	}
	if body != "The heartbeat body content." {
		t.Errorf("body: got %q", body)
	}
}

func TestParseHeartbeatFull_SingleQuotedValues(t *testing.T) {
	content := `---
tier: 'smart'
schedule: '0 9 * * *'
---
body line
`
	tier, schedule, _ := parseHeartbeatFull(content)
	if tier != "smart" {
		t.Errorf("tier: got %q, want %q", tier, "smart")
	}
	if schedule != "0 9 * * *" {
		t.Errorf("schedule: got %q", schedule)
	}
}

func TestParseHeartbeatFull_BOM(t *testing.T) {
	content := "\xef\xbb\xbf---\ntier: fast\n---\nhello\n"
	tier, _, body := parseHeartbeatFull(content)
	if tier != "fast" {
		t.Errorf("BOM should be stripped, tier=%q", tier)
	}
	if body != "hello" {
		t.Errorf("unexpected body after BOM: %q", body)
	}
}

func TestParseHeartbeatFull_FrontmatterOnly(t *testing.T) {
	content := `---
tier: fast
---`
	tier, _, body := parseHeartbeatFull(content)
	if tier != "fast" {
		t.Errorf("tier: got %q", tier)
	}
	// With no closing boundary beyond the second "---", body should be the trimmed content.
	if body == "" {
		t.Errorf("expected non-empty body fallback, got %q", body)
	}
}

func TestParseHeartbeatFull_IgnoresUnknownKeys(t *testing.T) {
	content := `---
tier: fast
weird_key: something
---
body`
	tier, schedule, body := parseHeartbeatFull(content)
	if tier != "fast" || schedule != "" {
		t.Errorf("unexpected tier/schedule: %q / %q", tier, schedule)
	}
	if body != "body" {
		t.Errorf("body: %q", body)
	}
}

func TestParseHeartbeatMeta_MissingFile(t *testing.T) {
	tier, schedule := parseHeartbeatMeta(filepath.Join(t.TempDir(), "nope.md"))
	if tier != "" || schedule != "" {
		t.Errorf("missing file should yield empty meta, got %q / %q", tier, schedule)
	}
}

func TestParseHeartbeatMeta_ReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "heartbeat.md")
	os.WriteFile(path, []byte("---\ntier: smart\nschedule: @hourly\n---\nbody\n"), 0o644)

	tier, schedule := parseHeartbeatMeta(path)
	if tier != "smart" {
		t.Errorf("tier: got %q", tier)
	}
	if schedule != "@hourly" {
		t.Errorf("schedule: got %q", schedule)
	}
}

func TestParseHeartbeatMetaExported(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "heartbeat.md"), []byte("---\ntier: fast\n---\n"), 0o644)

	tier, _ := ParseHeartbeatMeta(dir)
	if tier != "fast" {
		t.Errorf("tier: got %q", tier)
	}
}
