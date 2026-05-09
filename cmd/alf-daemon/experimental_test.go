package main

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

// TestWarnDeprecatedExperimentalEnv_NoEnvSilent pins that the
// strict-flip migration helper stays silent when the deprecated
// var is absent. The daemon's normal boot output is already
// noisy; the warning should fire ONLY when the operator has the
// flag still set.
func TestWarnDeprecatedExperimentalEnv_NoEnvSilent(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	warnDeprecatedExperimentalEnv(func(string) string { return "" })

	if buf.Len() != 0 {
		t.Errorf("expected no log output without env set; got %q", buf.String())
	}
}

// TestWarnDeprecatedExperimentalEnv_PresenceWarns pins the
// migration UX: an operator with ALF_EXPERIMENTAL=1 still in
// their docker-compose.yml sees a one-line deprecation warning
// pointing at the cleanup step. The daemon does NOT refuse boot.
func TestWarnDeprecatedExperimentalEnv_PresenceWarns(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	warnDeprecatedExperimentalEnv(func(k string) string {
		if k == "ALF_EXPERIMENTAL" {
			return "1"
		}
		return ""
	})

	out := buf.String()
	if !strings.Contains(out, "DEPRECATED") {
		t.Errorf("warning must label itself as DEPRECATED so it surfaces in log scans: %q", out)
	}
	if !strings.Contains(out, "ALF_EXPERIMENTAL") {
		t.Errorf("warning must name the env var: %q", out)
	}
	if !strings.Contains(out, "docker-compose") {
		t.Errorf("warning should hint at the cleanup location: %q", out)
	}
}
