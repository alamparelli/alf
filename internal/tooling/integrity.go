// Package tooling — Tool Integrity Guard (CWE-94 mitigation, issue #121).
//
// The IntegrityGuard watches the user tools/ directory for changes using
// periodic polling. When a file is modified:
//
//  1. New file → hash recorded in manifest, backup created, allowed
//  2. Known file, hash matches → no action
//  3. Known file, hash mismatch → quarantine:
//     a. Modified file renamed to {name}.quarantined
//     b. Previous backup restored as the active tool
//     c. User notified via comms engine
//  4. File deleted → removed from manifest
//
// The user resolves quarantine via /tool keep or /tool revert.
// The manifest is stored at {DataDir}/.daemon/tool-manifest.json, outside
// the LLM-accessible tools/ directory.
package tooling

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrToolQuarantined is returned when a tool fails integrity check.
var ErrToolQuarantined = errors.New("tool quarantined: hash mismatch detected, awaiting user approval")

// UID/GID constants for file ownership.
// alf (uid 1000): LLM user — runs bash, creates tools.
// alfd (uid 1001): daemon — owns .daemon/, quarantine state, integrity data.
const (
	uidAlf  = 1000
	gidAlf  = 1000
	uidAlfd = 1001
	gidAlfd = 1001
)

// ManifestEntry records the approved state of a tool.
type ManifestEntry struct {
	Name       string `json:"name"`
	ExeHash    string `json:"exe_hash"`
	SchemaHash string `json:"schema_hash,omitempty"`
	Size       int64  `json:"size"`
	ModTime    int64  `json:"mod_time"` // unix nano — fast-path skip
	FirstSeen  string `json:"first_seen"`
	LastCheck  string `json:"last_checked"`
}

// QuarantinedTool describes a tool pending user approval.
type QuarantinedTool struct {
	Name    string `json:"name"`
	OldHash string `json:"old_hash"`
	NewHash string `json:"new_hash"`
}

// IntegrityGuard watches tools/ for modifications and quarantines tampered tools.
type IntegrityGuard struct {
	manifestPath    string
	quarantinePath  string // .daemon/tool-quarantine.json — persisted state
	backupDir       string
	quarantineDir   string // .daemon/tool-quarantine/ — inaccessible to LLM user
	toolsDir        string
	manifest        map[string]ManifestEntry
	quarantined     map[string]QuarantinedTool
	mu              sync.Mutex
	notifyFunc      func(tool, oldHash, newHash string)
	stopCh          chan struct{}
}

// NewIntegrityGuard creates a guard that stores manifests under {dataDir}/.daemon/.
// The notifyFunc is called when a tool is quarantined (may be nil for testing).
func NewIntegrityGuard(dataDir string, notify func(tool, oldHash, newHash string)) (*IntegrityGuard, error) {
	daemonDir := filepath.Join(dataDir, ".daemon")
	backupDir := filepath.Join(daemonDir, "tool-backups")
	quarantineDir := filepath.Join(daemonDir, "tool-quarantine")
	for _, d := range []string{daemonDir, backupDir, quarantineDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("integrity: create dir %s: %w", d, err)
		}
		// Ensure .daemon tree is owned by daemon (alfd, uid 1001) and mode 700.
		// This prevents the LLM (alf, uid 1000) from tampering via bash.
		os.Chown(d, uidAlfd, gidAlfd)
		os.Chmod(d, 0o700)
	}

	ig := &IntegrityGuard{
		manifestPath:   filepath.Join(daemonDir, "tool-manifest.json"),
		quarantinePath: filepath.Join(daemonDir, "tool-quarantine.json"),
		backupDir:      backupDir,
		quarantineDir:  quarantineDir,
		toolsDir:       filepath.Join(dataDir, "tools"),
		manifest:       make(map[string]ManifestEntry),
		quarantined:    make(map[string]QuarantinedTool),
		notifyFunc:     notify,
		stopCh:         make(chan struct{}),
	}
	if err := ig.loadManifest(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("integrity: load manifest: %w", err)
	}
	ig.restoreQuarantineState()
	return ig, nil
}

