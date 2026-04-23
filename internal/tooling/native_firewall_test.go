package tooling

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeFirewallService struct {
	entries []FirewallEntry
	hosts   []FirewallHostStat
}

func (f *fakeFirewallService) RecentEntries(limit int) []FirewallEntry {
	if limit > 0 && limit < len(f.entries) {
		return f.entries[:limit]
	}
	return f.entries
}
func (f *fakeFirewallService) Hosts() []FirewallHostStat { return f.hosts }

func sampleEntries() []FirewallEntry {
	now := time.Date(2026, 1, 1, 12, 34, 56, 0, time.UTC)
	return []FirewallEntry{
		{Time: now, Method: "GET", Host: "api.example.com", Path: "/v1/ping", Blocked: false, Source: "vault"},
		{Time: now, Method: "POST", Host: "tracker.bad", Path: "/collect", Blocked: true},
	}
}

func TestFormatEntries(t *testing.T) {
	out := formatEntries(sampleEntries())
	if !strings.Contains(out, "api.example.com") {
		t.Errorf("missing host: %s", out)
	}
	if !strings.Contains(out, "BLOCKED") {
		t.Errorf("expected BLOCKED marker, got %s", out)
	}
	if !strings.Contains(out, "[vault]") {
		t.Errorf("expected [vault] source tag, got %s", out)
	}
}

func TestFormatHosts(t *testing.T) {
	hosts := []FirewallHostStat{
		{Host: "api.example.com", Count: 10, Allowed: 8, Blocked: 2, Vault: true},
		{Host: "other.com", Count: 5, Allowed: 5, Blocked: 0},
	}
	out := formatHosts(hosts)
	if !strings.Contains(out, "HOST") || !strings.Contains(out, "ALLOWED") {
		t.Errorf("header missing: %s", out)
	}
	if !strings.Contains(out, "api.example.com") || !strings.Contains(out, "vault") {
		t.Errorf("entry missing: %s", out)
	}
}

func TestFirewallNativeTool_Recent(t *testing.T) {
	tool := FirewallNativeTool{Service: &fakeFirewallService{entries: sampleEntries()}}
	out, err := tool.Run(context.Background(), `{"action":"recent","limit":10}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "api.example.com") {
		t.Errorf("expected entry in output: %s", out)
	}
}

func TestFirewallNativeTool_RecentEmpty(t *testing.T) {
	tool := FirewallNativeTool{Service: &fakeFirewallService{}}
	out, err := tool.Run(context.Background(), `{"action":"recent"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "No recent firewall entries." {
		t.Errorf("unexpected: %s", out)
	}
}

func TestFirewallNativeTool_Hosts(t *testing.T) {
	tool := FirewallNativeTool{Service: &fakeFirewallService{
		hosts: []FirewallHostStat{{Host: "api.example.com", Count: 1, Allowed: 1}},
	}}
	out, err := tool.Run(context.Background(), `{"action":"hosts"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "api.example.com") {
		t.Errorf("expected host in output: %s", out)
	}
}

func TestFirewallNativeTool_HostsEmpty(t *testing.T) {
	tool := FirewallNativeTool{Service: &fakeFirewallService{}}
	out, err := tool.Run(context.Background(), `{"action":"hosts"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "No hosts recorded." {
		t.Errorf("unexpected: %s", out)
	}
}

func TestFirewallNativeTool_Search_NoQuery(t *testing.T) {
	tool := FirewallNativeTool{Service: &fakeFirewallService{entries: sampleEntries()}}
	_, err := tool.Run(context.Background(), `{"action":"search"}`)
	if err == nil {
		t.Error("expected error when query is empty")
	}
}

func TestFirewallNativeTool_Search_Matches(t *testing.T) {
	tool := FirewallNativeTool{Service: &fakeFirewallService{entries: sampleEntries()}}
	out, err := tool.Run(context.Background(), `{"action":"search","query":"example"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "api.example.com") {
		t.Errorf("expected match in output: %s", out)
	}
}

func TestFirewallNativeTool_Search_NoMatch(t *testing.T) {
	tool := FirewallNativeTool{Service: &fakeFirewallService{entries: sampleEntries()}}
	out, err := tool.Run(context.Background(), `{"action":"search","query":"zzz"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, `No entries matching "zzz"`) {
		t.Errorf("expected no-match message, got: %s", out)
	}
}

func TestFirewallNativeTool_UnknownAction(t *testing.T) {
	tool := FirewallNativeTool{Service: &fakeFirewallService{}}
	_, err := tool.Run(context.Background(), `{"action":"bogus"}`)
	if err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestFirewallNativeTool_Schema(t *testing.T) {
	var tool FirewallNativeTool
	s := tool.Schema()
	if s.Name != "firewall" {
		t.Errorf("unexpected schema name: %q", s.Name)
	}
	if s.Description == "" {
		t.Error("description must be non-empty")
	}
}

func TestFirewallNativeTool_ToolName(t *testing.T) {
	var tool FirewallNativeTool
	if tool.ToolName() != "firewall" {
		t.Error("ToolName mismatch")
	}
}
