package tierfs

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	cc "github.com/alamparelli/alf/internal/controlcenter"
)

// TierFS manages per-tier filesystem directories under data/tiers/.
type TierFS struct {
	baseDir string // e.g. /home/node/data/tiers
}

// New creates a TierFS rooted at configDir/tiers.
func New(configDir string) *TierFS {
	base := filepath.Join(configDir, "tiers")
	os.MkdirAll(base, 0o755)
	return &TierFS{baseDir: base}
}

// EnsureDir creates the tier directory structure: tiers/{name}/skills/.
func (t *TierFS) EnsureDir(tierName string) error {
	dir := filepath.Join(t.baseDir, tierName, "skills")
	return os.MkdirAll(dir, 0o755)
}

// RemoveDir removes the entire tier directory. Use with caution.
func (t *TierFS) RemoveDir(tierName string) error {
	return os.RemoveAll(filepath.Join(t.baseDir, tierName))
}

// RenameDir renames a tier directory from old to new.
func (t *TierFS) RenameDir(oldName, newName string) error {
	oldPath := filepath.Join(t.baseDir, oldName)
	newPath := filepath.Join(t.baseDir, newName)
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		return nil // nothing to rename
	}
	return os.Rename(oldPath, newPath)
}

// SystemPrompt reads the system-prompt.md file for a tier.
// Returns empty string if the file doesn't exist.
func (t *TierFS) SystemPrompt(tierName string) string {
	data, err := os.ReadFile(filepath.Join(t.baseDir, tierName, "system-prompt.md"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WriteSystemPrompt writes the system-prompt.md file for a tier.
func (t *TierFS) WriteSystemPrompt(tierName, content string) error {
	dir := filepath.Join(t.baseDir, tierName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create tier dir: %w", err)
	}
	p := filepath.Join(dir, "system-prompt.md")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write system prompt: %w", err)
	}
	return os.Rename(tmp, p)
}

// SkillStore returns a ResourceStore for the skills/ subdirectory of a tier.
func (t *TierFS) SkillStore(tierName string) cc.ResourceStore {
	dir := filepath.Join(t.baseDir, tierName, "skills")
	return cc.NewFileResourceStore(dir, ".md")
}

// CollectPromptArgs returns CLI arguments to inject the tier's system prompt
// and skills as --append-system-prompt flags.
// Order: system-prompt.md first, then skills alphabetically.
func (t *TierFS) CollectPromptArgs(tierName string) []string {
	var args []string

	// System prompt.
	if sp := t.SystemPrompt(tierName); sp != "" {
		block := fmt.Sprintf("=== [tier:%s/system-prompt] ===\n%s", tierName, sp)
		args = append(args, "--append-system-prompt", block)
	}

	// Skills.
	skillDir := filepath.Join(t.baseDir, tierName, "skills")
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		return args
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(skillDir, name))
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		block := fmt.Sprintf("=== [tier:%s/skills/%s] ===\n%s", tierName, name, strings.TrimSpace(string(data)))
		args = append(args, "--append-system-prompt", block)
	}

	return args
}
