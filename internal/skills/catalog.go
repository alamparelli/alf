package skills

import (
	"fmt"
	"strings"
	"unicode"
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
	sb.WriteString("Before saying you cannot do something, check if a skill below handles it.\n")
	for _, e := range entries {
		sb.WriteString(e)
		sb.WriteByte('\n')
	}
	sb.WriteString("\nTo use a skill, read its SKILL.md file for detailed instructions.")

	return sb.String()
}

// MatchTriggers returns skills whose trigger keywords appear in the message.
// Matching is case-insensitive. Single-word triggers use word-boundary matching.
// Multi-word triggers match if ALL words appear in the message (not necessarily adjacent),
// so "create app" matches "create an app" or "create a todo app".
func MatchTriggers(store Store, message string) []*Skill {
	msg := strings.ToLower(message)

	var matched []*Skill
	seen := make(map[string]bool)
	for _, sk := range store.All() {
		if len(sk.Triggers) == 0 || seen[sk.Name] {
			continue
		}
		for _, t := range sk.Triggers {
			if matchTrigger(msg, strings.ToLower(t)) {
				matched = append(matched, sk)
				seen[sk.Name] = true
				break
			}
		}
	}
	return matched
}

// matchTrigger checks if a trigger matches the message.
// Single-word triggers use exact word-boundary matching.
// Multi-word triggers match if ALL words appear in the message.
func matchTrigger(msg, trigger string) bool {
	words := strings.Fields(trigger)
	if len(words) <= 1 {
		return containsWord(msg, trigger)
	}
	// Multi-word: all words must appear (word-boundary aware).
	for _, w := range words {
		if !containsWord(msg, w) {
			return false
		}
	}
	return true
}

// containsWord checks if needle appears in haystack as a whole word/phrase,
// i.e. surrounded by non-alphanumeric characters or string boundaries.
func containsWord(haystack, needle string) bool {
	for i := 0; ; {
		idx := strings.Index(haystack[i:], needle)
		if idx < 0 {
			return false
		}
		start := i + idx
		end := start + len(needle)

		leftOK := start == 0 || !unicode.IsLetter(rune(haystack[start-1])) && !unicode.IsDigit(rune(haystack[start-1]))
		rightOK := end == len(haystack) || !unicode.IsLetter(rune(haystack[end])) && !unicode.IsDigit(rune(haystack[end]))

		if leftOK && rightOK {
			return true
		}
		i = start + 1
	}
}

// BuildInjection returns a system prompt block that tells Claude to read
// the matched skills. Injects metadata + path, NOT the full prompt.
// This keeps context small and forces Claude to actively engage with the skill.
func BuildInjection(matched []*Skill) string {
	if len(matched) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("=== [ACTIVE SKILLS - YOU MUST USE THESE] ===\n")
	sb.WriteString("The following skills are active for this conversation.\n")
	sb.WriteString("MANDATORY: Read each skill's SKILL.md file NOW and follow its instructions.\n")
	sb.WriteString("Do NOT claim you cannot do something if an active skill provides the capability.\n\n")
	for _, sk := range matched {
		desc := sk.Description
		if desc == "" {
			desc = "(no description)"
		}
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n  Read: %s/SKILL.md\n", sk.Name, desc, sk.Dir))
	}
	sb.WriteString("\nYou MUST read these SKILL.md files before responding to skill-related requests.\n")
	return sb.String()
}

// ResolveMinTier returns the tier name required by active skills, or "" if none.
// When multiple skills specify a tier, returns the first non-empty one found.
func ResolveMinTier(store Store, names []string) string {
	for _, name := range names {
		if sk, ok := store.Get(name); ok && sk.Tier != "" {
			return sk.Tier
		}
	}
	return ""
}

// BuildInjectionByName returns an injection block for skills looked up by name.
// Used for session-persisted skills that were triggered in earlier messages.
func BuildInjectionByName(store Store, names []string) string {
	if len(names) == 0 {
		return ""
	}
	var matched []*Skill
	for _, name := range names {
		if sk, ok := store.Get(name); ok {
			matched = append(matched, sk)
		}
	}
	return BuildInjection(matched)
}
