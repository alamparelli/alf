package firewall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStore_Load_DefaultsWhenMissing(t *testing.T) {
	s := NewStore(t.TempDir())

	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	if cfg.Mode != ModeLogOnly {
		t.Errorf("default mode = %q, want %q", cfg.Mode, ModeLogOnly)
	}
	if cfg.Port != 4751 {
		t.Errorf("default port = %d, want 4751", cfg.Port)
	}
	if len(cfg.Rules) != 0 {
		t.Errorf("default rules length = %d, want 0", len(cfg.Rules))
	}
}

func TestStore_Save_Load_Roundtrip(t *testing.T) {
	s := NewStore(t.TempDir())

	original := &Config{
		Mode: ModeEnforce,
		Port: 9999,
		Rules: []Rule{
			{Pattern: "*.anthropic.com", Action: "allow"},
			{Pattern: "*", Action: "deny"},
		},
	}
	if err := s.Save(original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Mode != original.Mode || got.Port != original.Port {
		t.Errorf("roundtrip mismatch: got mode=%q port=%d, want %q %d", got.Mode, got.Port, original.Mode, original.Port)
	}
	if len(got.Rules) != 2 || got.Rules[0].Pattern != "*.anthropic.com" || got.Rules[1].Action != "deny" {
		t.Errorf("rules roundtrip mismatch: %+v", got.Rules)
	}
}

func TestStore_Load_MalformedReturnsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "firewall.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed malformed: %v", err)
	}
	s := NewStore(dir)

	if _, err := s.Load(); err == nil {
		t.Fatal("expected error on malformed config, got nil")
	}
}

func TestStore_Save_WritesTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	if err := s.Save(DefaultConfig()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "firewall.json"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Errorf("expected trailing newline, got %q", string(data[max(0, len(data)-5):]))
	}
}

func TestStore_RecordHost_Accumulates(t *testing.T) {
	s := NewStore(t.TempDir())

	s.RecordHost("api.telegram.org", false, false)
	s.RecordHost("api.telegram.org", false, false)
	s.RecordHost("api.telegram.org", true, false) // one blocked

	hosts := s.Hosts()
	if len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %d", len(hosts))
	}
	h := hosts[0]
	if h.Host != "api.telegram.org" {
		t.Errorf("host name = %q", h.Host)
	}
	if h.Count != 3 {
		t.Errorf("count = %d, want 3", h.Count)
	}
	if h.Allowed != 2 || h.Blocked != 1 {
		t.Errorf("allowed=%d blocked=%d, want 2/1", h.Allowed, h.Blocked)
	}
	if h.LastSeen.IsZero() {
		t.Error("LastSeen should be set")
	}
	if h.Vault {
		t.Error("Vault flag set without vault traffic")
	}
}

func TestStore_RecordHost_VaultStickiness(t *testing.T) {
	s := NewStore(t.TempDir())

	// One vault request — flag should be set.
	s.RecordHost("openrouter.ai", false, true)
	// Subsequent non-vault requests to the same host must NOT unset the flag.
	s.RecordHost("openrouter.ai", false, false)

	hosts := s.Hosts()
	if len(hosts) != 1 {
		t.Fatalf("want 1 host, got %d", len(hosts))
	}
	if !hosts[0].Vault {
		t.Error("Vault flag unset by later non-vault record")
	}
}

func TestStore_SaveHosts_LoadHosts_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	s.RecordHost("a.com", false, false)
	s.RecordHost("a.com", true, false)
	s.RecordHost("b.com", false, true)
	s.SaveHosts()

	// Fresh Store on the same dir — constructor calls loadHosts().
	s2 := NewStore(dir)
	got := s2.Hosts()
	if len(got) != 2 {
		t.Fatalf("expected 2 hosts after reload, got %d", len(got))
	}

	byHost := map[string]HostStat{}
	for _, h := range got {
		byHost[h.Host] = h
	}
	if a, ok := byHost["a.com"]; !ok || a.Count != 2 || a.Allowed != 1 || a.Blocked != 1 {
		t.Errorf("a.com reload mismatch: %+v", a)
	}
	if b, ok := byHost["b.com"]; !ok || !b.Vault {
		t.Errorf("b.com reload missing vault flag: %+v", b)
	}
}

func TestStore_SaveHosts_FileFormat(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.RecordHost("x.com", true, false)
	s.SaveHosts()

	raw, err := os.ReadFile(filepath.Join(dir, "firewall-hosts.json"))
	if err != nil {
		t.Fatalf("read hosts file: %v", err)
	}
	var parsed map[string]*HostStat
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("hosts file not valid JSON: %v\n%s", err, string(raw))
	}
	if _, ok := parsed["x.com"]; !ok {
		t.Errorf("x.com missing from hosts file: %+v", parsed)
	}
}

func TestStore_Hosts_EmptyInitially(t *testing.T) {
	s := NewStore(t.TempDir())
	if got := s.Hosts(); len(got) != 0 {
		t.Errorf("expected 0 hosts, got %d", len(got))
	}
}
