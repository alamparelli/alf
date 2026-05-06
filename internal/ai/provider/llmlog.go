package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/platform/trace"
)

// LLMLogger writes structured JSONL entries for every LLM invocation
// to daily-rotated files under {dataDir}/logs/llm/.
// Separate from the daemon log to avoid polluting operational logs.
type LLMLogger struct {
	dir   string
	mu    sync.Mutex
	file  *os.File
	today string
}

var llmLog *LLMLogger

// InitLLMLog creates the package-level LLM logger.
// Must be called once at startup before any provider invocations.
func InitLLMLog(dataDir string) {
	dir := filepath.Join(dataDir, "logs", "llm")
	_ = os.MkdirAll(dir, 0o755)
	llmLog = &LLMLogger{dir: dir}
}

// CloseLLMLog closes the LLM logger file.
func CloseLLMLog() {
	if llmLog != nil {
		llmLog.mu.Lock()
		defer llmLog.mu.Unlock()
		if llmLog.file != nil {
			llmLog.file.Close()
			llmLog.file = nil
		}
	}
}

// logLLMCtx logs an LLM event with trace correlation if available.
func logLLMCtx(ctx context.Context, event string, fields map[string]any) {
	if t := trace.FromContext(ctx); t != nil {
		fields["trace_id"] = t.TraceID
	}
	logLLM(event, fields)
}

func logLLM(event string, fields map[string]any) {
	if llmLog == nil {
		return
	}
	llmLog.mu.Lock()
	defer llmLog.mu.Unlock()

	now := time.Now()
	today := now.Format("2006-01-02")

	if llmLog.today != today || llmLog.file == nil {
		if llmLog.file != nil {
			llmLog.file.Close()
		}
		path := filepath.Join(llmLog.dir, today+".jsonl")
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		llmLog.file = f
		llmLog.today = today
	}

	rec := make(map[string]any, len(fields)+2)
	rec["event"] = event
	rec["ts"] = now.Format(time.RFC3339)
	for k, v := range fields {
		rec[k] = v
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	data = append(data, '\n')
	llmLog.file.Write(data)
}

// trunc truncates a string to maxLen characters.
func trunc(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// kernelMarker is a stable substring of the §3.2 kernel prompt
// (internal/runtime/llm/kernel_prompt.txt). Used by summarizeSystemPrompts
// to flag whether the kernel was prepended for a given Invoke.
const kernelMarker = "ALF KERNEL INSTRUCTIONS"

// summarizeSystemPrompts returns a privacy-respecting audit summary of
// params.SystemPrompts for the LLM log. Plaintext system prompts can
// carry skill bodies, tier instructions, and conversation context — we
// log shape + identity (count, total length, kernel-present flag, sha256
// prefix) without dumping the bytes themselves.
//
// The kernel_present flag is the soak-window audit signal: every chat
// invocation must show kernel_present=true; absence is a regression of
// the §3.2 wiring (see TestKernelPromptIsImported and the §12 gate).
//
// nonce_substituted reports whether any literal "{NONCE}" placeholder
// remains in the joined system prompts — should always be false when
// KernelPromptInjector is wrapping the registry. Presence indicates a
// dispatch path that bypassed the injector (regression of the SEC-002
// per-turn nonce wiring).
func summarizeSystemPrompts(prompts []string) map[string]any {
	count := len(prompts)
	if count == 0 {
		return map[string]any{"system_count": 0, "system_total_len": 0, "system_kernel_present": false, "system_nonce_unsubstituted": false}
	}
	joined := strings.Join(prompts, "\n\n")
	sum := sha256.Sum256([]byte(joined))
	return map[string]any{
		"system_count":               count,
		"system_total_len":           len(joined),
		"system_kernel_present":      strings.Contains(joined, kernelMarker),
		"system_nonce_unsubstituted": strings.Contains(joined, "{NONCE}"),
		"system_sha256":              hex.EncodeToString(sum[:])[:16],
	}
}

// mergeFields copies extra into base, overwriting on key collision.
// Used to fold summarizeSystemPrompts output into an invoke log entry
// without inflating call sites with verbose map literal joins.
func mergeFields(base, extra map[string]any) map[string]any {
	for k, v := range extra {
		base[k] = v
	}
	return base
}
