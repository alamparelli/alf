package controlcenter

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/provider"
)

// BackendsModelsHandler serves the available models for a specific backend.
// GET /api/backends/{name}/models
type BackendsModelsHandler struct {
	Registry *provider.Registry
}

// modelInfo describes a model available on a backend.
type modelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"` // display name (OpenRouter only)
}

func (h *BackendsModelsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse backend name from path: /api/backends/{name}/models
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/backends/"), "/")
	if len(parts) < 2 || parts[1] != "models" || parts[0] == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "expected /api/backends/{name}/models"})
		return
	}
	name := parts[0]

	// CLI backend returns static model list.
	if name == "cli" {
		respondJSON(w, http.StatusOK, map[string]any{
			"backend": "cli",
			"models":  []modelInfo{{ID: "haiku"}, {ID: "sonnet"}, {ID: "opus"}},
		})
		return
	}

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

	respondJSON(w, http.StatusOK, map[string]any{
		"backend": name,
		"models":  models,
	})
}

// fetchModels queries the backend's models endpoint.
// Supports three response formats:
//   - Ollama:     GET /api/tags      → {"models": [{"name": "..."}]}
//   - OpenAI:     GET /v1/models     → {"data": [{"id": "..."}]}
//   - OpenRouter: GET /api/v1/models → {"data": [{"id": "...", "name": "..."}]}
func fetchModels(ap *provider.APIProvider) ([]modelInfo, error) {
	baseURL := ap.BaseURL()

	// Try each known models endpoint path.
	endpoints := []struct {
		url   string
		parse func([]byte) []modelInfo
	}{
		// Ollama: base is http://host:11434/v1, strip /v1 and use /api/tags
		{
			url:   strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/v1") + "/api/tags",
			parse: parseOllamaModels,
		},
		// OpenAI-compatible: base/models (base usually ends with /v1)
		{
			url:   strings.TrimSuffix(baseURL, "/") + "/models",
			parse: parseOpenAIModels,
		},
	}

	client := &http.Client{Timeout: 10 * time.Second}

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
			lastErr = fmt.Errorf("%s returned %d", ep.url, resp.StatusCode)
			continue
		}

		models := ep.parse(body)
		if len(models) > 0 {
			return models, nil
		}
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
