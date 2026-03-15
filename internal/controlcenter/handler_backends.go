package controlcenter

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alamparelli/alf/internal/provider"
)

// modelInfo describes a model available on a backend.
type modelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"` // display name (OpenRouter only)
}

// ModelCache caches available models per backend with background refresh.
type ModelCache struct {
	registry *provider.Registry
	mu       sync.RWMutex
	models   map[string][]modelInfo // backend name → models
	stopCh   chan struct{}
}

// NewModelCache creates a cache that pre-fetches models at startup
// and refreshes every interval. Call Stop() to stop the background goroutine.
func NewModelCache(registry *provider.Registry, interval time.Duration) *ModelCache {
	mc := &ModelCache{
		registry: registry,
		models:   map[string][]modelInfo{"cli": {{ID: "haiku"}, {ID: "sonnet"}, {ID: "opus"}}},
		stopCh:   make(chan struct{}),
	}
	// Initial fetch in background (non-blocking).
	go mc.refreshAll()
	// Periodic refresh.
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mc.refreshAll()
			case <-mc.stopCh:
				return
			}
		}
	}()
	return mc
}

// Stop stops the background refresh goroutine.
func (mc *ModelCache) Stop() {
	close(mc.stopCh)
}

// Get returns cached models for a backend. Returns nil if not cached.
func (mc *ModelCache) Get(backend string) []modelInfo {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.models[backend]
}

// All returns all cached models keyed by backend name.
func (mc *ModelCache) All() map[string][]modelInfo {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	result := make(map[string][]modelInfo, len(mc.models))
	for k, v := range mc.models {
		result[k] = v
	}
	return result
}

// RefreshBackend forces a refresh for a single backend.
func (mc *ModelCache) RefreshBackend(name string) {
	if mc.registry == nil {
		return
	}
	ap := mc.registry.GetAPIBackend(name)
	if ap == nil {
		return
	}
	models, err := fetchModels(ap)
	if err != nil {
		log.Printf("model-cache: refresh %q failed: %v", name, err)
		return
	}
	mc.mu.Lock()
	mc.models[name] = models
	mc.mu.Unlock()
	log.Printf("model-cache: %q refreshed (%d models)", name, len(models))
}

// refreshAll fetches models for all registered backends.
func (mc *ModelCache) refreshAll() {
	if mc.registry == nil {
		return
	}
	names := mc.registry.BackendNames()
	if len(names) == 0 {
		return
	}
	log.Printf("model-cache: refreshing %d backends...", len(names))
	for _, name := range names {
		ap := mc.registry.GetAPIBackend(name)
		if ap == nil {
			continue
		}
		models, err := fetchModels(ap)
		if err != nil {
			log.Printf("model-cache: %q failed: %v", name, err)
			continue
		}
		mc.mu.Lock()
		mc.models[name] = models
		mc.mu.Unlock()
		log.Printf("model-cache: %q loaded %d models", name, len(models))
	}
}

// BackendsModelsHandler serves the available models for a specific backend.
// GET /api/backends/{name}/models          - return cached models
// POST /api/backends/{name}/models/refresh - force refresh
type BackendsModelsHandler struct {
	Registry *provider.Registry
	Cache    *ModelCache
}

