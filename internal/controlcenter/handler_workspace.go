package controlcenter

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxFileSize = 1 << 20  // 1 MB
const maxUploadTotal = 10 << 20 // 10 MB total for multi-file upload

// editableExts is no longer used for write gating - all non-binary text files
// under the workspace are editable. Kept only for backward compat in readFile
// (the "editable" JSON field hint to the frontend).
var editableExts map[string]bool // nil - unused

// WorkspaceHandler serves the data directory as a browsable workspace.
// Files under config.d/ are read via the :ro bind mount in DataDir but
// written through ConfigDir (the rw mount at /opt/alf/config.d).
//
//	GET    /api/workspace?path=         → list directory
//	GET    /api/workspace?path=foo.md   → read file
//	PUT    /api/workspace?path=foo.md   → save file (editable extensions only)
//	DELETE /api/workspace?path=foo.md   → delete file
type WorkspaceHandler struct {
	DataDir   string
	ConfigDir string // rw path for config.d writes (e.g. /opt/alf/config.d)
	SkillsDir string // rw path for skills.d writes (e.g. /opt/alf/skills.d)
	Notifier  Notifier
}

type wsEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

func (h *WorkspaceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("path")

	absPath, err := h.resolve(relPath)
	if err != nil {
		http.Error(w, jsonErr("invalid path"), http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.get(w, absPath, relPath)
	case http.MethodPut:
		if h.isReadOnly(relPath) {
			http.Error(w, jsonErr("read-only directory"), http.StatusForbidden)
			return
		}
		writePath := h.resolveWrite(relPath, absPath)
		h.put(w, r, writePath, relPath)
	case http.MethodDelete:
		if h.isReadOnly(relPath) {
			http.Error(w, jsonErr("read-only directory"), http.StatusForbidden)
			return
		}
		writePath := h.resolveWrite(relPath, absPath)
		h.del(w, writePath, relPath)
	default:
		methodNotAllowed(w)
	}
}

// resolve validates and resolves a relative path within DataDir.
// Returns the absolute path or an error if the path escapes DataDir.
func (h *WorkspaceHandler) resolve(rel string) (string, error) {
	cleaned := filepath.Clean(filepath.Join(h.DataDir, rel))

	// Evaluate symlinks so we catch escapes through symlink targets.
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		// If the path doesn't exist yet (for PUT), check the parent.
		parent := filepath.Dir(cleaned)
		resolvedParent, perr := filepath.EvalSymlinks(parent)
		if perr != nil {
			return "", perr
		}
		if !h.isAllowedPath(resolvedParent) {
			return "", os.ErrPermission
		}
		return cleaned, nil
	}

	if !h.isAllowedPath(resolved) {
		return "", os.ErrPermission
	}
	return resolved, nil
}

// isAllowedPath checks if a resolved path falls within DataDir, ConfigDir, or SkillsDir.
// Uses directory-boundary-safe prefix check to prevent prefix confusion attacks
// (e.g. /home/alf/data-evil matching /home/alf/data).
func (h *WorkspaceHandler) isAllowedPath(resolved string) bool {
	if pathWithinDir(resolved, h.realDataDir()) {
		return true
	}
	if h.ConfigDir != "" {
		if real, err := filepath.EvalSymlinks(h.ConfigDir); err == nil {
			if pathWithinDir(resolved, real) {
				return true
			}
		}
	}
	if h.SkillsDir != "" {
		if real, err := filepath.EvalSymlinks(h.SkillsDir); err == nil {
			if pathWithinDir(resolved, real) {
				return true
			}
		}
	}
	return false
}

