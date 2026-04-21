package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/skills"
)

// ensureBashrcPath adds ~/.local/bin to PATH in .bashrc if not already present.
// This fixes the "native installation exists but ~/.local/bin is not in your PATH" warning
// for interactive shells (CC terminal, docker exec).
func ensureBashrcPath(home string) {
	if home == "" {
		return
	}
	bashrc := filepath.Join(home, ".bashrc")
	line := `export PATH="$HOME/.local/bin:$PATH"`

	// Check if already present.
	if data, err := os.ReadFile(bashrc); err == nil {
		if strings.Contains(string(data), ".local/bin") {
			return
		}
	}

	f, err := os.OpenFile(bashrc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("bashrc: cannot write %s: %v", bashrc, err)
		return
	}
	defer f.Close()
	f.WriteString("\n" + line + "\n")
	log.Printf("bashrc: added .local/bin to PATH")
}

// linkSystemTools recreates symlinks in toolsDir for each binary in srcDir.
// Removes all existing symlinks first to clean up tools removed after an upgrade.
func linkSystemTools(toolsDir, srcDir string) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}
	os.MkdirAll(toolsDir, 0o755)

	// Remove all existing symlinks (stale ones from previous versions).
	if existing, err := os.ReadDir(toolsDir); err == nil {
		for _, e := range existing {
			p := filepath.Join(toolsDir, e.Name())
			if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
				os.Remove(p)
			}
		}
	}

	// Recreate symlinks for current tools.
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		link := filepath.Join(toolsDir, e.Name())
		target := filepath.Join(srcDir, e.Name())
		if err := os.Symlink(target, link); err == nil {
			log.Printf("linked tools.d/%s → %s", e.Name(), target)
		}
	}

	// Lock down: tools.d is system-managed.
	// Ownership set by entrypoint (root) — daemon cannot chown.
	os.Chmod(toolsDir, 0o755)
}

// seedDefaultTiers copies /opt/alf/defaults/tiers.json into config.d/tiers/claude.json
// if no tiers profiles exist yet. Also migrates legacy config.d/tiers.json to the new location.
// Additionally seeds all embedded setup presets as available tier profiles.
func seedDefaultTiers(configDir string) {
	tiersDir := filepath.Join(configDir, "tiers")
	os.MkdirAll(tiersDir, 0o750)

	dest := filepath.Join(tiersDir, "claude.json")

	// Migrate legacy config.d/tiers.json → config.d/tiers/claude.json
	legacyPath := filepath.Join(configDir, "tiers.json")
	if _, err := os.Stat(legacyPath); err == nil {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			if data, err := os.ReadFile(legacyPath); err == nil {
				os.WriteFile(dest, data, 0o644)
				log.Printf("seed-tiers: migrated %s → %s", legacyPath, dest)
			}
		}
	}

	// Seed embedded presets as tier profiles (skip if already exists).
	// This runs first so the "claude" preset seeds claude.json correctly.
	seedPresetsAsTierProfiles(tiersDir)

	// Seed default claude.json from legacy defaults if still missing
	// (only applies when no "claude" preset exists in embedded presets).
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		const defaultPath = "/opt/alf/defaults/tiers.json"
		data, err := os.ReadFile(defaultPath)
		if err != nil {
			log.Printf("seed-tiers: no default at %s: %v", defaultPath, err)
		} else if err := os.WriteFile(dest, data, 0o644); err != nil {
			log.Printf("seed-tiers: failed to write %s: %v", dest, err)
		} else {
			log.Printf("seed-tiers: created %s from defaults", dest)
		}
	}
}

// seedDefaultClaudeModels writes the embedded claude_models.txt into
// configDir on first run, giving users a pre-populated file they can edit
// to add new Claude models without waiting for a daemon update.
// Existing files are left untouched.
func seedDefaultClaudeModels(configDir string) {
	dest := cc.ClaudeModelsPath(configDir)
	if _, err := os.Stat(dest); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		log.Printf("seed-claude-models: create dir: %v", err)
		return
	}
	if err := os.WriteFile(dest, cc.DefaultClaudeModelsTxt(), 0o644); err != nil {
		log.Printf("seed-claude-models: write %s: %v", dest, err)
		return
	}
	log.Printf("seed-claude-models: created %s", dest)
}

