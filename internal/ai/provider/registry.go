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

	// kernelPrompt is the daemon-shipped, immutable system prompt
	// prepended to every LLM call per #400 / §3.2. Wired via
	// SetKernelPrompt at daemon init; empty string disables injection
	// (legacy tests + paths that explicitly opt out).
	kernelPrompt string
}

// NewRegistry creates a Registry with the given CLI provider.
func NewRegistry(cli *CLIProvider) *Registry {
	return &Registry{
		cli:      cli,
		backends: make(map[string]*APIProvider),
		generic:  make(map[string]Provider),
	}
}

// SetKernelPrompt installs the daemon-shipped kernel prompt that the
// Registry will prepend to every Invoke's SystemPrompts. Set once at
// daemon init from llm.KernelPrompt(); subsequent calls overwrite.
// An empty string disables injection — callers must explicitly opt out
// rather than silently skipping.
func (r *Registry) SetKernelPrompt(p string) {
	r.mu.Lock()
	r.kernelPrompt = p
	r.mu.Unlock()
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
//
// When a kernel prompt is set (SetKernelPrompt), the returned provider
// is wrapped in a KernelPromptInjector so every Invoke prepends the
// kernel prompt to params.SystemPrompts. The wrap is per-call (no
// hidden state on r.backends entries).
func (r *Registry) ForBackend(backend string) Provider {
	r.mu.RLock()
	kp := r.kernelPrompt
	r.mu.RUnlock()

	var raw Provider
	if backend == "" || backend == "cli" {
		raw = r.cli
	} else {
		r.mu.RLock()
		p, ok := r.backends[backend]
		if !ok {
			gp, gok := r.generic[backend]
			r.mu.RUnlock()
			if gok {
				raw = gp
			} else {
				log.Printf("WARNING: backend %q requested but not registered, falling back to CLI", backend)
				raw = r.cli
			}
		} else {
			r.mu.RUnlock()
			raw = p
		}
	}

	if kp == "" || raw == nil {
		return raw
	}
	return NewKernelPromptInjector(raw, kp)
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