// Watch starts the polling loop. Call Stop() to terminate.
// On first call it does an initial scan to baseline existing tools.
func (ig *IntegrityGuard) Watch(interval time.Duration) {
	// Initial scan — registers all existing tools without quarantine.
	ig.scan(true)
	go ig.pollLoop(interval)
}

// Stop terminates the polling loop.
func (ig *IntegrityGuard) Stop() {
	close(ig.stopCh)
}

func (ig *IntegrityGuard) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ig.stopCh:
			return
		case <-ticker.C:
			ig.scan(false)
		}
	}
}

// scan reads tools/ and checks each file against the manifest.
// If initial=true, new files are registered silently (baseline).
// If initial=false, hash mismatches trigger quarantine.
func (ig *IntegrityGuard) scan(initial bool) {
	entries, err := os.ReadDir(ig.toolsDir)
	if err != nil {
		return
	}

	ig.mu.Lock()
	defer ig.mu.Unlock()

	seen := make(map[string]bool)

	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".quarantined") {
			continue
		}
		name := e.Name()
		seen[name] = true
		toolPath := filepath.Join(ig.toolsDir, name)

		info, err := e.Info()
		if err != nil {
			continue
		}

		entry, exists := ig.manifest[name]

		// Fast path: skip if size and mtime haven't changed.
		if exists && entry.Size == info.Size() && entry.ModTime == info.ModTime().UnixNano() {
			continue
		}

		// Compute hash.
		exeHash, err := hashFile(toolPath)
		if err != nil {
			continue
		}
		schemaHash := ig.hashSchema(name)
		now := time.Now().UTC().Format(time.RFC3339)

		if !exists {
			// New tool — register and backup.
			ig.manifest[name] = ManifestEntry{
				Name:       name,
				ExeHash:    exeHash,
				SchemaHash: schemaHash,
				Size:       info.Size(),
				ModTime:    info.ModTime().UnixNano(),
				FirstSeen:  now,
				LastCheck:  now,
			}
			ig.backup(toolPath)
			ig.saveManifest()
			log.Printf("[integrity] new tool registered: %s (hash=%s)", name, exeHash[:12])
			continue
		}

		// Hash match — update cache fields.
		if entry.ExeHash == exeHash && entry.SchemaHash == schemaHash {
			entry.Size = info.Size()
			entry.ModTime = info.ModTime().UnixNano()
			entry.LastCheck = now
			ig.manifest[name] = entry
			continue
		}

		// Hash mismatch.

		if initial {
			// If already quarantined (restored from disk), don't re-baseline.
			if _, q := ig.quarantined[name]; q {
				if err := ig.restore(name); err != nil {
					log.Printf("[integrity] re-restore failed for quarantined %s on startup: %v", name, err)
				}
				continue
			}
			// During baseline scan, accept the current state.
			entry.ExeHash = exeHash
			entry.SchemaHash = schemaHash
			entry.Size = info.Size()
			entry.ModTime = info.ModTime().UnixNano()
			entry.LastCheck = now
			ig.manifest[name] = entry
			ig.backup(toolPath)
			ig.saveManifest()
			log.Printf("[integrity] baseline updated: %s (hash=%s)", name, exeHash[:12])
			continue
		}

		// Already quarantined — re-restore backup if file content differs from backup.
		if _, q := ig.quarantined[name]; q {
			backupHash, _ := hashFile(filepath.Join(ig.backupDir, name+".prev"))
			if backupHash != "" && exeHash != backupHash {
				if err := ig.restore(name); err != nil {
					log.Printf("[integrity] re-restore failed for quarantined %s: %v", name, err)
				} else {
					log.Printf("[integrity] re-restored quarantined %s (LLM re-wrote)", name)
				}
			}
			lockdownTool(toolPath)
			continue
		}

		// Log-only mode: accept the change, update manifest, log to file.
		// Only quarantine if the new version contains dangerous patterns.
		log.Printf("[integrity] change detected: %s (old=%s new=%s)", name, entry.ExeHash[:12], exeHash[:12])
		ig.logChange(name, entry.ExeHash, exeHash)

		// Check for dangerous patterns — quarantine only if flagged.
		if warnings := auditToolSource(toolPath, name); len(warnings) > 0 {
			log.Printf("[integrity] DANGEROUS PATTERN in %s — quarantining", name)
			for _, w := range warnings {
				log.Printf("[integrity]   %s: %s", w.Pattern, w.Reason)
			}

			quarantinedPath := filepath.Join(ig.quarantineDir, name)
			if err := copyFile(toolPath, quarantinedPath); err != nil {
				log.Printf("[integrity] failed to save quarantined copy: %v", err)
			}
			os.Chmod(quarantinedPath, 0o600)
			os.Chown(quarantinedPath, uidAlfd, gidAlfd)

			if err := ig.restore(name); err != nil {
				log.Printf("[integrity] failed to restore backup for %s: %v", name, err)
			}
			lockdownTool(toolPath)

			qt := QuarantinedTool{
				Name:    name,
				OldHash: entry.ExeHash,
				NewHash: exeHash,
			}
			ig.quarantined[name] = qt
			ig.saveQuarantine()

			// Notify user — dangerous quarantine requires human attention.
			if ig.notifyFunc != nil {
				ig.notifyFunc(name, entry.ExeHash, exeHash)
			}
			continue
		}

		// Safe change — auto-approve: update manifest and backup.
		entry.ExeHash = exeHash
		entry.SchemaHash = schemaHash
		entry.Size = info.Size()
		entry.ModTime = info.ModTime().UnixNano()
		entry.LastCheck = now
		ig.manifest[name] = entry
		ig.backup(toolPath)
		ig.saveManifest()
	}

	// Clean up manifest entries for deleted tools.
	for name := range ig.manifest {
		if !seen[name] {
			delete(ig.manifest, name)
			log.Printf("[integrity] tool removed: %s", name)
		}
	}
}