// pathWithinDir checks if path is exactly dir or a child of dir, with a proper
// directory boundary check to prevent prefix confusion.
func pathWithinDir(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// isReadOnly returns true if relPath falls inside a read-only directory.
// Currently no directories are read-only - config.d and skills.d writes are
// remapped to their rw mounts by resolveWrite.
func (h *WorkspaceHandler) isReadOnly(relPath string) bool {
	return false
}

// resolveWrite returns the writable path for a given relPath.
// For config.d/ paths, writes go through ConfigDir (rw mount) instead of
// the :ro bind mount visible in DataDir.
func (h *WorkspaceHandler) resolveWrite(relPath, absPath string) string {
	if h.ConfigDir != "" && (relPath == "config.d" || strings.HasPrefix(relPath, "config.d/")) {
		sub := strings.TrimPrefix(relPath, "config.d")
		sub = strings.TrimPrefix(sub, "/")
		return filepath.Join(h.ConfigDir, sub)
	}
	if h.SkillsDir != "" && (relPath == "skills.d" || strings.HasPrefix(relPath, "skills.d/")) {
		sub := strings.TrimPrefix(relPath, "skills.d")
		sub = strings.TrimPrefix(sub, "/")
		return filepath.Join(h.SkillsDir, sub)
	}
	return absPath
}

// realDataDir returns the symlink-resolved DataDir (cached on first call would
// be nice but keeping it simple).
func (h *WorkspaceHandler) realDataDir() string {
	resolved, err := filepath.EvalSymlinks(h.DataDir)
	if err != nil {
		return h.DataDir
	}
	return resolved
}

func (h *WorkspaceHandler) get(w http.ResponseWriter, absPath, relPath string) {
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, jsonErr("not found"), http.StatusNotFound)
		} else {
			http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		}
		return
	}

	if info.IsDir() {
		h.listDir(w, absPath, relPath)
	} else {
		h.readFile(w, absPath, relPath, info)
	}
}

func (h *WorkspaceHandler) listDir(w http.ResponseWriter, absPath, relPath string) {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}

	var dirs, files []wsEntry
	for _, e := range entries {
		name := e.Name()
		// Hide .git directory.
		if name == ".git" {
			continue
		}
		// Hide hidden files at root only (dotfiles like .claude).
		if strings.HasPrefix(name, ".") && relPath == "" {
			continue
		}

		// Use os.Stat (not Lstat) to follow symlinks - so symlinked dirs
		// appear as directories in the file browser, not as tiny files.
		fullPath := filepath.Join(absPath, name)

		// Skip self-referencing symlinks to prevent infinite directory loops.
		if e.Type()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(fullPath)
			if err != nil {
				continue
			}
			resolvedParent, _ := filepath.EvalSymlinks(absPath)
			if target == resolvedParent || strings.HasPrefix(resolvedParent, target+string(filepath.Separator)) {
				continue
			}
		}

		info, err := os.Stat(fullPath)
		if err != nil {
			continue
		}
		isDir := info.IsDir()
		entry := wsEntry{Name: name, IsDir: isDir, Size: info.Size()}
		if isDir {
			dirs = append(dirs, entry)
		} else {
			files = append(files, entry)
		}
	}

	// Sort: dirs first, then files, alphabetical within each group.
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	all := append(dirs, files...)
	if all == nil {
		all = []wsEntry{}
	}

	resp := map[string]any{
		"type":    "directory",
		"path":    relPath,
		"entries": all,
	}
	// Include protected + readOnly lists on root listing for frontend.
	if relPath == "" {
		names := make([]string, 0, len(protectedDirs))
		for k := range protectedDirs {
			names = append(names, k)
		}
		sort.Strings(names)
		resp["protected"] = names
		resp["readOnly"] = []string{}
	}
	data, _ := json.Marshal(resp)
	w.Write(data)
}

func (h *WorkspaceHandler) readFile(w http.ResponseWriter, absPath, relPath string, info os.FileInfo) {
	editable := !h.isReadOnly(relPath)

	if info.Size() > maxFileSize {
		data, _ := json.Marshal(map[string]any{
			"type":     "file",
			"name":     info.Name(),
			"size":     info.Size(),
			"editable": false,
			"message":  "File too large to display (max 1 MB)",
		})
		w.Write(data)
		return
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		return
	}

	if isBinary(content) {
		data, _ := json.Marshal(map[string]any{
			"type":     "file",
			"name":     info.Name(),
			"size":     info.Size(),
			"editable": false,
			"message":  "Binary file - cannot display",
		})
		w.Write(data)
		return
	}

	data, _ := json.Marshal(map[string]any{
		"type":     "file",
		"name":     info.Name(),
		"size":     info.Size(),
		"editable": editable,
		"content":  string(content),
	})
	w.Write(data)
}