func (h *BackendsModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse path: /api/backends/{name}/models[/refresh]
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/backends/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || parts[0] == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "expected /api/backends/{name}/models"})
		return
	}
	name := parts[0]

	// POST .../refresh - force refresh a single backend
	if r.Method == http.MethodPost && len(parts) >= 3 && parts[2] == "refresh" {
		if h.Cache != nil {
			go h.Cache.RefreshBackend(name)
		}
		respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	if parts[1] != "models" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "expected /api/backends/{name}/models"})
		return
	}

	// CLI backend: static list.
	if name == "cli" {
		respondJSON(w, http.StatusOK, map[string]any{
			"backend": "cli",
			"models":  []modelInfo{{ID: "haiku"}, {ID: "sonnet"}, {ID: "opus"}},
		})
		return
	}

	// Return from cache if available.
	if h.Cache != nil {
		if cached := h.Cache.Get(name); cached != nil {
			respondJSON(w, http.StatusOK, map[string]any{
				"backend": name,
				"models":  cached,
			})
			return
		}
	}

	// Cache miss: fetch on demand.
	if h.Registry == nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "registry unavailable"})
		return
	}

	ap := h.Registry.GetAPIBackend(name)
	if ap == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("backend %q not found", name)})
		return
	}

	models, err := fetchModels(ap)
	if err != nil {
		log.Printf("backends: failed to fetch models for %q: %v", name, err)
		respondJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("failed to fetch models: %v", err)})
		return
	}

	// Populate cache for next time.
	if h.Cache != nil {
		h.Cache.mu.Lock()
		h.Cache.models[name] = models
		h.Cache.mu.Unlock()
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"backend": name,
		"models":  models,
	})
}

// fetchModels queries the backend's models endpoint.
// Tries multiple endpoint patterns to support Ollama, OpenAI, and OpenRouter.
func fetchModels(ap *provider.APIProvider) ([]modelInfo, error) {
	baseURL := ap.BaseURL()
	baseStripped := strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/v1")

	// Try each known models endpoint path.
	endpoints := []struct {
		label string
		url   string
		parse func([]byte) []modelInfo
	}{
		// Ollama native: strip /v1 from base and call /api/tags
		{
			label: "ollama /api/tags",
			url:   baseStripped + "/api/tags",
			parse: parseOllamaModels,
		},
		// OpenAI-compatible: base + /models (works for OpenAI, OpenRouter, and Ollama /v1/models)
		{
			label: "openai /models",
			url:   strings.TrimSuffix(baseURL, "/") + "/models",
			parse: parseOpenAIModels,
		},
	}

	client := &http.Client{Timeout: 15 * time.Second}

	var lastErr error
	for _, ep := range endpoints {
		req, err := http.NewRequest("GET", ep.url, nil)
		if err != nil {
			lastErr = err
			continue
		}

		// Add auth if backend requires it.
		if ap.Auth() != "none" && ap.APIKey() != "" {
			req.Header.Set("Authorization", "Bearer "+ap.APIKey())
		}
		for k, v := range ap.Headers() {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("model-fetch: %s %s: %v", ap.Name(), ep.label, err)
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024)) // 2MB max
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != 200 {
			log.Printf("model-fetch: %s %s returned %d", ap.Name(), ep.label, resp.StatusCode)
			lastErr = fmt.Errorf("%s returned %d", ep.url, resp.StatusCode)
			continue
		}

		models := ep.parse(body)
		if len(models) > 0 {
			sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
			log.Printf("model-fetch: %s %s: %d models", ap.Name(), ep.label, len(models))
			return models, nil
		}
		log.Printf("model-fetch: %s %s: 0 models (trying next)", ap.Name(), ep.label)
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return []modelInfo{}, nil
}

// parseOllamaModels parses {"models": [{"name": "..."}]}
func parseOllamaModels(data []byte) []modelInfo {
	var resp struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || len(resp.Models) == 0 {
		return nil
	}
	models := make([]modelInfo, 0, len(resp.Models))
	for _, m := range resp.Models {
		id := m.Name
		if id == "" {
			id = m.Model
		}
		if id != "" {
			models = append(models, modelInfo{ID: id})
		}
	}
	return models
}

// parseOpenAIModels parses {"data": [{"id": "...", "name": "..."}]}
func parseOpenAIModels(data []byte) []modelInfo {
	var resp struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name,omitempty"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || len(resp.Data) == 0 {
		return nil
	}
	models := make([]modelInfo, 0, len(resp.Data))
	for _, m := range resp.Data {
		if m.ID != "" {
			models = append(models, modelInfo{ID: m.ID, Name: m.Name})
		}
	}
	return models
}
