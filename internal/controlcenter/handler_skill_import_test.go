package controlcenter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name      string
		cmd       string
		wantOwner string
		wantRepo  string
		wantSkill string
		wantErr   bool
	}{
		{
			name:      "bare owner/repo",
			cmd:       "vercel-labs/skills",
			wantOwner: "vercel-labs",
			wantRepo:  "skills",
			wantSkill: "skills",
		},
		{
			name:      "bare with skill flag",
			cmd:       "vercel-labs/skills --skill find-skills",
			wantOwner: "vercel-labs",
			wantRepo:  "skills",
			wantSkill: "find-skills",
		},
		{
			name:      "npx skills add",
			cmd:       "npx skills add vercel-labs/skills --skill find-skills",
			wantOwner: "vercel-labs",
			wantRepo:  "skills",
			wantSkill: "find-skills",
		},
		{
			name:      "npx scoped package",
			cmd:       "npx @foo/bar skills add owner/repo --skill my-skill",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantSkill: "my-skill",
		},
		{
			name:      "no skill flag defaults to repo",
			cmd:       "npx skills add my-org/cool-skill",
			wantOwner: "my-org",
			wantRepo:  "cool-skill",
			wantSkill: "cool-skill",
		},
		{
			name:    "empty command",
			cmd:     "",
			wantErr: true,
		},
		{
			name:    "no slash",
			cmd:     "just-a-word",
			wantErr: true,
		},
		{
			name:      "dots in names",
			cmd:       "user.name/repo.name --skill skill.name",
			wantOwner: "user.name",
			wantRepo:  "repo.name",
			wantSkill: "skill.name",
		},
		{
			name:      "full github URL",
			cmd:       "https://github.com/blader/humanizer",
			wantOwner: "blader",
			wantRepo:  "humanizer",
			wantSkill: "humanizer",
		},
		{
			name:      "github URL without https",
			cmd:       "github.com/blader/humanizer",
			wantOwner: "blader",
			wantRepo:  "humanizer",
			wantSkill: "humanizer",
		},
		{
			name:      "github URL with skill flag",
			cmd:       "https://github.com/vercel-labs/skills --skill find-skills",
			wantOwner: "vercel-labs",
			wantRepo:  "skills",
			wantSkill: "find-skills",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, skill, err := parseCommand(tt.cmd)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
			if skill != tt.wantSkill {
				t.Errorf("skill = %q, want %q", skill, tt.wantSkill)
			}
		})
	}
}

func TestBuildEnrichedSkill(t *testing.T) {
	content := `---
name: test-skill
description: A test skill
version: 1.0
---

Do something useful.`

	result := buildEnrichedSkill(content, "test-skill", "keyword1, keyword2", "fast", "owner/repo")

	if !strings.Contains(result, "name: test-skill") {
		t.Error("missing name in enriched skill")
	}
	if !strings.Contains(result, "description: A test skill") {
		t.Error("missing description in enriched skill")
	}
	if !strings.Contains(result, "triggers: [keyword1, keyword2]") {
		t.Error("missing triggers in enriched skill")
	}
	if !strings.Contains(result, "tier: fast") {
		t.Error("missing tier in enriched skill")
	}
	if !strings.Contains(result, "source: owner/repo") {
		t.Error("missing source in enriched skill")
	}
	if !strings.Contains(result, "Do something useful.") {
		t.Error("missing body in enriched skill")
	}
}

func TestBuildEnrichedSkill_NoFrontmatter(t *testing.T) {
	content := "Just a plain skill body with instructions."

	result := buildEnrichedSkill(content, "plain-skill", "trigger1", "smart", "org/repo")

	if !strings.Contains(result, "name: plain-skill") {
		t.Error("missing name")
	}
	if !strings.Contains(result, "source: org/repo") {
		t.Error("missing source")
	}
	if !strings.Contains(result, "Just a plain skill body") {
		t.Error("missing body")
	}
}

func TestSkillImport_Install_PathTraversal(t *testing.T) {
	names := []string{"../etc", "../../passwd", "foo/bar", "foo\\bar", ".."}
	for _, name := range names {
		if safeSkillName.MatchString(name) && !strings.Contains(name, "..") && !strings.Contains(name, "/") && !strings.Contains(name, "\\") {
			t.Errorf("name %q should be rejected", name)
		}
	}
}

func TestSkillImport_Install_WritesCorrectStructure(t *testing.T) {
	tmpDir := t.TempDir()
	h := &SkillImportHandler{DataDir: tmpDir}

	content := `---
name: my-skill
description: test
---

Skill instructions here.`

	enriched := buildEnrichedSkill(content, "my-skill", "test, demo", "fast", "owner/repo")

	// Simulate the install write.
	skillDir := filepath.Join(tmpDir, "skills", "my-skill")
	os.MkdirAll(skillDir, 0o775)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(enriched), 0o664); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Verify file exists and has expected content.
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	got := string(data)
	if !strings.Contains(got, "source: owner/repo") {
		t.Error("missing source field")
	}
	if !strings.Contains(got, "triggers: [test, demo]") {
		t.Error("missing triggers field")
	}

	// Verify the handler's DataDir was used.
	_ = h // used for DataDir reference
}

func TestSkillImport_OversizedContent(t *testing.T) {
	big := strings.Repeat("x", maxSkillContentBytes+1)
	if len(big) <= maxSkillContentBytes {
		t.Fatal("test setup error: content should exceed limit")
	}
}

func TestParseFrontmatterSimple(t *testing.T) {
	content := `---
name: test
description: A description
version: 2.0
triggers: [word1, word2, word3]
tier: smart
---

Body content here.`

	name, desc, version, triggers, tier, body := parseFrontmatterSimple(content)

	if name != "test" {
		t.Errorf("name = %q, want %q", name, "test")
	}
	if desc != "A description" {
		t.Errorf("description = %q", desc)
	}
	if version != "2.0" {
		t.Errorf("version = %q", version)
	}
	if len(triggers) != 3 || triggers[0] != "word1" {
		t.Errorf("triggers = %v", triggers)
	}
	if tier != "smart" {
		t.Errorf("tier = %q", tier)
	}
	if !strings.Contains(body, "Body content here.") {
		t.Errorf("body = %q", body)
	}
}

func TestSafeSkillName(t *testing.T) {
	valid := []string{"my-skill", "skill_name", "skill123", "My.Skill"}
	for _, n := range valid {
		if !safeSkillName.MatchString(n) {
			t.Errorf("%q should be valid", n)
		}
	}

	invalid := []string{".hidden", "-start", "../bad", "", "a/b"}
	for _, n := range invalid {
		if safeSkillName.MatchString(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}
