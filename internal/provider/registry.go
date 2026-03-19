package provider

import (
	"log"
	"sync"
)

// Registry holds available provider backends and dispatches by name.
type Registry struct {
	cli      *CLIProvider
	mu       sync.RWMutex
	backends map[string]*APIProvider
	generic  map[string]Provider // non-API providers (e.g. CodexProvider)
}

// NewRegistry creates a Registry with the given CLI provider.
func NewRegistry(cli *CLIProvider) *Registry {
	return &Registry{
		cli:      cli,
		backends: make(map[string]*APIProvider),
		generic:  make(map[string]Provider),
	}
}

// Register adds or replaces an API backend.
func (r *Registry) Register(name string, p *APIProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[name] = p
	log.Printf("registry: backend %q registered (base_url=%s)", name, p.baseURL)
}

// RegisterProvider adds or replaces a generic (non-API) provider backend.
func (r *Registry) RegisterProvider(name string, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generic[name] = p
	log.Printf("registry: provider %q registered", name)
}

// Unregister removes an API backend.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.backends, name)
	delete(r.generic, name)
}

// ForBackend returns the provider for the given backend name.
// Returns CLI for "", "cli", or when the requested backend is unavailable.
func (r *Registry) ForBackend(backend string) Provider {
	if backend == "" || backend == "cli" {
		return r.cli
	}
	r.mu.RLock()
	p, ok := r.backends[backend]
	if !ok {
		gp, gok := r.generic[backend]
		r.mu.RUnlock()
		if gok {
			return gp
		}
		log.Printf("WARNING: backend %q requested but not registered, falling back to CLI", backend)
		return r.cli
	}
	r.mu.RUnlock()
	return p
}

// GetAPIBackend returns the APIProvider for the given name, or nil.
func (r *Registry) GetAPIBackend(name string) *APIProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.backends[name]
}

// HasBackend returns true if the named backend is registered.
func (r *Registry) HasBackend(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.backends[name]; ok {
		return true
	}
	_, ok := r.generic[name]
	return ok
}

// BackendNames returns all registered backend names (API + generic).
func (r *Registry) BackendNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.backends)+len(r.generic))
	for n := range r.backends {
		names = append(names, n)
	}
	for n := range r.generic {
		names = append(names, n)
	}
	return names
}

// HasOpenRouter returns true if the OpenRouter backend is configured.
// Deprecated: use HasBackend("openrouter").
func (r *Registry) HasOpenRouter() bool {
	return r.HasBackend("openrouter")
}
