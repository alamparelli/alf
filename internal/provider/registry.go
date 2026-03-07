package provider

// Registry holds available provider backends and dispatches by name.
type Registry struct {
	CLI        *CLIProvider
	OpenRouter *APIProvider // nil if no API key configured
}

// ForBackend returns the provider for the given backend name.
// Returns CLI for unknown backends or when the requested backend is unavailable.
func (r *Registry) ForBackend(backend string) Provider {
	switch backend {
	case "openrouter":
		if r.OpenRouter != nil {
			return r.OpenRouter
		}
		return r.CLI
	default:
		return r.CLI
	}
}

// HasOpenRouter returns true if the OpenRouter backend is configured.
func (r *Registry) HasOpenRouter() bool {
	return r.OpenRouter != nil
}