// Check returns ErrToolQuarantined if the tool is quarantined, nil otherwise.
// Kept for non-execution checks (status queries, UI).
func (ig *IntegrityGuard) Check(toolPath string) error {
	name := filepath.Base(toolPath)
	ig.mu.Lock()
	defer ig.mu.Unlock()
	if _, ok := ig.quarantined[name]; ok {
		return ErrToolQuarantined
	}
	return nil
}

// Verify performs a full integrity check suitable for use immediately before
// execution. Unlike Check (map lookup only), Verify hashes the file on disk
// and compares against the manifest to close the TOCTOU window between scan
// and execution. Returns nil if the tool is safe to execute.
func (ig *IntegrityGuard) Verify(toolPath string) error {
	name := filepath.Base(toolPath)
	ig.mu.Lock()
	defer ig.mu.Unlock()

	// Quarantined tools are always blocked.
	if _, ok := ig.quarantined[name]; ok {
		return ErrToolQuarantined
	}

	// Tool not yet in manifest (new, not scanned yet) — audit inline
	// before allowing execution. This closes the window between file
	// creation and the next scan cycle.
	entry, exists := ig.manifest[name]
	if !exists {
		if warnings := auditToolSource(toolPath, name); len(warnings) > 0 {
			return fmt.Errorf("integrity: new tool %s contains dangerous patterns, blocked pending scan", name)
		}
		// Baseline it now so subsequent calls skip the inline audit.
		hash, err := hashFile(toolPath)
		if err != nil {
			return fmt.Errorf("integrity: cannot hash new tool: %w", err)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		ig.manifest[name] = ManifestEntry{
			Name:      name,
			ExeHash:   hash,
			FirstSeen: now,
			LastCheck: now,
		}
		ig.saveManifest()
		return nil
	}

	// Hash at execution time — closes TOCTOU between periodic scan and exec.
	hash, err := hashFile(toolPath)
	if err != nil {
		return fmt.Errorf("integrity: cannot read tool for verification: %w", err)
	}

	if hash != entry.ExeHash {
		return fmt.Errorf("integrity: tool %s modified since last scan (expected %s, got %s)",
			name, entry.ExeHash[:12], hash[:12])
	}

	return nil
}

// IsQuarantined returns true if the named tool is currently quarantined.
func (ig *IntegrityGuard) IsQuarantined(name string) bool {
	ig.mu.Lock()
	defer ig.mu.Unlock()
	_, ok := ig.quarantined[name]
	return ok
}

// ApproveModified accepts the modified version of a quarantined tool.
func (ig *IntegrityGuard) ApproveModified(name string) error {
	ig.mu.Lock()
	defer ig.mu.Unlock()

	qt, ok := ig.quarantined[name]
	if !ok {
		return fmt.Errorf("tool %q is not quarantined", name)
	}

	toolPath := filepath.Join(ig.toolsDir, name)
	quarantinedPath := filepath.Join(ig.quarantineDir, name)

	// Move quarantined version back as the active tool and restore permissions.
	if err := copyFile(quarantinedPath, toolPath); err != nil {
		return fmt.Errorf("integrity: restore quarantined: %w", err)
	}
	unlockTool(toolPath)
	os.Remove(quarantinedPath)

	// Update manifest with new hash.
	entry := ig.manifest[name]
	entry.ExeHash = qt.NewHash
	entry.LastCheck = time.Now().UTC().Format(time.RFC3339)
	entry.SchemaHash = ig.hashSchema(name)

	// Update size/mtime cache from the now-active file.
	if info, err := os.Stat(toolPath); err == nil {
		entry.Size = info.Size()
		entry.ModTime = info.ModTime().UnixNano()
	}

	ig.manifest[name] = entry
	ig.backup(toolPath)
	ig.saveManifest()

	delete(ig.quarantined, name)
	ig.saveQuarantine()
	log.Printf("[integrity] approved modified tool: %s (new hash=%s)", name, qt.NewHash[:12])
	return nil
}

// RevertTool discards the modified version, keeping the original.
func (ig *IntegrityGuard) RevertTool(name string) error {
	ig.mu.Lock()
	defer ig.mu.Unlock()

	if _, ok := ig.quarantined[name]; !ok {
		return fmt.Errorf("tool %q is not quarantined", name)
	}

	quarantinedPath := filepath.Join(ig.quarantineDir, name)
	os.Remove(quarantinedPath)

	// Update mtime cache so we don't re-trigger on the restored file.
	toolPath := filepath.Join(ig.toolsDir, name)
	if info, err := os.Stat(toolPath); err == nil {
		if entry, ok := ig.manifest[name]; ok {
			entry.Size = info.Size()
			entry.ModTime = info.ModTime().UnixNano()
			ig.manifest[name] = entry
		}
	}

	// Restore execute permission and alf ownership now that quarantine is cleared.
	unlockTool(toolPath)

	delete(ig.quarantined, name)
	ig.saveQuarantine()
	log.Printf("[integrity] reverted tool: %s (kept original)", name)
	return nil
}

// Quarantined returns all tools currently awaiting user approval.
func (ig *IntegrityGuard) Quarantined() []QuarantinedTool {
	ig.mu.Lock()
	defer ig.mu.Unlock()
	result := make([]QuarantinedTool, 0, len(ig.quarantined))
	for _, qt := range ig.quarantined {
		result = append(result, qt)
	}
	return result
}

func (ig *IntegrityGuard) hashSchema(name string) string {
	schemaPath := filepath.Join(ig.toolsDir, name+".json")
	if _, err := os.Stat(schemaPath); err == nil {
		h, _ := hashFile(schemaPath)
		return h
	}
	return ""
}

func (ig *IntegrityGuard) loadManifest() error {
	data, err := os.ReadFile(ig.manifestPath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &ig.manifest)
}

// restoreQuarantineState rebuilds the in-memory quarantine map.
// Primary source: tool-quarantine.json (authoritative).
// Fallback: scan quarantine directory files (backwards compat).
func (ig *IntegrityGuard) restoreQuarantineState() {
	// Try JSON state file first.
	if data, err := os.ReadFile(ig.quarantinePath); err == nil {
		var state map[string]QuarantinedTool
		if json.Unmarshal(data, &state) == nil && len(state) > 0 {
			for name, qt := range state {
				ig.quarantined[name] = qt
				// Re-enforce lockdown on startup (perms may have been tampered with).
				lockdownTool(filepath.Join(ig.toolsDir, name))
				log.Printf("[integrity] restored quarantine state for: %s", name)
			}
			return
		}
	}

	// Fallback: rebuild from quarantine directory files.
	entries, err := os.ReadDir(ig.quarantineDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		quarantinedPath := filepath.Join(ig.quarantineDir, name)
		newHash, err := hashFile(quarantinedPath)
		if err != nil {
			continue
		}
		oldHash := ""
		if entry, ok := ig.manifest[name]; ok {
			oldHash = entry.ExeHash
		}
		ig.quarantined[name] = QuarantinedTool{
			Name:    name,
			OldHash: oldHash,
			NewHash: newHash,
		}
		log.Printf("[integrity] restored quarantine state for: %s (from dir)", name)
	}
	if len(ig.quarantined) > 0 {
		ig.saveQuarantine()
	}
}

// saveQuarantine persists the quarantine map to disk.
func (ig *IntegrityGuard) saveQuarantine() {
	data, err := json.MarshalIndent(ig.quarantined, "", "  ")
	if err != nil {
		log.Printf("[integrity] failed to marshal quarantine state: %v", err)
		return
	}
	if err := os.WriteFile(ig.quarantinePath, data, 0o600); err != nil {
		log.Printf("[integrity] failed to write quarantine state: %v", err)
	}
}

// logChange appends a tool change record to the integrity log file.
func (ig *IntegrityGuard) logChange(name, oldHash, newHash string) {
	logPath := filepath.Join(filepath.Dir(ig.manifestPath), "tool-changes.log")
	entry := fmt.Sprintf("%s\t%s\told=%s\tnew=%s\n",
		time.Now().UTC().Format(time.RFC3339), name, oldHash[:12], newHash[:12])
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(entry)
}

func (ig *IntegrityGuard) saveManifest() {
	data, err := json.MarshalIndent(ig.manifest, "", "  ")
	if err != nil {
		log.Printf("[integrity] failed to marshal manifest: %v", err)
		return
	}
	if err := os.WriteFile(ig.manifestPath, data, 0o600); err != nil {
		log.Printf("[integrity] failed to write manifest: %v", err)
	}
}

func (ig *IntegrityGuard) backup(toolPath string) {
	name := filepath.Base(toolPath)
	dst := filepath.Join(ig.backupDir, name+".prev")
	if err := copyFile(toolPath, dst); err != nil {
		log.Printf("[integrity] backup failed for %s: %v", name, err)
		return
	}
	// Preserve execute bit in backup.
	os.Chmod(dst, 0o755)
}

func (ig *IntegrityGuard) restore(name string) error {
	src := filepath.Join(ig.backupDir, name+".prev")
	dst := filepath.Join(ig.toolsDir, name)
	// Remove target first — it may be owned by a different user (e.g. alf vs alfd).
	os.Remove(dst)
	if err := copyFile(src, dst); err != nil {
		return err
	}
	// Ensure restored tools are executable and group-readable (alf group).
	os.Chmod(dst, 0o775)
	return nil
}

// lockdownTool strips execute permission and changes group to alfd (gid 1001)
// so the LLM user (alf, uid 1000) cannot chmod +x or execute it via bash.
// Only the daemon (alfd) can restore it via ApproveModified.
func lockdownTool(path string) {
	os.Chmod(path, 0o640)           // rw-r----- (no execute)
	os.Chown(path, -1, gidAlfd)
	log.Printf("[integrity] locked down %s (mode=640, group=alfd)", filepath.Base(path))
}

// unlockTool restores normal execute permission and alf group ownership.
func unlockTool(path string) {
	os.Chmod(path, 0o755)
	os.Chown(path, uidAlf, gidAlf)
}

// hashFile computes the SHA-256 hex digest of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyFile copies src to dst, preserving permissions.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode())
}

// IsUserTool returns true if the tool path is under the user tools/ directory
// (not system tools.d/). Only user tools are integrity-checked.
func IsUserTool(toolPath, dataDir string) bool {
	userDir := filepath.Join(dataDir, "tools")
	return strings.HasPrefix(toolPath, userDir+string(filepath.Separator))
}