// seedPresetsAsTierProfiles converts embedded setup presets into tier profile files
// in config.d/tiers/. Each preset is written as <id>.json with the TiersConfig format.
// Existing profiles are not overwritten.
func seedPresetsAsTierProfiles(tiersDir string) {
	for _, presets := range cc.LoadEmbeddedPresets() {
		for _, p := range presets {
			if p.ID == "" {
				continue
			}
			dest := filepath.Join(tiersDir, p.ID+".json")
			if _, err := os.Stat(dest); err == nil {
				continue // already exists, don't overwrite
			}
			tc := cc.PresetToTiersConfig(p)
			data, err := json.MarshalIndent(tc, "", "  ")
			if err != nil {
				log.Printf("seed-tiers: failed to marshal preset %s: %v", p.ID, err)
				continue
			}
			if err := os.WriteFile(dest, data, 0o644); err != nil {
				log.Printf("seed-tiers: failed to write preset %s: %v", p.ID, err)
				continue
			}
			log.Printf("seed-tiers: seeded preset %s", p.ID)
		}
	}
}

// syncClaudeJSON persists .claude.json across container rebuilds.
// Claude CLI replaces symlinks with real files, so we can't use a symlink.
// Strategy: on startup, restore from the .claude/ volume if the file is missing;
// after restoring (or if already present), back it up into the volume.
func syncClaudeJSON(homeDir string) {
	realFile := filepath.Join(homeDir, ".claude.json")
	volumeCopy := filepath.Join(homeDir, ".claude", "claude.json")

	// If .claude.json is a symlink (from Dockerfile), remove it - we use copies now.
	if fi, err := os.Lstat(realFile); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		os.Remove(realFile)
	}

	if _, err := os.Stat(realFile); os.IsNotExist(err) {
		// File missing (fresh container or rebuild). Try restoring from volume.
		if data, err := os.ReadFile(volumeCopy); err == nil && len(data) > 0 {
			os.WriteFile(realFile, data, 0o640)
			log.Printf("claude-json: restored from volume backup")
		} else {
			// Check for Claude's own backup files.
			backupDir := filepath.Join(homeDir, ".claude", "backups")
			entries, _ := os.ReadDir(backupDir)
			var newest string
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".claude.json.backup.") {
					newest = filepath.Join(backupDir, e.Name())
				}
			}
			if newest != "" {
				if data, err := os.ReadFile(newest); err == nil {
					os.WriteFile(realFile, data, 0o640)
					log.Printf("claude-json: restored from Claude backup %s", filepath.Base(newest))
					// Remove backup files so Claude CLI doesn't warn about them.
					for _, e := range entries {
						if strings.HasPrefix(e.Name(), ".claude.json.backup.") {
							os.Remove(filepath.Join(backupDir, e.Name()))
						}
					}
				}
			}
		}
	}

	// Fresh install: no volume copy, no backups. Create a minimal stub so
	// Claude CLI subprocesses (classifier, provider) don't warn on every call.
	if _, err := os.Stat(realFile); os.IsNotExist(err) {
		stub := []byte(`{"hasCompletedOnboarding":true,"numStartups":1}`)
		if err := os.WriteFile(realFile, stub, 0o640); err == nil {
			log.Printf("claude-json: created default stub (fresh install)")
		}
	}

	// Ensure hasCompletedOnboarding is set. Users who authenticate via
	// "claude login" get a valid OAuth token but skip the interactive
	// onboarding that sets this flag. Without it, "claude -p" invocations fail.
	if data, err := os.ReadFile(realFile); err == nil && len(data) > 2 {
		var cfg map[string]any
		if json.Unmarshal(data, &cfg) == nil {
			changed := false
			if v, ok := cfg["hasCompletedOnboarding"]; !ok || v != true {
				cfg["hasCompletedOnboarding"] = true
				changed = true
			}
			if _, ok := cfg["numStartups"]; !ok {
				cfg["numStartups"] = 1
				changed = true
			}
			if changed {
				if patched, err := json.MarshalIndent(cfg, "", "  "); err == nil {
					os.WriteFile(realFile, patched, 0o640)
					log.Printf("claude-json: patched hasCompletedOnboarding=true")
				}
			}
		}
	}

	// Back up current file into volume for next rebuild.
	if data, err := os.ReadFile(realFile); err == nil && len(data) > 0 {
		os.WriteFile(volumeCopy, data, 0o640)
	}

	// Permissions are handled by entrypoint (Phase 2.5) before dropping to uid 1000.
}

