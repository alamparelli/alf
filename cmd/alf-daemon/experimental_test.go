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
	for _, truthy := range []string{"1", "true", "TRUE", "yes", "on", " 1 ", "True"} {
		t.Run(truthy, func(t *testing.T) {
			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer log.SetOutput(os.Stderr)

			warnDeprecatedExperimentalEnv(func(k string) string {
				if k == "ALF_EXPERIMENTAL" {
					return truthy
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
		})
	}
}

// TestWarnDeprecatedExperimentalEnv_FalsyValuesSilent pins SEC-080-010:
// ALF_EXPERIMENTAL=0 / false / off / no must be treated as "operator
// already disabled it" — no warning fires. The previous implementation
// warned for any non-empty value, including =0, which surprised
// operators who had downgraded the value rather than deleting the line.
func TestWarnDeprecatedExperimentalEnv_FalsyValuesSilent(t *testing.T) {
	for _, falsy := range []string{"0", "false", "FALSE", "off", "no", "n", "f", " "} {
		t.Run(falsy, func(t *testing.T) {
			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer log.SetOutput(os.Stderr)

			warnDeprecatedExperimentalEnv(func(k string) string {
				if k == "ALF_EXPERIMENTAL" {
					return falsy
				}
				return ""
			})

			if buf.Len() != 0 {
				t.Errorf("expected no warning for falsy value %q; got %q", falsy, buf.String())
			}
		})
	}
}
