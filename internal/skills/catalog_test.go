package skills

import (
	"strings"
	"testing"
)

func TestBuildCatalog_Empty(t *testing.T) {
	store := NewFileSkillStore("/nonexistent", "/nonexistent")
	result := BuildCatalog(store)
	if result != "" {
		t.Errorf("expected empty, got %q", result)
	}
}

func TestBuildCatalog_Multiple(t *testing.T) {
	sysDir := t.TempDir()
	writeSkill(t, sysDir, "coding", `---
description: Expert coding
---
Prompt.
`)
	writeSkill(t, sysDir, "memory-expert", `---
description: Memory management
---
Prompt.
`)

	store := NewFileSkillStore(sysDir, "")
	result := BuildCatalog(store)

	if !strings.Contains(result, "=== [Available Skills] ===") {
		t.Error("missing header")
	}
	if !strings.Contains(result, "- coding: Expert coding") {
		t.Error("missing coding skill")
	}
	if !strings.Contains(result, "- memory-expert: Memory management") {
		t.Error("missing memory-expert skill")
	}
	if !strings.Contains(result, "To use a skill, read its SKILL.md") {
		t.Error("missing footer instruction")
	}
}

func TestContainsWord(t *testing.T) {
	tests := []struct {
		haystack string
		needle   string
		want     bool
	}{
		{"schedule a post on x", "post", true},
		{"les titres des posts", "post", false},    // "posts" != "post"
		{"draft a tweet", "tweet", true},
		{"tweeted yesterday", "tweet", false},       // "tweeted" != "tweet"
		{"post this on x", "x", true},               // "x" at end
		{"x is a platform", "x", true},              // "x" at start
		{"fix the css", "x", false},                 // "x" inside "fix"
		{"check seo page", "seo", true},
		{"x post ready", "x post", true},            // multi-word trigger
		{"export data", "x post", false},
		{"", "post", false},
		{"post", "post", true},
		{"re-post it", "post", true},                // hyphen is boundary
		{"post123", "post", false},                   // digit not boundary
	}
	for _, tt := range tests {
		got := containsWord(tt.haystack, tt.needle)
		if got != tt.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v", tt.haystack, tt.needle, got, tt.want)
		}
	}
}

func TestMatchTriggers_WordBoundary(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "x-manager", `---
description: X content manager
triggers: tweet, draft, x post
---
Prompt.
`)

	store := NewFileSkillStore(dir, "")

	// Should NOT match "posts" or "fix"
	matched := MatchTriggers(store, "les titres des posts")
	if len(matched) != 0 {
		t.Errorf("expected no match for 'posts', got %v", matched[0].Name)
	}

	// Should match exact "tweet"
	matched = MatchTriggers(store, "draft a tweet for me")
	if len(matched) != 1 || matched[0].Name != "x-manager" {
		t.Error("expected match for 'tweet'")
	}

	// Should match "x post" as phrase
	matched = MatchTriggers(store, "schedule an x post")
	if len(matched) != 1 {
		t.Error("expected match for 'x post'")
	}
}

func TestBuildCatalog_SkipsEmptyDescription(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "no-desc", "Just a body, no frontmatter.")

	store := NewFileSkillStore(dir, "")
	result := BuildCatalog(store)
	if result != "" {
		t.Errorf("expected empty catalog for skill without description, got %q", result)
	}
}