// cleanClaudeSettings removes .claude/settings.json at startup.
// Claude Code may persist restrictive allow-lists in this file which then
// block tools (Edit, Write, etc.) even when --dangerously-skip-permissions
// is used. Deleting it on restart ensures a clean slate every time.
func cleanClaudeSettings(homeDir string) {
	p := filepath.Join(homeDir, ".claude", "settings.json")
	if err := os.Remove(p); err != nil {
		if !os.IsNotExist(err) {
			log.Printf("clean-settings: failed to remove %s: %v", p, err)
		}
		return
	}
	log.Printf("clean-settings: removed stale %s", p)
}

func migrateConfig(dataDir, configDir string) {
	oldConfigDir := filepath.Join(dataDir, "config")

	// Config files: copy if missing in configDir.
	for _, name := range []string{"config.json", "tiers.json", "router-prompt.md"} {
		dst := filepath.Join(configDir, name)
		if _, err := os.Stat(dst); err == nil {
			continue // already exists
		}
		src := filepath.Join(oldConfigDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			continue // no old file
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			log.Printf("migrate: failed to copy %s: %v", name, err)
			continue
		}
		log.Printf("migrate: %s → %s", src, dst)
	}

	// Migrate cron.json from context/ to config/ (was exposed to Claude's context injection).
	oldCron := filepath.Join(dataDir, "context", "cron.json")
	newCron := filepath.Join(configDir, "cron.json")
	if _, err := os.Stat(newCron); os.IsNotExist(err) {
		if data, err := os.ReadFile(oldCron); err == nil {
			if err := os.WriteFile(newCron, data, 0o644); err == nil {
				os.Remove(oldCron)
				log.Printf("migrate: cron.json → %s", newCron)
			}
		}
	} else {
		os.Remove(oldCron) // clean up old location even if new exists
	}

	// Clean up orphan directories from old layout.
	for _, orphan := range []string{"tiers", "memory", "state"} {
		p := filepath.Join(dataDir, orphan)
		if _, err := os.Stat(p); err == nil {
			if err := os.RemoveAll(p); err != nil {
				log.Printf("migrate: failed to remove old %s: %v", orphan, err)
			} else {
				log.Printf("migrate: removed orphan %s/", orphan)
			}
		}
	}
}

