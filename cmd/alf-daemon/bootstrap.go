package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	cc "github.com/alamparelli/alf/internal/controlcenter"
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

	// Lock down: tools.d is system-managed, read+execute only.
	os.Chmod(toolsDir, 0o755)
}

// seedDefaultTiers copies /opt/alf/defaults/tiers.json into the config dir
// if no user tiers.json exists yet.
func seedDefaultTiers(configDir string) {
	dest := cc.TiersPath(configDir)
	if _, err := os.Stat(dest); err == nil {
		return // user file already exists
	}
	const defaultPath = "/opt/alf/defaults/tiers.json"
	data, err := os.ReadFile(defaultPath)
	if err != nil {
		log.Printf("seed-tiers: no default at %s: %v", defaultPath, err)
		return
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		log.Printf("seed-tiers: failed to write %s: %v", dest, err)
		return
	}
	log.Printf("seed-tiers: created %s from defaults", dest)
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
		// Extract title from first # heading.
		title := id
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "# ") {
				title = strings.TrimPrefix(strings.TrimSpace(line), "# ")
				break
			}
		}
		b.WriteString(fmt.Sprintf("- %s: %s\n", id, title))
	}

	// Write docs to filesystem so LLM can read them (read-only).
	docsDir := filepath.Join(dataDir, "docs")
	os.MkdirAll(docsDir, 0o555)
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

// seedHeartbeatFile creates a default context/heartbeat.md if it doesn't exist.
func seedHeartbeatFile(contextDir string) {
	hbPath := filepath.Join(contextDir, "heartbeat.md")
	if _, err := os.Stat(hbPath); err == nil {
		return // already exists
	}
	os.MkdirAll(contextDir, 0o755)
	content := `---
tier: haiku
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

// seedBundledSkills copies missing skill directories from /opt/alf/defaults/skills.d
// into the active skills directory. Existing skills are never overwritten.
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
		dest := filepath.Join(skillsDir, e.Name())
		if _, err := os.Stat(dest); err == nil {
			continue // skill already exists
		}
		// Copy entire skill directory.
		src := filepath.Join(defaultsDir, e.Name())
		if err := copyDir(src, dest); err != nil {
			log.Printf("seed skill %s: %v", e.Name(), err)
		} else {
			log.Printf("seeded bundled skill: %s", e.Name())
		}
	}
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
