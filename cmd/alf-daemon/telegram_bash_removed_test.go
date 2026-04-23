package main

import (
	"os"
	"strings"
	"testing"
)

// Regression guard for #385-4: the /bash Telegram command must stay
// removed. If the bot token leaks (or a capability with bash perm
// exfiltrates it), /bash would give an attacker shell via DM. SSH
// covers the "remote shell" use case; the Telegram surface must not.
//
// This scans the source directly because handleCommand wiring needs a
// live engine/tg/orch — scanning is cheap and catches a re-add in
// whichever form (case label, help string, menu command, helper func).
func TestTelegramBashCommandStaysRemoved(t *testing.T) {
	paths := []string{"telegram.go", "main.go"}
	banned := []string{
		`case "/bash":`,
		`"/bash - `,
		`execBashCommand`,
		`"bash", Description:`,
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		src := string(data)
		for _, b := range banned {
			if strings.Contains(src, b) {
				t.Errorf("%s still contains banned marker %q — /bash was re-added, see #385-4", p, b)
			}
		}
	}
}
