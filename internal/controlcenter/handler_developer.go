package controlcenter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/vault"
	vaultclient "github.com/alessandrolamparelli/vault-proxy/pkg/client"
)

// DeveloperHandler provides backend endpoints for the Developer Tools view.
// It replaces the bash-based approach with proper server-side operations.
type DeveloperHandler struct {
	DataDir      string
	VaultManager *vault.Manager
}

func (h *DeveloperHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/developer/")
	rest = strings.TrimRight(rest, "/")

	switch {
	case rest == "status" && r.Method == http.MethodGet:
		h.handleStatus(w, r)
	case rest == "apps" && r.Method == http.MethodGet:
		h.handleApps(w, r)
	case rest == "tools" && r.Method == http.MethodGet:
		h.handleTools(w, r)
	case rest == "skills" && r.Method == http.MethodGet:
		h.handleSkills(w, r)
	case rest == "catalog" && r.Method == http.MethodGet:
		h.handleCatalog(w, r)
	case rest == "app-meta" && r.Method == http.MethodGet:
		h.handleAppMeta(w, r)
	case rest == "validate" && r.Method == http.MethodPost:
		h.handleValidate(w, r)
	case rest == "publish" && r.Method == http.MethodPost:
		h.handlePublish(w, r)
	case rest == "unpublish" && r.Method == http.MethodPost:
		h.handleUnpublish(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleStatus checks marketplace connection via vault proxy.
func (h *DeveloperHandler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if h.VaultManager == nil {
		respondJSON(w, http.StatusOK, map[string]any{"connected": false, "error": "vault not configured"})
		return
	}

	client := h.vaultClient()
	if client == nil {
		respondJSON(w, http.StatusOK, map[string]any{"connected": false, "error": "vault locked"})
		return
	}

	type proxyResult struct {
		resp *http.Response
		err  error
	}
	ch := make(chan proxyResult, 1)
	go func() {
		resp, err := client.Proxy("marketplace", "GET", "/api/health", nil)
		ch <- proxyResult{resp, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			respondJSON(w, http.StatusOK, map[string]any{"connected": false, "error": "marketplace not reachable"})
			return
		}
		defer res.resp.Body.Close()
		var health map[string]any
		if err := json.NewDecoder(res.resp.Body).Decode(&health); err != nil || health["status"] != "connected" {
			respondJSON(w, http.StatusOK, map[string]any{"connected": false, "error": "marketplace not reachable"})
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"connected": true,
			"developer": health["developer"],
		})
	case <-time.After(5 * time.Second):
		respondJSON(w, http.StatusOK, map[string]any{"connected": false, "error": "marketplace not reachable (timeout)"})
	}
}

// handleApps lists local app directories (filtered for development).
// Only directories containing at least one recognizable app marker file are
// returned — this excludes orphaned/empty leftover dirs from incomplete
// uninstalls, which would otherwise pollute the Source App dropdown (#277).
func (h *DeveloperHandler) handleApps(w http.ResponseWriter, r *http.Request) {
	appsDir := filepath.Join(h.DataDir, "apps")
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"apps": []string{}})
		return
	}

	markers := []string{"app.json", "manifest.json", "index.html"}

	var apps []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		appPath := filepath.Join(appsDir, e.Name())
		hasMarker := false
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(appPath, marker)); err == nil {
				hasMarker = true
				break
			}
		}
		if !hasMarker {
			continue
		}
		apps = append(apps, e.Name())
	}
	respondJSON(w, http.StatusOK, map[string]any{"apps": apps})
}

// handleTools lists tool schemas from data/tools/.
func (h *DeveloperHandler) handleTools(w http.ResponseWriter, r *http.Request) {
	toolsDir := filepath.Join(h.DataDir, "tools")
	entries, err := os.ReadDir(toolsDir)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"tools": []any{}})
		return
	}

	var tools []map[string]any
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(toolsDir, e.Name()))
		if err != nil {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(data, &schema); err != nil || schema["name"] == nil {
			continue
		}
		tools = append(tools, schema)
	}
	respondJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

// handleSkills lists user skill directories.
func (h *DeveloperHandler) handleSkills(w http.ResponseWriter, r *http.Request) {
	skillsDir := filepath.Join(h.DataDir, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"skills": []string{}})
		return
	}

	var skills []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Check SKILL.md exists
		if _, err := os.Stat(filepath.Join(skillsDir, e.Name(), "SKILL.md")); err == nil {
			skills = append(skills, e.Name())
		}
	}
	respondJSON(w, http.StatusOK, map[string]any{"skills": skills})
}

