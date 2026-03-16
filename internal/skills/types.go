package skills

// Skill represents a parsed SKILL.md with its metadata and prompt body.
type Skill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version,omitempty"`
	Triggers    []string `json:"triggers,omitempty"` // keywords that auto-inject this skill
	Tier        string   `json:"tier,omitempty"`     // minimum tier name when this skill is active
	Prompt      string   `json:"-"`                  // flattened body (SKILL.md + refs), not serialized
	Dir         string   `json:"-"`                  // absolute path to skill directory
}

// Store provides read access to the loaded skill catalog.
type Store interface {
	All() []*Skill
	Get(name string) (*Skill, bool)
	Reload() error
	// AddDynamicTriggers appends extra triggers to a skill at runtime.
	// Used to inject app names so "update crypto app" matches app-builder.
	AddDynamicTriggers(skillName string, triggers []string)
}
