package provider

import (
	"testing"
)

// TestSummarizeSystemPrompts_KernelPresent pins the load-bearing audit
// signal: every chat invocation must show kernel_present=true in the
// LLM log. Detected during 0.8.0-beta soak — the existing invoke logs
// dropped params.SystemPrompts entirely, so post-hoc verification of
// the §3.2 wiring required reading code rather than data.
func TestSummarizeSystemPrompts_KernelPresent(t *testing.T) {
	prompts := []string{
		"# ALF KERNEL INSTRUCTIONS — AUTHORITATIVE, ALWAYS APPLY\nrules...",
		"skill prompt body",
	}
	got := summarizeSystemPrompts(prompts)
	if got["system_kernel_present"] != true {
		t.Errorf("system_kernel_present = %v, want true", got["system_kernel_present"])
	}
	if got["system_count"] != 2 {
		t.Errorf("system_count = %v, want 2", got["system_count"])
	}
	if got["system_nonce_unsubstituted"] != false {
		t.Errorf("system_nonce_unsubstituted = %v, want false", got["system_nonce_unsubstituted"])
	}
	if h, ok := got["system_sha256"].(string); !ok || len(h) != 16 {
		t.Errorf("system_sha256 = %v, want 16-hex prefix", got["system_sha256"])
	}
}

// TestSummarizeSystemPrompts_KernelAbsent flags the regression case:
// SystemPrompts present but no kernel marker — means a dispatch path
// bypassed KernelPromptInjector. The audit log surfaces this so soak
// triage doesn't have to read source.
func TestSummarizeSystemPrompts_KernelAbsent(t *testing.T) {
	prompts := []string{"plain skill prompt", "tier instructions"}
	got := summarizeSystemPrompts(prompts)
	if got["system_kernel_present"] != false {
		t.Errorf("system_kernel_present = %v, want false", got["system_kernel_present"])
	}
}

// TestSummarizeSystemPrompts_NonceUnsubstituted catches the SEC-002
// regression: a literal "{NONCE}" placeholder that escaped substitution
// reaches the model's system prompt. Must surface in the log so the
// operator knows the per-Invoke nonce wiring failed for this invocation.
func TestSummarizeSystemPrompts_NonceUnsubstituted(t *testing.T) {
	prompts := []string{"# ALF KERNEL INSTRUCTIONS\nuse <tool_output_{NONCE}>...</tool_output_{NONCE}>"}
	got := summarizeSystemPrompts(prompts)
	if got["system_nonce_unsubstituted"] != true {
		t.Errorf("system_nonce_unsubstituted = %v, want true (placeholder leaked)", got["system_nonce_unsubstituted"])
	}
}

// TestSummarizeSystemPrompts_NonceSubstituted confirms that real hex
// nonces in place of the placeholder do NOT trigger the unsubstituted
// flag — the check must be precise about the literal placeholder, not
// any occurrence of "NONCE" or curly braces.
func TestSummarizeSystemPrompts_NonceSubstituted(t *testing.T) {
	prompts := []string{"# ALF KERNEL INSTRUCTIONS\nuse <tool_output_a1b2c3d4e5f60708>...</tool_output_a1b2c3d4e5f60708>"}
	got := summarizeSystemPrompts(prompts)
	if got["system_nonce_unsubstituted"] != false {
		t.Errorf("system_nonce_unsubstituted = %v, want false (real hex nonce)", got["system_nonce_unsubstituted"])
	}
}

// TestSummarizeSystemPrompts_Empty returns the zero shape without
// computing a hash. Callers can rely on the keys always being present
// so the JSONL log schema stays stable across invocations with and
// without system prompts.
func TestSummarizeSystemPrompts_Empty(t *testing.T) {
	got := summarizeSystemPrompts(nil)
	if got["system_count"] != 0 || got["system_total_len"] != 0 {
		t.Errorf("zero shape: count=%v total_len=%v, want 0/0", got["system_count"], got["system_total_len"])
	}
	if got["system_kernel_present"] != false || got["system_nonce_unsubstituted"] != false {
		t.Errorf("zero shape: kernel/nonce flags should be false, got %v / %v", got["system_kernel_present"], got["system_nonce_unsubstituted"])
	}
	if _, has := got["system_sha256"]; has {
		t.Errorf("zero shape should not include sha256, got %v", got["system_sha256"])
	}
}

// TestSummarizeSystemPrompts_Sha256Stable confirms the joined-prompt
// hash is deterministic — equal inputs in the same order produce the
// same prefix. Non-stable hashing would defeat its purpose as a
// correlation key across log entries.
func TestSummarizeSystemPrompts_Sha256Stable(t *testing.T) {
	a := summarizeSystemPrompts([]string{"one", "two"})
	b := summarizeSystemPrompts([]string{"one", "two"})
	if a["system_sha256"] != b["system_sha256"] {
		t.Errorf("sha256 mismatch: %v vs %v", a["system_sha256"], b["system_sha256"])
	}
	c := summarizeSystemPrompts([]string{"two", "one"})
	if a["system_sha256"] == c["system_sha256"] {
		t.Errorf("sha256 should be order-sensitive, got identical: %v", a["system_sha256"])
	}
}

// TestMergeFields confirms the helper folds extra into base with
// later-wins semantics, used by the invoke logger to splice the
// system-prompt summary into the per-provider field set.
func TestMergeFields(t *testing.T) {
	base := map[string]any{"a": 1, "b": 2}
	extra := map[string]any{"b": 99, "c": 3}
	got := mergeFields(base, extra)
	if got["a"] != 1 || got["b"] != 99 || got["c"] != 3 {
		t.Errorf("mergeFields: got %v, want a=1 b=99 c=3", got)
	}
}
