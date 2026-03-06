package skills

import (
	"fmt"
	"strings"
)

// BuildCatalog returns a system prompt block listing all skills with
// their descriptions and directory paths. Returns "" if no skills loaded.
func BuildCatalog(store Store) string {
	all := store.All()
	if len(all) == 0 {
		return ""
	}

	var entries []string
	for _, sk := range all {
		if sk.Description == "" {
			continue // no description = not discoverable
		}
		entries = append(entries, fmt.Sprintf("- %s: %s\n  → %s/", sk.Name, sk.Description, sk.Dir))
	}
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("=== [Available Skills] ===\n")
	for _, e := range entries {
		sb.WriteString(e)
		sb.WriteByte('\n')
	}
	sb.WriteString("\nTo use a skill, read its SKILL.md file for detailed instructions.")

	return sb.String()
}

// MatchTriggers returns skills whose trigger keywords appear in the message.
// Matching is case-insensitive, word-boundary aware.
func MatchTriggers(store Store, message string) []*Skill {
	msg := strings.ToLower(message)

	var matched []*Skill
	seen := make(map[string]bool)
	for _, sk := range store.All() {
		if len(sk.Triggers) == 0 || seen[sk.Name] {
			continue
		}
		for _, t := range sk.Triggers {
			if strings.Contains(msg, t) {
				matched = append(matched, sk)
				seen[sk.Name] = true
				break
			}
		}
	}
	return matched
}

// BuildInjection returns a system prompt block with the full prompt content
// of the given skills, ready to inject. Returns "" if skills is empty.
func BuildInjection(matched []*Skill) string {
	if len(matched) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("=== [Auto-Loaded Skills] ===\n")
	sb.WriteString("The following skills were auto-loaded based on your message. Follow their instructions.\n\n")
	for _, sk := range matched {
		sb.WriteString(fmt.Sprintf("--- skill: %s ---\n", sk.Name))
		sb.WriteString(sk.Prompt)
		sb.WriteString("\n\n")
	}
	return sb.String()
}
