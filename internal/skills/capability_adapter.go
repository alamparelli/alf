package skills

import (
	"context"

	"github.com/alamparelli/alf/internal/capability"
)

// skillCapability adapts a Skill to the capability.Capability contract.
// Kind=KindSkill. Execute returns the skill's flattened prompt body —
// it is up to the Runtime (Step 4) to orchestrate LLM turns that
// consume that prompt; a Skill itself never calls tools or other
// capabilities directly (ARCHITECTURE-v0.7.10 §2.1 hard rule).
//
// This type lives in skills/ (not capability/) to preserve the
// capability ← skills dependency edge.
type skillCapability struct {
	skill *Skill
}

// asCapability wraps a Skill. Returned as capability.Capability so
// external code goes through the unified Registry.
func asCapability(sk *Skill) capability.Capability {
	return skillCapability{skill: sk}
}

func (s skillCapability) Manifest() capability.Manifest {
	return capability.Manifest{
		ID:          capability.ID(s.skill.Name),
		Kind:        capability.KindSkill,
		Name:        s.skill.Name,
		Version:     s.skill.Version,
		Description: s.skill.Description,
	}
}

func (s skillCapability) Permissions() capability.PermissionSet {
	return capability.PermissionSet{}
}

// Execute surfaces the skill's prompt body as Output.Data. Runtime
// takes that string and composes the actual LLM turn (tool choice,
// message assembly, etc.) — the Skill itself has no side effects.
func (s skillCapability) Execute(_ context.Context, _ capability.Input) (capability.Output, error) {
	return capability.Output{Data: s.skill.Prompt}, nil
}

// MirrorInto registers every current skill into reg as a KindSkill
// Capability. Idempotent: existing entries with the same ID are
// replaced, so it is safe to call after every Reload().
//
// Skills without a Description are skipped (they are not discoverable
// by the LLM; BuildCatalog already filters them out).
func MirrorInto(store Store, reg *capability.Registry) error {
	if store == nil || reg == nil {
		return nil
	}
	for _, sk := range store.All() {
		if sk.Description == "" {
			continue
		}
		if err := reg.Replace(asCapability(sk)); err != nil {
			return err
		}
	}
	return nil
}
