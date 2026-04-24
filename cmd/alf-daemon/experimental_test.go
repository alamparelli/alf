package main

import (
	"strings"
	"testing"
)

func TestRequireExperimentalGate_AllowsWhenOne(t *testing.T) {
	getenv := func(k string) string {
		if k == "ALF_EXPERIMENTAL" {
			return "1"
		}
		return ""
	}
	if err := requireExperimentalGate(getenv); err != nil {
		t.Fatalf("gate closed with ALF_EXPERIMENTAL=1: %v", err)
	}
}

func TestRequireExperimentalGate_BlocksWhenUnset(t *testing.T) {
	err := requireExperimentalGate(func(string) string { return "" })
	if err == nil {
		t.Fatal("gate open without ALF_EXPERIMENTAL set")
	}
	if !strings.Contains(err.Error(), "ALF_EXPERIMENTAL=1") {
		t.Errorf("error must name the env var so operators know what to set; got %q", err)
	}
	if !strings.Contains(err.Error(), "#406") {
		t.Errorf("error must link to the tracking ticket; got %q", err)
	}
}

func TestRequireExperimentalGate_BlocksOnWrongValue(t *testing.T) {
	for _, v := range []string{"0", "true", "yes", "ALF_EXPERIMENTAL=1"} {
		getenv := func(k string) string {
			if k == "ALF_EXPERIMENTAL" {
				return v
			}
			return ""
		}
		if err := requireExperimentalGate(getenv); err == nil {
			t.Errorf("gate accepted ALF_EXPERIMENTAL=%q — must require exactly %q", v, "1")
		}
	}
}

func TestExperimentalBanner_MentionsLackOfIsolation(t *testing.T) {
	// The banner is the second line of defense after the gate. It must be
	// impossible to misread — operators seeing it in `journalctl` or docker
	// logs must understand what state the daemon is in.
	if !strings.Contains(experimentalBanner, "NO ISOLATION") {
		t.Error("banner must contain 'NO ISOLATION' — that is the whole point")
	}
	if !strings.Contains(experimentalBanner, "#406") {
		t.Error("banner must link to #406 so readers can dig deeper")
	}
}