// seedREADMEs creates a README.md in each data subdirectory explaining its purpose.
// Only writes if the file doesn't already exist.
func seedREADMEs(dataDir string) {
	readmes := map[string]string{
		"config.d": "# config.d\n\nALF configuration files.\n\n- `config.json` - main configuration (backends, DNS, broadcast channel)\n- `tiers.json` - LLM tier definitions (models, routing, tools access)\n- `cron.json` - scheduled jobs\n- `router-prompt.md` - custom router prompt (optional)\n",
		"context": "# context\n\nLLM context files injected into every conversation.\n\n- `soul.md` - personality and behavior instructions\n- `preferences.md` - learned user preferences (auto-updated from reactions)\n- `heartbeat.md` - heartbeat check template\n- `memory.db` - semantic memory store (SQLite + vector embeddings)\n",
		"docs": "# docs\n\nBuilt-in documentation (auto-generated from embedded docs on each startup).\nThese files are read-only and will be overwritten on restart.\n\nBrowse in the Docs tab of the Control Center.\n",
		"logs": "# logs\n\nConversation and event logs.\n\n- `conversation.jsonl` - unified conversation store (all channels)\n- `events.jsonl` - system event log (messages, reactions, sessions)\n",
		"sessions": "# sessions\n\nClaude CLI session files for conversation continuity.\nManaged automatically. Safe to delete to reset sessions.\n",
		"tools": "# tools\n\nUser-created tools (JSON schema + executable).\nEach tool is a JSON file defining its schema and a matching executable.\n\nSee the docs on creating tools for more info.\n",
		"skills": "# skills\n\nUser-created skills. Each skill is a directory with a SKILL.md file.\nSkills extend ALF's capabilities with custom prompts and workflows.\n\nActivate with `/skillname` in chat.\n",
		"skills.d": "# skills.d\n\nSystem skills (bundled with ALF). Seeded on first boot.\nYou can override a system skill by creating one with the same name in `skills/`.\n",
		"agents/teams": "# agents/teams\n\nAgent team definitions (JSON). Each file defines a team of specialized agents\nthat can collaborate on complex tasks.\n\nManage teams in the Teams tab of the Control Center.\n",
		"apps": "# apps\n\nUser-created web applications. Each app is a directory with an index.html.\nBuild apps using the app-builder skill or create them manually.\n\nAccessible in the Apps section of the sidebar.\n",
	}

	for dir, content := range readmes {
		fullDir := filepath.Join(dataDir, dir)
		os.MkdirAll(fullDir, 0o755)
		readme := filepath.Join(fullDir, "README.md")
		if _, err := os.Stat(readme); err == nil {
			continue
		}
		os.WriteFile(readme, []byte(content), 0o644)
	}
}

// writeLLMSIndex generates a llms.txt file in dataDir with an index of all embedded docs.
// This lets the LLM quickly discover available documentation.
func writeLLMSIndex(dataDir string) {
	entries, err := cc.DocsFS().ReadDir("docs")
	if err != nil {
		return
	}

	var b strings.Builder
	b.WriteString("# ALF Documentation Index\n")
	b.WriteString("# Read any doc: cat ~/data/docs/<id>.md\n\n")

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		data, err := cc.DocsFS().ReadFile("docs/" + e.Name())
		if err != nil {
			continue
		}
		// Extract title and summary from doc content.
		title, summary := id, ""
		foundTitle := false
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if !foundTitle {
				if strings.HasPrefix(trimmed, "# ") {
					title = strings.TrimPrefix(trimmed, "# ")
					foundTitle = true
				}
				continue
			}
			if trimmed == "" || strings.HasPrefix(trimmed, "---") {
				continue
			}
			if strings.HasPrefix(trimmed, "#") {
				break
			}
			summary = trimmed
			if len(summary) > 120 {
				summary = summary[:117] + "..."
			}
			break
		}
		if summary != "" {
			b.WriteString(fmt.Sprintf("- %s: %s — %s\n", id, title, summary))
		} else {
			b.WriteString(fmt.Sprintf("- %s: %s\n", id, title))
		}
	}

	// Write docs to filesystem so LLM can read them (read-only).
	docsDir := filepath.Join(dataDir, "docs")
	os.MkdirAll(docsDir, 0o755)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, _ := cc.DocsFS().ReadFile("docs/" + e.Name())
		dest := filepath.Join(docsDir, e.Name())
		os.Chmod(dest, 0o644) // make writable before overwrite
		os.WriteFile(dest, data, 0o444)
	}

	llmsPath := filepath.Join(dataDir, "llms.txt")
	os.Chmod(llmsPath, 0o644) // make writable before overwrite
	os.WriteFile(llmsPath, []byte(b.String()), 0o444)
	log.Printf("docs: wrote llms.txt (%d docs)", len(entries))
}

