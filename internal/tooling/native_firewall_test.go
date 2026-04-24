package tooling

import "testing"

// 0.8.0-demo: native_firewall.go was the read-only LLM tool exposing global
// firewall activity (recent entries, host stats, search across all
// capabilities). Under the Tier 3.1 ocap model, each capability should
// only see its own network activity via its forged http.Handle, not a
// global view that leaks information across capabilities. The tool was
// razed in #406.
//
// Re-evaluate under #391 and the Tier 3.1 http.Handle design: if a
// per-handle "what did this capability do on the network" introspection
// surface becomes useful to the LLM, rebuild it scoped to the handle,
// not to the global firewall log.

func TestFirewallNativeTool_RazedIn406(t *testing.T) {
	t.Skip("0.8.0-demo: razed in #406; per-handle network introspection is part of #391 Tier 3.1 http.Handle design")
}
