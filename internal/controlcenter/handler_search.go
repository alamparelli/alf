package controlcenter

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/marketplace"
)

// SearchHandler searches across apps, files, and docs.
// GET /api/search?q=<query>&types=apps,files,docs
type SearchHandler struct {
	AppStore      AppStore
	Marketplace   *marketplace.Manager
	DataDir       string
	ConfigDir     string
	SkillsDir     string
}

type searchResponse struct {
	Apps        []any `json:"apps"`
	Marketplace []any `json:"marketplace"`
	Files       []any `json:"files"`
	Docs        []any `json:"docs"`
}

type searchFileResult struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64 `json:"size"`
	IsDir     bool  `json:"is_dir"`
	ModTime   string `json:"mod_time"`
	Extension string `json:"extension"`
}

type searchAppResult struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Icon        string   `json:"icon,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	State       string   `json:"state,omitempty"`
}

type searchDocResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Category string `json:"category,omitempty"`
}

func (h *SearchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	typesParam := r.URL.Query().Get("types")

	// Parse types (default: all)
	types := make(map[string]bool)
	if typesParam == "" {
		types["apps"] = true
		types["files"] = true
		types["docs"] = true
	} else {
		for _, t := range strings.Split(typesParam, ",") {
			types[strings.TrimSpace(t)] = true
		}
	}

	resp := searchResponse{
		Apps:        []any{},
		Marketplace: []any{},
		Files:       []any{},
		Docs:        []any{},
	}

	// Don't search with empty query
	if query == "" {
		respondJSON(w, http.StatusOK, resp)
		return
	}

	q := strings.ToLower(query)

	// Search apps (local + enabled marketplace)
	if types["apps"] {
		resp.Apps = h.searchApps(q)
	}

	// Search marketplace (all states + remote catalog)
	if types["apps"] {
		resp.Marketplace = h.searchMarketplace(q)
	}

	// Search files
	if types["files"] {
		resp.Files = h.searchFiles(q)
	}

	// Search docs
	if types["docs"] {
		resp.Docs = h.searchDocs(q)
	}

	respondJSON(w, http.StatusOK, resp)
}

func (h *SearchHandler) searchApps(query string) []any {
	var results []any
	seen := make(map[string]bool)

	// Build set of non-enabled marketplace slugs to exclude from local results
	disabledSlugs := make(map[string]bool)
	if h.Marketplace != nil {
		for _, app := range h.Marketplace.List() {
			if app.State != marketplace.StateEnabled {
				disabledSlugs[app.Slug] = true
			}
		}
	}

	// 1. Local apps — skip marketplace-managed disabled/installed ones
	if h.AppStore != nil {
		apps, err := h.AppStore.List()
		if err == nil {
			for _, app := range apps {
				if disabledSlugs[app.Name] {
					continue
				}
				if h.appMatches(app, query) {
					seen[app.Name] = true
					results = append(results, searchAppResult{
						Name:        app.Name,
						DisplayName: app.DisplayName,
						Icon:        app.Icon,
						State:       "local",
					})
				}
			}
		}
	}

	// 2. Marketplace enabled apps not already in local
	if h.Marketplace != nil {
		for _, app := range h.Marketplace.List() {
			if seen[app.Slug] || app.State != marketplace.StateEnabled {
				continue
			}
			if h.marketplaceAppMatches(app, query) {
				seen[app.Slug] = true
				results = append(results, searchAppResult{
					Name:        app.Slug,
					DisplayName: app.Name,
					Icon:        app.Icon,
					Category:    app.Category,
					State:       string(app.State),
				})
			}
		}
	}

	return results
}

// searchMarketplace returns all marketplace apps (any state) + remote catalog,
// excluding apps already shown in the Apps section (local + enabled).
func (h *SearchHandler) searchMarketplace(query string) []any {
	if h.Marketplace == nil {
		return nil
	}

	var results []any
	seen := make(map[string]bool)

	// Collect slugs already shown in the Apps section (to avoid duplication).
	// Only enabled marketplace apps and pure-local apps (not managed by marketplace).
	activeApps := make(map[string]bool)
	mpManaged := make(map[string]bool)
	for _, app := range h.Marketplace.List() {
		mpManaged[app.Slug] = true
		if app.State == marketplace.StateEnabled {
			activeApps[app.Slug] = true
		}
	}
	if h.AppStore != nil {
		if apps, err := h.AppStore.List(); err == nil {
			for _, a := range apps {
				if !mpManaged[a.Name] {
					activeApps[a.Name] = true // pure local, not marketplace
				}
			}
		}
	}

	// Marketplace managed apps (non-enabled: disabled, installed)
	for _, app := range h.Marketplace.List() {
		if activeApps[app.Slug] || seen[app.Slug] {
			continue
		}
		if h.marketplaceAppMatches(app, query) {
			seen[app.Slug] = true
			results = append(results, searchAppResult{
				Name:        app.Slug,
				DisplayName: app.Name,
				Icon:        app.Icon,
				Category:    app.Category,
				State:       string(app.State),
			})
		}
	}

	// Remote catalog (not yet installed)
	if catalog, err := h.Marketplace.FetchCatalog(); err == nil {
		for _, app := range catalog {
			if activeApps[app.Slug] || seen[app.Slug] {
				continue
			}
			if h.remoteCatalogAppMatches(app, query) {
				seen[app.Slug] = true
				results = append(results, searchAppResult{
					Name:        app.Slug,
					DisplayName: app.Name,
					Icon:        app.Icon,
					Category:    app.Category,
					State:       "available",
				})
			}
		}
	}

	return results
}

func (h *SearchHandler) appMatches(app AppMeta, query string) bool {
	return strings.Contains(strings.ToLower(app.DisplayName), query) ||
		strings.Contains(strings.ToLower(app.Name), query) ||
		strings.Contains(strings.ToLower(app.Description), query)
}

func (h *SearchHandler) marketplaceAppMatches(app marketplace.AppInfo, query string) bool {
	return strings.Contains(strings.ToLower(app.Name), query) ||
		strings.Contains(strings.ToLower(app.Description), query) ||
		strings.Contains(strings.ToLower(app.Category), query)
}

func (h *SearchHandler) remoteCatalogAppMatches(app marketplace.RemoteApp, query string) bool {
	return strings.Contains(strings.ToLower(app.Name), query) ||
		strings.Contains(strings.ToLower(app.Description), query) ||
		strings.Contains(strings.ToLower(app.Category), query)
}

const maxFileResults = 50

func (h *SearchHandler) searchFiles(query string) []any {
	var results []any

	// Protected, system, and internal directories to skip
	skipDirs := map[string]bool{
		"apps":          true, // app internals not useful in file search
		"tools":         true, // tool binaries and go module cache
		"config.d":      true,
		"skills.d":      true,
		"tools.d":       true,
		"agents":        true,
		"logs":          true,
		"sessions":      true,
		"context":       true,
		"docs":          true,
		"config":        true,
		"skills":        true,
		".git":          true,
		".claude":       true,
		".cache":        true,
		".local":        true,
		"node_modules":  true,
		"go-path":       true,
	}

	h.walkDir(h.DataDir, "", query, skipDirs, &results)
	return results
}

func (h *SearchHandler) walkDir(basePath, relPath string, query string, skipDirs map[string]bool, results *[]any) {
	if len(*results) >= maxFileResults {
		return
	}

	entries, err := os.ReadDir(filepath.Join(basePath, relPath))
	if err != nil {
		return
	}

	for _, entry := range entries {
		if len(*results) >= maxFileResults {
			return
		}

		name := entry.Name()

		// Skip hidden files/dirs
		if strings.HasPrefix(name, ".") {
			continue
		}

		// Skip protected directories
		if entry.IsDir() && skipDirs[name] {
			continue
		}

		fullRelPath := filepath.Join(relPath, name)
		if relPath == "" {
			fullRelPath = name
		}

		if entry.IsDir() {
			h.walkDir(basePath, fullRelPath, query, skipDirs, results)
			continue
		}

		if h.fileMatches(name, fullRelPath, query) {
			info, err := entry.Info()
			if err != nil {
				continue
			}

			result := searchFileResult{
				Path:      fullRelPath,
				Name:      name,
				Size:      info.Size(),
				IsDir:     false,
				ModTime:   info.ModTime().UTC().Format(time.RFC3339),
				Extension: strings.TrimPrefix(filepath.Ext(name), "."),
			}
			*results = append(*results, result)
		}
	}
}

func (h *SearchHandler) fileMatches(name, fullPath, query string) bool {
	// Match on basename (highest priority) or full path
	return strings.Contains(strings.ToLower(name), query) ||
		strings.Contains(strings.ToLower(fullPath), query)
}

func (h *SearchHandler) searchDocs(query string) []any {
	var results []any

	entries, err := docsFS.ReadDir("docs")
	if err != nil {
		return results
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}

		id := strings.TrimSuffix(e.Name(), ".md")
		data, err := docsFS.ReadFile("docs/" + e.Name())
		if err != nil {
			continue
		}

		info := parseDocInfo(string(data))

		// Search in title, summary, tags, and body
		if h.docMatches(info, query) {
			result := searchDocResult{
				ID:       id,
				Title:    info.title,
				Summary:  info.summary,
				Category: info.category,
			}
			results = append(results, result)
		}
	}

	return results
}

func (h *SearchHandler) docMatches(info docInfo, query string) bool {
	q := strings.ToLower(query)
	if strings.Contains(strings.ToLower(info.title), q) ||
		strings.Contains(strings.ToLower(info.summary), q) ||
		strings.Contains(strings.ToLower(info.body), q) {
		return true
	}
	for _, tag := range info.tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}
