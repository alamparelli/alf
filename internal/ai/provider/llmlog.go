package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
