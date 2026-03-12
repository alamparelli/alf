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
}

// NewRegistry creates a Registry with the given CLI provider.
func NewRegistry(cli *CLIProvider) *Registry {
	return &Registry{
		cli:      cli,
		backends: make(map[string]*APIProvider),
	}
}

// Register adds or replaces an API backend.
func (r *Registry) Register(name string, p *APIProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[name] = p
	log.Printf("registry: backend %q registered (base_url=%s)", name, p.baseURL)
}

// Unregister removes an API backend.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.backends, name)
}

// ForBackend returns the provider for the given backend name.
// Returns CLI for "", "cli", or when the requested backend is unavailable.
func (r *Registry) ForBackend(backend string) Provider {
	if backend == "" || backend == "cli" {
		return r.cli
	}
	r.mu.RLock()
	p, ok := r.backends[backend]
	r.mu.RUnlock()
	if ok {
		return p
	}
	log.Printf("WARNING: backend %q requested but not registered, falling back to CLI", backend)
	return r.cli
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
	_, ok := r.backends[name]
	return ok
}

// BackendNames returns all registered API backend names.
func (r *Registry) BackendNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.backends))
	for n := range r.backends {
		names = append(names, n)
	}
	return names
}

// HasOpenRouter returns true if the OpenRouter backend is configured.
// Deprecated: use HasBackend("openrouter").
func (r *Registry) HasOpenRouter() bool {
	return r.HasBackend("openrouter")
}
