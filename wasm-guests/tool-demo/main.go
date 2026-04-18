// wasm-demo is the reference WASM tool bundled with ALF. It illustrates
// the pattern a "migrated native" tool follows: Go source compiled to
// wasip1, executed inside the daemon's wazero runtime, permitted to call
// host functions only as declared in manifest.toml.
//
// This tool is embedded into the alf-daemon binary at build time (see
// wasm-guests/embed.go). No file placement on the deployed container is
// required to use it — it is always present.
//
// Contract with the LLM caller:
//
//   - Input (stdin, JSON): {"input": "<arbitrary text>"}
//   - Output (stdout, JSON): {"echo": "<input>", "runs": <int>}
//
// Build:  GOOS=wasip1 GOARCH=wasm go build -o tool-demo.wasm .
//
//go:build wasip1

package main

import (
	"encoding/json"
	"io"
	"os"
	"strconv"

	"github.com/alamparelli/alf/sdk/wasm/alf"
)

type input struct {
	Input string `json:"input"`
}

type output struct {
	Echo string `json:"echo"`
	Runs int    `json:"runs"`
}

func main() {
	alf.LogInfo("wasm-demo invoked")

	var in input
	raw, _ := io.ReadAll(os.Stdin)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &in)
	}

	// Persist a run counter in capability-scoped storage.
	runs := 0
	if prev, ok := alf.StorageGet("runs"); ok {
		runs, _ = strconv.Atoi(string(prev))
	}
	runs++
	_ = alf.StoragePut("runs", []byte(strconv.Itoa(runs)))

	out := output{Echo: in.Input, Runs: runs}
	b, _ := json.Marshal(out)
	os.Stdout.Write(b)

	alf.LogInfo("wasm-demo done (run " + strconv.Itoa(runs) + ")")
}
