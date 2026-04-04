package controlcenter

import (
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

// watchAppsDir polls the apps directory and emits EventApps when entries change.
// Call the returned stop function to terminate the goroutine.
func watchAppsDir(dir string, broker *EventBroker, interval time.Duration) (stop func()) {
	done := make(chan struct{})

	snapshot := dirSnapshot(dir)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				current := dirSnapshot(dir)
				if current != snapshot {
					snapshot = current
					log.Printf("[CC] apps directory changed, emitting refresh")
					broker.Emit(EventApps)
				}
			}
		}
	}()

	return func() { close(done) }
}

// dirSnapshot returns a sorted, comma-joined list of directory entry names.
func dirSnapshot(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
