// Package firewall is a thin re-export shim. The Network facet of Sandbox
// now lives at internal/sandbox/network (moved during #339 Step 3). Existing
// consumers keep importing internal/firewall until Runtime (#340) rewires
// them.
package firewall

import (
	"github.com/alamparelli/alf/internal/sandbox/network"
)

// Mode is an alias for network.Mode.
type Mode = network.Mode

// ModeEnforce re-exports network.ModeEnforce.
const ModeEnforce = network.ModeEnforce

// ModeLogOnly re-exports network.ModeLogOnly.
const ModeLogOnly = network.ModeLogOnly

// Rule is an alias for network.Rule.
type Rule = network.Rule

// Config is an alias for network.Config.
type Config = network.Config

// DefaultConfig re-exports network.DefaultConfig.
func DefaultConfig() *Config {
	return network.DefaultConfig()
}

// RequestEntry is an alias for network.RequestEntry.
type RequestEntry = network.RequestEntry

// RingBuffer is an alias for network.RingBuffer.
type RingBuffer = network.RingBuffer

// Proxy is an alias for network.Proxy.
type Proxy = network.Proxy

// NewProxy re-exports network.NewProxy.
func NewProxy(cfg *Config) *Proxy {
	return network.NewProxy(cfg)
}

// NetTracker is an alias for network.NetTracker.
type NetTracker = network.NetTracker

// NewNetTracker re-exports network.NewNetTracker.
func NewNetTracker(proxy *Proxy, sockPath string) *NetTracker {
	return network.NewNetTracker(proxy, sockPath)
}

// HostStat is an alias for network.HostStat.
type HostStat = network.HostStat

// Store is an alias for network.Store.
type Store = network.Store

// NewStore re-exports network.NewStore.
func NewStore(configDir string) *Store {
	return network.NewStore(configDir)
}