// fixContextPermissions ensures all files in context/ are group-readable (0o664).
// Claude CLI subprocesses may create or overwrite files with a restrictive umask,
// making them unreadable by other alf-group processes.
func fixContextPermissions(contextDir string) {
	entries, err := os.ReadDir(contextDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(contextDir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode().Perm() != 0o664 {
			if err := os.Chmod(path, 0o664); err != nil {
				log.Printf("fixContextPermissions: chmod %s: %v", e.Name(), err)
			} else {
				log.Printf("fixContextPermissions: fixed %s (%04o → 0664)", e.Name(), info.Mode().Perm())
			}
		}
	}
}

// seedHeartbeatFile creates a default context/heartbeat.md if it doesn't exist.
func seedHeartbeatFile(contextDir string) {
	hbPath := filepath.Join(contextDir, "heartbeat.md")
	if _, err := os.Stat(hbPath); err == nil {
		return // already exists
	}
	os.MkdirAll(contextDir, 0o755)
	content := `---
---

`
	if err := os.WriteFile(hbPath, []byte(content), 0o644); err != nil {
		log.Printf("seed heartbeat.md: %v", err)
	} else {
		log.Printf("seeded default context/heartbeat.md")
	}
}

// setupDataSymlinks creates symlinks inside data/ pointing to config.d and skills.d.
func setupDataSymlinks(dataDir, configDir, skillsDir string) {
	links := map[string]string{
		filepath.Join(dataDir, "config.d"): configDir,
		filepath.Join(dataDir, "skills.d"): skillsDir,
	}
	for link, target := range links {
		if dest, err := os.Readlink(link); err == nil && dest == target {
			continue
		}
		os.RemoveAll(link)
		if err := os.Symlink(target, link); err != nil {
			log.Printf("symlink %s → %s: %v", link, target, err)
		} else {
			log.Printf("symlink %s → %s", filepath.Base(link), target)
		}
	}
}

// deprecatedSkills lists skill directories that have been replaced and should be
// removed on upgrade. Maps old name → replacement (for logging).
var deprecatedSkills = map[string]string{
	"app-builder": "sdk-app-builder",
}

// cleanDeprecatedSkills removes skills that have been superseded by newer versions.
func cleanDeprecatedSkills(skillsDir string) {
	for old, replacement := range deprecatedSkills {
		p := filepath.Join(skillsDir, old)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			continue
		}
		if err := os.RemoveAll(p); err != nil {
			log.Printf("clean-skills: failed to remove deprecated %s: %v", old, err)
		} else {
			log.Printf("clean-skills: removed deprecated %s (replaced by %s)", old, replacement)
		}
	}
}

// seedBundledSkills copies skill directories from /opt/alf/defaults/skills.d
// into the active skills directory. New skills are created; existing skills are
// updated when the bundled version is newer (based on SKILL.md version field).
func seedBundledSkills(skillsDir string) {
	const defaultsDir = "/opt/alf/defaults/skills.d"
	entries, err := os.ReadDir(defaultsDir)
	if err != nil {
		return // no defaults directory (e.g. running outside Docker)
	}
	os.MkdirAll(skillsDir, 0o755)
	for _, e := range entries {
		if !e.IsDir() {
			// Copy top-level files (e.g. README.md).
			src := filepath.Join(defaultsDir, e.Name())
			dst := filepath.Join(skillsDir, e.Name())
			if _, err := os.Stat(dst); err == nil {
				continue
			}
			data, err := os.ReadFile(src)
			if err == nil {
				os.WriteFile(dst, data, 0o644)
				log.Printf("seeded skill file: %s", e.Name())
			}
			continue
		}
		src := filepath.Join(defaultsDir, e.Name())
		dest := filepath.Join(skillsDir, e.Name())
		if _, err := os.Stat(dest); err == nil {
			// Skill exists — check if bundled version is newer.
			if !bundledSkillNewer(src, dest) {
				continue
			}
			log.Printf("upgrading bundled skill: %s", e.Name())
			os.RemoveAll(dest)
		}
		if err := copyDir(src, dest); err != nil {
			log.Printf("seed skill %s: %v", e.Name(), err)
		} else {
			log.Printf("seeded bundled skill: %s", e.Name())
		}
	}
}