// handleAppMeta reads app.json and manifest.json for a given slug.
func (h *DeveloperHandler) handleAppMeta(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("slug")
	if slug == "" || !validName.MatchString(slug) {
		respondError(w, http.StatusBadRequest, "invalid slug")
		return
	}

	appDir := filepath.Join(h.DataDir, "apps", slug)
	result := map[string]any{"slug": slug}

	// Read app.json
	if data, err := os.ReadFile(filepath.Join(appDir, "app.json")); err == nil {
		var meta map[string]any
		if json.Unmarshal(data, &meta) == nil {
			result["app"] = meta
		}
	}

	// Read manifest.json
	if data, err := os.ReadFile(filepath.Join(appDir, "manifest.json")); err == nil {
		var manifest map[string]any
		if json.Unmarshal(data, &manifest) == nil {
			result["manifest"] = manifest
		}
	}

	// Check if binary exists
	binPath := filepath.Join(appDir, "bin", slug)
	if info, err := os.Stat(binPath); err == nil && !info.IsDir() {
		result["has_binary"] = true
	}

	// Check if index.html exists
	if _, err := os.Stat(filepath.Join(appDir, "index.html")); err == nil {
		result["has_index"] = true
	}

	respondJSON(w, http.StatusOK, result)
}

// handleCatalog fetches the developer's published apps from the marketplace.
func (h *DeveloperHandler) handleCatalog(w http.ResponseWriter, r *http.Request) {
	client := h.vaultClient()
	if client == nil {
		respondJSON(w, http.StatusOK, map[string]any{"apps": []any{}})
		return
	}

	resp, err := client.Proxy("marketplace", "GET", "/api/catalog", nil)
	if err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"apps": []any{}})
		return
	}
	defer resp.Body.Close()

	var apps []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&apps); err != nil {
		respondJSON(w, http.StatusOK, map[string]any{"apps": []any{}})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"apps": apps})
}

type validateRequest struct {
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Category string   `json:"category"`
	Tools    []string `json:"tools"`
}

var validCategories = map[string]bool{
	"productivity": true, "dev-tools": true, "games": true, "media": true,
	"utilities": true, "finance": true, "social": true, "communication": true,
}

// handleValidate checks an app for common issues before publishing.
func (h *DeveloperHandler) handleValidate(w http.ResponseWriter, r *http.Request) {
	var req validateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}

	var errors, warnings []string

	if req.Slug == "" {
		errors = append(errors, "Slug is required")
	} else if matched, _ := filepath.Match("[a-z0-9]*", req.Slug); !matched {
		errors = append(errors, "Slug: lowercase alphanumeric + hyphens")
	}
	if req.Name == "" {
		errors = append(errors, "Name is required")
	}
	if req.Version != "" {
		parts := strings.Split(req.Version, ".")
		if len(parts) != 3 {
			errors = append(errors, "Version must be semver (e.g. 1.0.0)")
		}
	}
	if req.Category != "" && !validCategories[req.Category] {
		warnings = append(warnings, "Unknown category: "+req.Category)
	}
	if len(req.Tools) == 0 {
		warnings = append(warnings, "No tools selected — app will have no CLI backend")
	}

	// Check app directory
	if req.Slug != "" && validName.MatchString(req.Slug) {
		appDir := filepath.Join(h.DataDir, "apps", req.Slug)
		if _, err := os.Stat(filepath.Join(appDir, "index.html")); err != nil {
			warnings = append(warnings, "No index.html — app will have no web UI")
		}
		if len(req.Tools) > 0 {
			binPath := filepath.Join(appDir, "bin", req.Slug)
			if _, err := os.Stat(binPath); err != nil {
				errors = append(errors, "Tool binary not found at bin/"+req.Slug)
			}
		}
	}

	respondJSON(w, http.StatusOK, map[string]any{"errors": errors, "warnings": warnings})
}

type publishRequest struct {
	Slug      string   `json:"slug"`
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Desc      string   `json:"description"`
	Category  string   `json:"category"`
	Icon      string   `json:"icon"`
	Changelog string   `json:"changelog"`
	Tools     []string `json:"tools"`
	Skill     string   `json:"skill"`
}

