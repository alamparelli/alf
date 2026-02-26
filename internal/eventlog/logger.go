package eventlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger writes structured JSONL events to daily-rotated files.
type Logger struct {
	dir   string
	mu    sync.Mutex
	file  *os.File
	today string // "2006-01-02"
}

// New creates a Logger that writes to {dataDir}/logs/events/.
func New(dataDir string) *Logger {
	dir := filepath.Join(dataDir, "logs", "events")
	_ = os.MkdirAll(dir, 0o755)
	return &Logger{dir: dir}
}

// Log writes a single event line. Fields are merged with event name and timestamp.
func (l *Logger) Log(event string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	today := now.Format("2006-01-02")

	if l.today != today || l.file == nil {
		l.rotate(today)
	}
	if l.file == nil {
		return
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
	l.file.Write(data)
}

// Close releases the underlying file.
func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}

func (l *Logger) rotate(today string) {
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
	path := filepath.Join(l.dir, today+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	l.file = f
	l.today = today
}