func (h *WorkspaceHandler) put(w http.ResponseWriter, r *http.Request, absPath, relPath string) {
	if relPath == "" {
		http.Error(w, jsonErr("cannot write to root directory"), http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxFileSize+1024))
	if err != nil {
		http.Error(w, jsonErr("failed to read body"), http.StatusBadRequest)
		return
	}

	var payload struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, jsonErr("invalid JSON: "+err.Error()), http.StatusBadRequest)
		return
	}

	if len(payload.Content) > maxFileSize {
		http.Error(w, jsonErr("content too large (max 1 MB)"), http.StatusRequestEntityTooLarge)
		return
	}

	// Atomic write: tmp file + rename.
	dir := filepath.Dir(absPath)
	tmp, err := os.CreateTemp(dir, ".ws-*.tmp")
	if err != nil {
		http.Error(w, jsonErr("write failed: "+err.Error()), http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()

	if _, err := tmp.WriteString(payload.Content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		http.Error(w, jsonErr("write failed"), http.StatusInternalServerError)
		return
	}
	tmp.Close()

	if err := os.Rename(tmpName, absPath); err != nil {
		os.Remove(tmpName)
		http.Error(w, jsonErr("write failed: "+err.Error()), http.StatusInternalServerError)
		return
	}

	h.notifyChange(relPath)
	w.Write([]byte(`{"ok":true}`))
}

// protectedDirs are directories that cannot be deleted (top-level and nested).
var protectedDirs = map[string]bool{
	"config":          true,
	"config.d":        true,
	"agents/teams": true,
	"context":         true,
	"docs":            true,
	"logs":            true,
	"apps":            true,
	"sessions":        true,
	"skills":          true,
	"skills.d":        true,
	"tools":           true,
	"tools.d":         true,
}

func (h *WorkspaceHandler) del(w http.ResponseWriter, absPath, relPath string) {
	if relPath == "" {
		http.Error(w, jsonErr("cannot delete root"), http.StatusBadRequest)
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, jsonErr("not found"), http.StatusNotFound)
		} else {
			http.Error(w, jsonErr(err.Error()), http.StatusInternalServerError)
		}
		return
	}

	if info.IsDir() {
		// Protect system directories.
		if protectedDirs[relPath] {
			http.Error(w, jsonErr("cannot delete system directory"), http.StatusForbidden)
			return
		}
		if err := os.RemoveAll(absPath); err != nil {
			http.Error(w, jsonErr("delete failed: "+err.Error()), http.StatusInternalServerError)
			return
		}
	} else {
		if err := os.Remove(absPath); err != nil {
			http.Error(w, jsonErr("delete failed: "+err.Error()), http.StatusInternalServerError)
			return
		}
	}

	h.notifyChange(relPath)
	w.Write([]byte(`{"ok":true}`))
}

// notifyChange sends reload events based on which file was modified.
func (h *WorkspaceHandler) notifyChange(relPath string) {
	if h.Notifier == nil {
		return
	}
	switch {
	case relPath == "config.d/config.json":
		h.Notifier.Notify(ReloadConfig)
	case relPath == "config.d/tiers.json":
		h.Notifier.Notify(ReloadTiers)
	case strings.HasPrefix(relPath, "tools"):
		h.Notifier.Notify(ReloadTools)
	case strings.HasPrefix(relPath, "skills") || strings.HasPrefix(relPath, "skills.d"):
		h.Notifier.Notify(ReloadSkills)
	case strings.HasPrefix(relPath, "agents/teams"):
		h.Notifier.Notify(ReloadAgents)
	}
}

// UploadHandler handles multi-file uploads to a target directory.
//
//	POST /api/workspace/upload  (multipart/form-data)
//	  - "target" form field: destination directory relative to DataDir
//	  - "files" form field(s): file content (supports multiple)
type UploadHandler struct {
	DataDir   string
	ConfigDir string
	SkillsDir string
	Notifier  Notifier
}