// bundledSkillNewer returns true if the bundled SKILL.md has a higher version than installed.
func bundledSkillNewer(srcDir, destDir string) bool {
	srcVer := readSkillVersion(filepath.Join(srcDir, "SKILL.md"))
	destVer := readSkillVersion(filepath.Join(destDir, "SKILL.md"))
	if srcVer == "" || destVer == "" {
		return false
	}
	return srcVer > destVer
}

// readSkillVersion extracts the version field from SKILL.md frontmatter.
func readSkillVersion(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		return ""
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(content[4:4+end], "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, ":"); ok {
			if strings.TrimSpace(k) == "version" {
				return strings.Trim(strings.TrimSpace(v), "\"'")
			}
		}
	}
	return ""
}

// seedBundledTeams copies missing team JSON files from /opt/alf/defaults/teams
// into the active teams directory. Existing teams are never overwritten.
func seedBundledTeams(dataDir string) {
	const defaultsDir = "/opt/alf/defaults/teams"
	teamsDir := filepath.Join(dataDir, "agents", "teams")
	entries, err := os.ReadDir(defaultsDir)
	if err != nil {
		return
	}
	os.MkdirAll(teamsDir, 0o755)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		dst := filepath.Join(teamsDir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(defaultsDir, e.Name()))
		if err == nil {
			os.WriteFile(dst, data, 0o644)
			log.Printf("seeded bundled team: %s", e.Name())
		}
	}
}

// seedBundledApps copies marketplace app defaults from /opt/alf/defaults/apps/
// into the active apps directory. Each app gets its binary, manifest, and web files seeded.
// On upgrade, new files (e.g. index.html, app.json) are added without overwriting user data.
// The data/ subdirectory is never touched.
func seedBundledApps(dataDir string) {
	const defaultsDir = "/opt/alf/defaults/apps"
	appsDir := filepath.Join(dataDir, "apps")
	entries, err := os.ReadDir(defaultsDir)
	if err != nil {
		return // no defaults directory (e.g. running outside Docker)
	}
	os.MkdirAll(appsDir, 0o755)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()

		// Protected apps are not auto-seeded.
		if isProtectedApp(slug) {
			continue
		}

		src := filepath.Join(defaultsDir, slug)
		dest := filepath.Join(appsDir, slug)

		// Always sync bundled files (bin/, manifest.json, index.html, app.json).
		// Skip the data/ subdirectory to preserve user data.
		if err := seedAppFiles(src, dest); err != nil {
			log.Printf("seed app %s: %v", slug, err)
		} else {
			log.Printf("seeded bundled app: %s", slug)
		}
	}

	// Set default-disabled apps to "disabled" in .state.json if they have no state yet.
	seedDefaultDisabledState(appsDir)
}

// seedDefaultDisabledState ensures default-disabled apps start as "disabled"
// in .state.json. Apps that already have a state (e.g. user enabled them) are not touched.
func seedDefaultDisabledState(appsDir string) {
	statePath := filepath.Join(appsDir, ".state.json")

	// Load existing state.
	var sf struct {
		States map[string]string `json:"states"`
	}
	if data, err := os.ReadFile(statePath); err == nil {
		json.Unmarshal(data, &sf)
	}
	if sf.States == nil {
		sf.States = make(map[string]string)
	}

	changed := false

	// Remove system apps from marketplace state — they are platform-level,
	// not marketplace-managed. Prevents permission tracking issues.
	for slug := range systemAppSlugs {
		if _, exists := sf.States[slug]; exists {
			delete(sf.States, slug)
			changed = true
			log.Printf("seed-state: removed system app %s from marketplace state", slug)
		}
	}

	for slug := range defaultDisabledApps {
		if _, exists := sf.States[slug]; !exists {
			sf.States[slug] = "disabled"
			changed = true
			log.Printf("seed-state: %s set to disabled (default)", slug)
		}
	}

	if changed {
		data, _ := json.MarshalIndent(sf, "", "  ")
		os.WriteFile(statePath, data, 0o644)
	}
}

