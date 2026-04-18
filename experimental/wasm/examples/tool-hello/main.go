// tool-hello is the spike's hello-world tool.
//
// It demonstrates the three things a tool can do in a WASM-sandboxed ALF:
//   1. emit a log line to the host
//   2. write a value to its scoped storage
//   3. read it back and verify the round-trip
//
// It has NO access to the host filesystem, network, subprocesses, or vault —
// not because it's polite, but because its manifest didn't declare those
// capabilities, so the corresponding host imports don't exist in the linked
// module. Attempting to use them would fail at compile time (no function)
// or at instantiation (unresolved import).
//
// Build: GOOS=wasip1 GOARCH=wasm go build -o tool-hello.wasm .
// Run:   alf-wasm-host run --manifest manifest.toml
package main

import (
	"fmt"

	"github.com/alamparelli/alf/experimental/wasm/sdk/go/alf"
)

func main() {
	alf.LogInfo("tool-hello starting")

	// Count how many times we've been invoked.
	var count int
	if prev, ok := alf.StorageGet("invocations"); ok {
		count = bytesToInt(prev)
	}
	count++
	if err := alf.StoragePut("invocations", intToBytes(count)); err != nil {
		alf.LogError("StoragePut failed: " + err.Error())
		fmt.Println("ERROR:", err)
		return
	}

	// Round-trip verification.
	readBack, ok := alf.StorageGet("invocations")
	if !ok || bytesToInt(readBack) != count {
		alf.LogError("storage round-trip failed")
		fmt.Println("ERROR: round-trip failed")
		return
	}

	fmt.Printf("hello from the WASM sandbox!\n")
	fmt.Printf("invocation count (persisted in host-scoped storage): %d\n", count)
	fmt.Printf("manifest declared: log=true, storage=true — nothing else.\n")
	fmt.Printf("vault, http, filesystem, subprocess: all structurally unreachable.\n")

	alf.LogInfo("tool-hello done")
}

// intToBytes / bytesToInt — a minimal varint-less encoder for the spike.
func intToBytes(n int) []byte {
	return []byte(fmt.Sprintf("%d", n))
}

func bytesToInt(b []byte) int {
	var n int
	_, _ = fmt.Sscanf(string(b), "%d", &n)
	return n
}
