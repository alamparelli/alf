package main

import "log"

// daemonWASMNotifier forwards WASM guest log lines to the daemon's
// standard log sink. Replace with a structured-logging or eventlog
// adapter when consolidating daemon observability.
type daemonWASMNotifier struct{}

func (daemonWASMNotifier) GuestLog(cap, level, msg string) {
	log.Printf("[wasm:%s] %s: %s", cap, level, msg)
}