// handlePublish bundles and publishes an app to the marketplace.
func (h *DeveloperHandler) handlePublish(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[developer] publish: decode body: %v", err)
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Slug == "" || !validName.MatchString(req.Slug) {
		log.Printf("[developer] publish: invalid slug %q", req.Slug)
		respondError(w, http.StatusBadRequest, "invalid slug")
		return
	}

	client := h.vaultClient()
	if client == nil {
		log.Printf("[developer] publish: vault not available (manager=%v)", h.VaultManager != nil)
		respondError(w, http.StatusBadRequest, "vault not available")
		return
	}

	appDir := filepath.Join(h.DataDir, "apps", req.Slug)

	// Load existing manifest (if any) to preserve non-UI fields like `permissions`
	manifest := map[string]any{}
	if data, err := os.ReadFile(filepath.Join(appDir, "manifest.json")); err == nil {
		_ = json.Unmarshal(data, &manifest)
	}

	// Overlay UI-managed fields onto existing manifest
	manifest["name"] = req.Name
	manifest["slug"] = req.Slug
	manifest["version"] = req.Version
	manifest["description"] = req.Desc
	manifest["category"] = req.Category
	manifest["icon"] = req.Icon
	manifest["tools"] = []any{}

	// Load tool schemas
	for _, toolName := range req.Tools {
		if !validName.MatchString(toolName) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(h.DataDir, "tools", toolName+".json"))
		if err != nil {
			continue
		}
		var schema map[string]any
		if json.Unmarshal(data, &schema) == nil {
			manifest["tools"] = append(manifest["tools"].([]any), map[string]any{
				"name": schema["name"], "description": schema["description"], "parameters": schema["parameters"],
			})
		}
	}

	// Write manifest.json to app dir
	manifestJSON, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(appDir, "manifest.json"), manifestJSON, 0o644); err != nil {
		log.Printf("[developer] publish: write manifest to %s: %v", filepath.Join(appDir, "manifest.json"), err)
		respondError(w, http.StatusInternalServerError, "write manifest: " + err.Error())
		return
	}

	// Create tarball
	tarball := fmt.Sprintf("/tmp/alf-publish-%s.tar.gz", req.Slug)
	defer os.Remove(tarball)

	tarCmd := exec.Command("tar", "-czf", tarball, "-C", appDir,
		"--exclude=./data", "--exclude=.git", "--exclude=.env*",
		"--exclude=*.pem", "--exclude=*.key", ".")
	if out, err := tarCmd.CombinedOutput(); err != nil {
		log.Printf("[developer] publish: tar %s failed: %v — %s", appDir, err, string(out))
		respondError(w, http.StatusInternalServerError, "tar: " + string(out))
		return
	}

	// Build multipart form for the marketplace publish API.
	// Uses Go net/http instead of shelling out to curl — avoids leaking
	// the vault token in /proc/PID/cmdline (SEC-002).
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// manifest field
	if err := mw.WriteField("manifest", string(manifestJSON)); err != nil {
		log.Printf("[developer] publish: multipart write manifest field: %v", err)
		respondError(w, http.StatusInternalServerError, "multipart: " + err.Error())
		return
	}

	// app_bundle field (tarball)
	tarFile, err := os.Open(tarball)
	if err != nil {
		log.Printf("[developer] publish: open tarball %s: %v", tarball, err)
		respondError(w, http.StatusInternalServerError, "open tarball: " + err.Error())
		return
	}
	defer tarFile.Close()

	bundlePart, err := mw.CreateFormFile("app_bundle", filepath.Base(tarball))
	if err != nil {
		log.Printf("[developer] publish: create form file: %v", err)
		respondError(w, http.StatusInternalServerError, "multipart: " + err.Error())
		return
	}
	if _, err := io.Copy(bundlePart, tarFile); err != nil {
		log.Printf("[developer] publish: copy tarball to multipart: %v", err)
		respondError(w, http.StatusInternalServerError, "copy tarball: " + err.Error())
		return
	}

	// Optional fields
	if req.Changelog != "" {
		mw.WriteField("whats_new", req.Changelog)
	}
	if req.Skill != "" && validName.MatchString(req.Skill) {
		skillPath := filepath.Join(h.DataDir, "skills", req.Skill, "SKILL.md")
		if skillData, err := os.ReadFile(skillPath); err == nil {
			skillPart, _ := mw.CreateFormFile("skill", "SKILL.md")
			skillPart.Write(skillData)
		}
	}
	mw.Close()

	vc := vaultclient.NewWithSocket(h.VaultManager.SocketPath(), h.VaultManager.ProxyToken())
	publishPath := "/proxy/marketplace/api/apps/" + req.Slug + "/publish"
	httpReq, err := http.NewRequestWithContext(r.Context(), "POST", vc.Addr+publishPath, &buf)
	if err != nil {
		log.Printf("[developer] publish: build HTTP request to %s: %v", vc.Addr+publishPath, err)
		respondError(w, http.StatusInternalServerError, "build request: " + err.Error())
		return
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := vc.DoRequest(httpReq)
	if err != nil {
		log.Printf("[developer] publish: DoRequest failed: %v", err)
		respondError(w, http.StatusBadGateway, "publish failed: " + err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		respondJSON(w, http.StatusOK, map[string]any{"success": true, "message": string(body)})
	} else {
		log.Printf("[developer] publish: marketplace returned HTTP %d: %s", resp.StatusCode, string(body))
		respondJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("HTTP %d", resp.StatusCode), "detail": string(body)})
	}
}

// handleUnpublish removes an app from the marketplace.
func (h *DeveloperHandler) handleUnpublish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Slug == "" || !validName.MatchString(req.Slug) {
		respondError(w, http.StatusBadRequest, "invalid request")
		return
	}

	client := h.vaultClient()
	if client == nil {
		respondError(w, http.StatusBadRequest, "vault not available")
		return
	}

	resp, err := client.Proxy("marketplace", "DELETE", "/api/apps/"+req.Slug, nil)
	if err != nil {
		respondError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		respondJSON(w, http.StatusOK, map[string]any{"success": true})
	} else {
		respondJSON(w, resp.StatusCode, map[string]any{"error": string(body)})
	}
}

func (h *DeveloperHandler) vaultClient() *vaultclient.Client {
	if h.VaultManager == nil {
		return nil
	}
	token := h.VaultManager.ProxyToken()
	if token == "" {
		return nil
	}
	return vaultclient.NewWithSocket(h.VaultManager.SocketPath(), token)
}
