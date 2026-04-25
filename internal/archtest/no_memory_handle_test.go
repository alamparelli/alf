// Archtest rule for #400 — pin §3.2 Tier 3.2 (memory agent-mediated):
// no `MemoryHandle` type may exist anywhere in the capability or
// runtime layer. Memory access must flow through the LLM driver as
// agent-mediated tools, never through a structural ocap handle.
//
// Background: Tier 3.1 (structural ocap) is appropriate for external
// I/O — fs, http, exec, secrets — where the threat is irreversible
// exfiltration and the user expects "this capability cannot reach my
// disk unless declared". Memory has a different mental model: the
// user treats memory as "mine, mediated by my agent". Adding a
// MemoryHandle would project the wrong model and create a structural
// disclosure path that bypasses the kernel-prompt rules.
//
// This archtest catches the "looks like other handles, just add one"
// drift early.
package archtest_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoMemoryHandleType enforces §3.2 by walking the codebase for any
// type declaration matching `MemoryHandle` or `*MemoryHandle`. The
// kernel-prompt + tool-surface model is incompatible with a handle
// type — adding one would silently weaken the agent-mediation guarantee.
//
// If you genuinely need a per-capability memory scoping mechanism,
// reopen the §3.2 / §3.1 boundary discussion before adding the type.
// Memory disclosure should remain a tool the LLM gates, not a
// structural primitive a capability holds.
func TestNoMemoryHandleType(t *testing.T) {
	root := repoRoot()

	// Match: `type MemoryHandle struct {`, `type MemoryHandle interface`,
	// `type MemoryHandle = ...` (alias), and the pointer field form
	// `MemoryHandle *MemoryHandle` inside another struct (which would
	// also imply the type exists somewhere). The first 3 patterns
	// alone are sufficient — if the type exists, one of them matches.
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?m)^\s*type\s+MemoryHandle\s+(struct|interface|=)\b`),
	}

	var violations []string
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipOcapDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		// Skip this archtest file — the regex matches the literal
		// "MemoryHandle" inside its own pattern strings as data.
		if rel == filepath.Join("internal", "archtest", "no_memory_handle_test.go") {
			return nil
		}
		for _, re := range patterns {
			if re.Match(b) {
				violations = append(violations, rel)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}
	for _, v := range violations {
		t.Errorf("§3.2 violation: %s declares a MemoryHandle type.\n"+
			"Memory is Tier 3.2 (agent-mediated), not Tier 3.1 (structural ocap).\n"+
			"Capabilities access memory by asking the LLM driver — never via a handle.\n"+
			"If you genuinely need per-capability memory scoping, open the §3.2 boundary\n"+
			"discussion before adding the type.", v)
	}
}

// TestKernelPromptIsImported sanity-checks that the daemon imports
// internal/runtime/llm and calls SetKernelPrompt — without the wiring,
// the kernel prompt artifact exists but no LLM call sees it. Catches
// a refactor that accidentally drops the wiring while keeping the
// package compileable.
func TestKernelPromptIsImported(t *testing.T) {
	root := repoRoot()
	daemonMain := filepath.Join(root, "cmd", "alf-daemon", "main.go")

	b, err := os.ReadFile(daemonMain)
	if err != nil {
		t.Fatalf("read %s: %v", daemonMain, err)
	}
	src := string(b)
	if !strings.Contains(src, `"github.com/alamparelli/alf/internal/runtime/llm"`) {
		t.Error(`cmd/alf-daemon/main.go does not import internal/runtime/llm — kernel prompt cannot be wired`)
	}
	if !strings.Contains(src, "SetKernelPrompt(llm.KernelPrompt())") {
		t.Error(`cmd/alf-daemon/main.go does not call registry.SetKernelPrompt(llm.KernelPrompt()) — every LLM call would skip §3.2 rules`)
	}
}
