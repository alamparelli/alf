// app-hello is the spike's hello-world HTTP app.
//
// It serves three endpoints:
//
//	GET  /api/hello       — plain greeting
//	GET  /api/counter     — bumps and returns a storage-backed counter
//	GET  /api/btc         — proxies a coingecko call (via vault allowlist)
//
// The app has no filesystem, no arbitrary network, no subprocess. Its only
// levers on the outside world are the host functions explicitly granted in
// manifest.toml: log, storage, vault["coingecko","httpbin"].
//
// Build: GOOS=wasip1 GOARCH=wasm go build -o app-hello.wasm .
// Serve: alf-wasm-host serve --manifest manifest.toml --frontend frontend
package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/alamparelli/alf/experimental/wasm/sdk/go/alf"
)

func main() {
	req := alf.ReadRequest()
	alf.LogInfo("app-hello handling " + req.Method + " " + req.Path)

	switch req.Path {
	case "/api/hello":
		writeJSON(map[string]any{
			"message":  "hello from a sandboxed WASM app",
			"method":   req.Method,
			"runtime":  "go-wasip1",
			"sandbox":  "wazero + manifest-gated host imports",
		})

	case "/api/counter":
		var n int
		if prev, ok := alf.StorageGet("requests"); ok {
			n, _ = strconv.Atoi(string(prev))
		}
		n++
		_ = alf.StoragePut("requests", []byte(strconv.Itoa(n)))
		writeJSON(map[string]any{"requests_served": n})

	case "/api/btc":
		// This call is only permitted because the manifest lists
		// vault = ["coingecko"]. Replacing it with "kraken" below
		// would return rc=-2 (permission denied) at runtime.
		body, err := alf.VaultRequest("coingecko", "/simple/price?ids=bitcoin&vs_currencies=usd")
		if err != nil {
			writeJSON(map[string]any{"error": err.Error()})
			return
		}
		alf.LogInfo("btc fetched, " + strconv.Itoa(len(body)) + " bytes")
		// Pass the upstream response through as-is.
		alf.WriteResponse(body)

	case "/api/denied-demo":
		// Intentionally attempt a service we did NOT declare in the
		// manifest. This showcases that the Policy layer gates at call
		// time even for the same "vault_request" import, returning
		// rc=-2. (Structural absence is for totally un-declared
		// capability categories; per-service allowlists are enforced
		// by the host function body.)
		_, err := alf.VaultRequest("openai", "/v1/models")
		if err != nil {
			writeJSON(map[string]any{"error": err.Error(), "expected": "denied"})
			return
		}
		writeJSON(map[string]any{"warning": "unexpectedly allowed"})

	default:
		writeJSON(map[string]any{"error": "unknown path", "path": req.Path})
	}
}

func writeJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		alf.WriteResponseString(fmt.Sprintf(`{"error":%q}`, err.Error()))
		return
	}
	alf.WriteResponse(b)
}
