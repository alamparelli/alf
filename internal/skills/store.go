package skills

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
)

const promptSoftLimit = 8 * 1024 // 8KB

// fileSkillStore loads skills from multiple directories.
// Later directories override earlier ones by skill name.
type fileSkillStore struct {
	dirs    []string
	current atomic.Pointer[map[string]*Skill]
}

// NewFileSkillStore creates a Store backed by directories loaded in order.
// Later directories override earlier ones by skill name.
// Any directory may be empty or nonexistent.
func NewFileSkillStore(dirs ...string) Store {
	s := &fileSkillStore{dirs: dirs}
	empty := make(map[string]*Skill)
	s.current.Store(&empty)
	_ = s.Reload()
	return s
}

func (s *fileSkillStore) All() []*Skill {
	m := s.current.Load()
	out := make([]*Skill, 0, len(*m))
	for _, sk := range *m {
		out = append(out, sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *fileSkillStore) Get(name string) (*Skill, bool) {
	m := s.current.Load()
	sk, ok := (*m)[name]
	return sk, ok
}

func (s *fileSkillStore) Reload() error {
	merged := make(map[string]*Skill)

	// Load directories in order; later dirs override earlier by name.
	for _, dir := range s.dirs {
		for k, v := range loadDir(dir) {
			merged[k] = v
		}
	}

	s.current.Store(&merged)
	if len(merged) > 0 {
		log.Printf("skills: loaded %d skills from %v", len(merged), s.dirs)
	}
	return nil
}

func (s *fileSkillStore) AddDynamicTriggers(skillName string, triggers []string) {
	m := s.current.Load()
	sk, ok := (*m)[skillName]
	if !ok || len(triggers) == 0 {
		return
	}
	// Deduplicate: only add triggers not already present.
	existing := make(map[string]bool)
	for _, t := range sk.Triggers {
		existing[strings.ToLower(t)] = true
	}
	for _, t := range triggers {
		if t != "" && !existing[strings.ToLower(t)] {
			sk.Triggers = append(sk.Triggers, t)
			existing[strings.ToLower(t)] = true
		}
	}
}

// loadDir scans a directory for skill subdirectories containing SKILL.md.
func loadDir(dir string) map[string]*Skill {
	out := make(map[string]*Skill)
	if dir == "" {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("skills: cannot read %s: %v", dir, err)
		}
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillDir := filepath.Join(dir, e.Name())
		sk, err := parseSkill(skillDir)
		if err != nil {
			log.Printf("skills: skip %s: %v", skillDir, err)
			continue
		}
		out[sk.Name] = sk
	}
	return out
}

// parseSkill reads SKILL.md from skillDir, extracts frontmatter and body,
// then flattens any additional .md files as reference material.
func parseSkill(skillDir string) (*Skill, error) {
	skillPath := filepath.Join(skillDir, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, err
	}

	name, description, version, triggers, tier, body := parseFrontmatter(string(data))

	// Default name from directory name.
	if name == "" {
		name = filepath.Base(skillDir)
	}

	// Auto-add skill name as a trigger if not already present.
	{
		found := false
		low := strings.ToLower(name)
		for _, t := range triggers {
			if strings.ToLower(t) == low {
				found = true
				break
			}
		}
		if !found {
			triggers = append(triggers, name)
		}
	}

	// Flatten additional .md reference files.
	var refs []string
	entries, _ := os.ReadDir(skillDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || e.Name() == "SKILL.md" {
			continue
		}
		refData, err := os.ReadFile(filepath.Join(skillDir, e.Name()))
		if err != nil || len(refData) == 0 {
			continue
		}
		refs = append(refs, string(refData))
	}

	prompt := body
	if len(refs) > 0 {
		prompt = body + "\n\n" + strings.Join(refs, "\n\n")
	}

	if len(prompt) > promptSoftLimit {
		log.Printf("skills: %s prompt is %dB (soft limit %dB)", name, len(prompt), promptSoftLimit)
	}

	return &Skill{
		Name:        name,
		Description: description,
		Version:     version,
		Triggers:    triggers,
		Tier:        tier,
		Prompt:      prompt,
		Dir:         skillDir,
	}, nil
}

// parseFrontmatter extracts YAML-like frontmatter from a SKILL.md file.
// Supports:
//
//	---
//	name: value
//	description: value
//	version: value
//	---
//	body content
func parseFrontmatter(content string) (name, description, version string, triggers []string, tier string, body string) {
	content = strings.TrimLeft(content, "\xef\xbb\xbf") // strip BOM
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		return "", "", "", nil, "", strings.TrimSpace(content)
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	// Skip first ---
	scanner.Scan()
	var frontLines []string
	foundEnd := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			foundEnd = true
			break
		}
		frontLines = append(frontLines, line)
	}
	if !foundEnd {
		// No closing ---, treat entire content as body.
		return "", "", "", nil, "", strings.TrimSpace(content)
	}

	// Parse simple key: value pairs from frontmatter.
	for _, line := range frontLines {
		key, val, ok := parseKV(line)
		if !ok {
			continue
		}
		switch key {
		case "name":
			name = val
		case "description":
			description = val
		case "version":
			version = val
		case "triggers":
			triggers = parseList(val)
		case "tier":
			tier = val
		}
	}

	// Rest is body.
	var bodyLines []string
	for scanner.Scan() {
		bodyLines = append(bodyLines, scanner.Text())
	}
	body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
	return
}

// parseList splits a comma-separated or YAML-style list value.
// Supports: "a, b, c" or "[a, b, c]".
func parseList(val string) []string {
	val = strings.TrimSpace(val)
	val = strings.TrimPrefix(val, "[")
	val = strings.TrimSuffix(val, "]")
	var out []string
	for _, s := range strings.Split(val, ",") {
		s = strings.TrimSpace(s)
		// Strip quotes.
		if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
			s = s[1 : len(s)-1]
		}
		if s != "" {
			out = append(out, strings.ToLower(s))
		}
	}
	return out
}

// parseKV extracts "key: value" from a line, stripping quotes.
func parseKV(line string) (string, string, bool) {
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	val := strings.TrimSpace(line[idx+1:])
	// Strip surrounding quotes.
	if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
		val = val[1 : len(val)-1]
	}
	return key, val, key != ""
}
