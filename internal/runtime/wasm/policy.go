package wasm

// Policy is derived mechanically from a Manifest. It tells the runtime
// which host functions to link into the guest's imports.
//
// The invariant: a capability cannot call a host function that the
// Policy does not permit, because the import simply is not provided to
// the module at instantiation time. A guest that tries will get a
// wazero link error — it cannot execute, period.
//
// This is the single authoritative mapping. Changing permission semantics
// means changing PolicyFromManifest and nothing else.
type Policy struct {
	LogEnabled     bool
	StorageEnabled bool
	MemoryEnabled  bool
	EventsEnabled  bool

	// VaultServices is the allowlist of service names for vault.request.
	// nil or empty = no vault access at all.
	VaultServices map[string]bool

	// HTTPHosts is the allowlist of hostnames for http.fetch.
	// nil or empty = no raw HTTP access.
	HTTPHosts map[string]bool
}

// PolicyFromManifest maps declared Permissions to a runtime Policy.
func PolicyFromManifest(m *Manifest) Policy {
	p := Policy{
		LogEnabled:     m.Permissions.Log,
		StorageEnabled: m.Permissions.Storage,
		MemoryEnabled:  m.Permissions.Memory,
		EventsEnabled:  m.Permissions.Events,
	}
	if len(m.Permissions.Vault) > 0 {
		p.VaultServices = make(map[string]bool, len(m.Permissions.Vault))
		for _, s := range m.Permissions.Vault {
			p.VaultServices[s] = true
		}
	}
	if len(m.Permissions.HTTP) > 0 {
		p.HTTPHosts = make(map[string]bool, len(m.Permissions.HTTP))
		for _, h := range m.Permissions.HTTP {
			p.HTTPHosts[h] = true
		}
	}
	return p
}

// VaultAllowed returns true iff the given service may be invoked.
func (p Policy) VaultAllowed(service string) bool {
	return p.VaultServices != nil && p.VaultServices[service]
}

// HTTPAllowed returns true iff the given hostname may be fetched.
func (p Policy) HTTPAllowed(host string) bool {
	return p.HTTPHosts != nil && p.HTTPHosts[host]
}
