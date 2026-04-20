//go:build cgo

package memory

import (
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

func init() {
	// Register the sqlite-vec extension on every sqlite3 connection
	// opened after this point. Safe to call from multiple packages —
	// idempotent in sqlite_vec/cgo. Only compiled when CGO_ENABLED=1;
	// tool binaries that build with CGO_ENABLED=0 (cmd/memory-tools,
	// cmd/system-tools, …) transitively import this package but never
	// open a SQLiteStore with an embedder, so the missing init is
	// inert for them.
	sqlite_vec.Auto()
}