func (h *UploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	if err := r.ParseMultipartForm(maxUploadTotal); err != nil {
		http.Error(w, jsonErr("request too large or invalid multipart"), http.StatusBadRequest)
		return
	}

	target := r.FormValue("target")

	// Validate target directory.
	wsH := &WorkspaceHandler{DataDir: h.DataDir, ConfigDir: h.ConfigDir, SkillsDir: h.SkillsDir}
	targetAbs, err := wsH.resolve(target)
	if err != nil {
		http.Error(w, jsonErr("invalid target path"), http.StatusBadRequest)
		return
	}

	// Ensure target exists and is a directory.
	info, err := os.Stat(targetAbs)
	if err != nil && !os.IsNotExist(err) {
		http.Error(w, jsonErr("target error"), http.StatusInternalServerError)
		return
	}
	if err == nil && !info.IsDir() {
		http.Error(w, jsonErr("target is not a directory"), http.StatusBadRequest)
		return
	}

	// Use the writable path for the target (config.d, skills.d remap).
	writeTarget := wsH.resolveWrite(target, targetAbs)

	// Create target directory if it doesn't exist.
	if err := os.MkdirAll(writeTarget, 0755); err != nil {
		http.Error(w, jsonErr("failed to create target directory"), http.StatusInternalServerError)
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		http.Error(w, jsonErr("no files provided"), http.StatusBadRequest)
		return
	}

	// Also accept webkitRelativePath hints for preserving folder structure.
	relativePaths := r.MultipartForm.Value["paths"]

	var saved []string
	for i, fh := range files {
		if fh.Size > maxFileSize {
			http.Error(w, jsonErr("file too large: "+fh.Filename), http.StatusRequestEntityTooLarge)
			return
		}

		// Determine destination path. If a relative path is provided, preserve directory structure.
		destName := filepath.Base(fh.Filename)
		if i < len(relativePaths) && relativePaths[i] != "" {
			// Sanitize the relative path.
			relP := filepath.Clean(relativePaths[i])
			if !strings.HasPrefix(relP, "..") && relP != "." {
				destName = relP
			}
		}

		destPath := filepath.Join(writeTarget, destName)

		// Security: ensure we don't escape the target directory.
		if !pathWithinDir(filepath.Clean(destPath), filepath.Clean(writeTarget)) {
			http.Error(w, jsonErr("invalid file path: "+fh.Filename), http.StatusBadRequest)
			return
		}

		// Ensure parent directories exist for nested paths.
		if dir := filepath.Dir(destPath); dir != writeTarget {
			if err := os.MkdirAll(dir, 0755); err != nil {
				http.Error(w, jsonErr("failed to create directory"), http.StatusInternalServerError)
				return
			}
		}

		src, err := fh.Open()
		if err != nil {
			http.Error(w, jsonErr("failed to read file: "+fh.Filename), http.StatusInternalServerError)
			return
		}

		content, err := io.ReadAll(io.LimitReader(src, maxFileSize+1))
		src.Close()
		if err != nil {
			http.Error(w, jsonErr("failed to read file: "+fh.Filename), http.StatusInternalServerError)
			return
		}

		if err := os.WriteFile(destPath, content, 0644); err != nil {
			http.Error(w, jsonErr("failed to write file: "+fh.Filename), http.StatusInternalServerError)
			return
		}

		relSaved := target
		if relSaved != "" {
			relSaved += "/"
		}
		relSaved += destName
		saved = append(saved, relSaved)
	}

	// Notify about changes.
	if h.Notifier != nil {
		for _, s := range saved {
			switch {
			case strings.HasPrefix(s, "tools"):
				h.Notifier.Notify(ReloadTools)
			case strings.HasPrefix(s, "skills") || strings.HasPrefix(s, "skills.d"):
				h.Notifier.Notify(ReloadSkills)
			case s == "config.d/config.json":
				h.Notifier.Notify(ReloadConfig)
			case s == "config.d/tiers.json":
				h.Notifier.Notify(ReloadTiers)
			}
		}
	}

	data, _ := json.Marshal(map[string]any{
		"ok":    true,
		"files": saved,
	})
	w.Write(data)
}

// isBinary checks if content is likely binary by looking for null bytes
// and checking UTF-8 validity.
func isBinary(data []byte) bool {
	// Check first 8KB for null bytes.
	check := data
	if len(check) > 8192 {
		check = check[:8192]
	}
	for _, b := range check {
		if b == 0 {
			return true
		}
	}
	return !utf8.Valid(check)
}
