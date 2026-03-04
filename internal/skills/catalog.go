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
