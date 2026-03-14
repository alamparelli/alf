package controlcenter

import (
	"testing"
)

func TestEffectiveDNS_DefaultsWhenEmpty(t *testing.T) {
	cfg := &Config{}
	dns := cfg.EffectiveDNS()
	if len(dns) != 2 || dns[0] != "8.8.8.8" || dns[1] != "1.1.1.1" {
		t.Errorf("expected default DNS [8.8.8.8, 1.1.1.1], got %v", dns)
	}
}

func TestEffectiveDNS_UsesConfigured(t *testing.T) {
	cfg := &Config{DNSServers: []string{"9.9.9.9", "149.112.112.112"}}
	dns := cfg.EffectiveDNS()
	if len(dns) != 2 || dns[0] != "9.9.9.9" {
		t.Errorf("expected configured DNS, got %v", dns)
	}
}

func TestEffectiveDNS_SingleServer(t *testing.T) {
	cfg := &Config{DNSServers: []string{"10.0.0.1"}}
	dns := cfg.EffectiveDNS()
	if len(dns) != 1 || dns[0] != "10.0.0.1" {
		t.Errorf("expected single DNS, got %v", dns)
	}
}
