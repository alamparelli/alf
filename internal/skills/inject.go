package skills

import (
	"fmt"
	"log"
	"strings"
)

// BuildInjectionBlock resolves skill names and returns their full
// flattened prompts for non-interactive contexts (scheduler).
// Returns "" if names is empty or no skills found.
func BuildInjectionBlock(store Store, names []string) string {
	if len(names) == 0 {
		return ""
	}

	var blocks []string
	for _, name := range names {
		sk, ok := store.Get(name)
		if !ok {
			log.Printf("skills: injection requested for unknown skill %q, skipping", name)
			continue
		}
		if sk.Prompt == "" {
			continue
		}
		blocks = append(blocks, fmt.Sprintf("--- %s ---\n%s", sk.Name, sk.Prompt))
	}

	if len(blocks) == 0 {
		return ""
	}
	return strings.Join(blocks, "\n\n")
}