// protectedApps are not auto-seeded at startup. They are installed via marketplace
// and locked by the entrypoint to prevent LLM modification.
// protectedApps are not auto-seeded at startup.
var protectedApps = map[string]bool{}

// defaultDisabledApps are seeded but start with state "disabled" in .state.json.
// They appear in the Marketplace but not in the sidebar until the user enables them.
var defaultDisabledApps = map[string]bool{}

// systemApps are platform-level apps shown in the SYSTEM sidebar section.
// They are removed from marketplace state on boot to avoid permission tracking.
var systemAppSlugs = map[string]bool{
	"developer": true,
}

func isProtectedApp(slug string) bool {
	return protectedApps[slug]
}

// bundledAppSlugs returns slugs of all apps bundled in the daemon image.
// These are trusted platform components and get full permissions.
func bundledAppSlugs() []string {
	const defaultsDir = "/opt/alf/defaults/apps"
	entries, err := os.ReadDir(defaultsDir)
	if err != nil {
		return nil
	}
	var slugs []string
	for _, e := range entries {
		if e.IsDir() {
			slugs = append(slugs, e.Name())
		}
	}
	return slugs
}

// seedAppFiles copies files from src to dest, creating directories as needed.
// Skips the data/ subdirectory to preserve user data.
// Preserves execute permission on binaries.
func seedAppFiles(src, dest string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	os.MkdirAll(dest, 0o755)
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dest, e.Name())
		if e.IsDir() {
			if e.Name() == "data" {
				continue // never overwrite user data
			}
			if err := seedAppFiles(s, d); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(s)
			if err != nil {
				return err
			}
			perm := os.FileMode(0o644)
			if info, err := e.Info(); err == nil && info.Mode()&0o111 != 0 {
				perm = 0o755
			}
			// Unlock read-only files before overwriting (marketplace lock).
			os.Chmod(d, 0o644)
			if err := os.WriteFile(d, data, perm); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(s)
			if err != nil {
				return err
			}
			if err := os.WriteFile(d, data, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// setupUserPackagesPaths adds /opt/alf/user-packages/bin to PATH and lib to LD_LIBRARY_PATH.
func setupUserPackagesPaths() {
	const pkgDir = "/opt/alf/user-packages"
	binDir := filepath.Join(pkgDir, "bin")
	libDir := filepath.Join(pkgDir, "lib")
	os.MkdirAll(binDir, 0o755)
	os.MkdirAll(libDir, 0o755)

	path := os.Getenv("PATH")
	if !strings.Contains(path, binDir) {
		os.Setenv("PATH", binDir+":"+path)
	}
	ldPath := os.Getenv("LD_LIBRARY_PATH")
	if !strings.Contains(ldPath, libDir) {
		if ldPath == "" {
			os.Setenv("LD_LIBRARY_PATH", libDir)
		} else {
			os.Setenv("LD_LIBRARY_PATH", libDir+":"+ldPath)
		}
	}
	log.Printf("user-packages: PATH includes %s, LD_LIBRARY_PATH includes %s", binDir, libDir)
}

// injectAppTriggers scans the apps directory and adds each app's name and slug
// as dynamic triggers to the app-builder skill. This way "update the crypto app"
// or "fix reading-list" auto-matches the app-builder skill.
func injectAppTriggers(store skills.Store, appsDir string) {
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return
	}
	var triggers []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		slug := e.Name()
		triggers = append(triggers, slug)
		// Also add human name from app.json if different from slug.
		data, err := os.ReadFile(filepath.Join(appsDir, slug, "app.json"))
		if err == nil {
			var meta struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(data, &meta) == nil && meta.Name != "" && strings.ToLower(meta.Name) != strings.ToLower(slug) {
				triggers = append(triggers, meta.Name)
			}
		}
	}
	if len(triggers) > 0 {
		store.AddDynamicTriggers("sdk-app-builder", triggers)
		log.Printf("skills: injected %d app triggers into sdk-app-builder: %v", len(triggers), triggers)
	}
}
