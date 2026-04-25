// Skill loader (Étape 4 of #389). LoadDir walks the configured skill
// directories in order, prepares each skill's signed bundle, hands it
// to the runtime.Instantiator-backed InstantiateFn, and returns the
// verified skills plus the per-skill errors that didn't abort the load.
//
// This file does NOT touch the legacy parseSkill / Reload code path.
// It coexists during the transition window: the daemon will switch
// from the YAML-frontmatter MirrorInto path to LoadDir in Étape 8,
// and only at that point does the unsigned skill become a load-time
// rejection. Until then, both paths run side-by-side.
package skills

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/alamparelli/alf/internal/capability/envelope"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// InstantiateFn is the narrow callback the loader uses to drive
// runtime.Instantiator.InstantiateVerified without importing the
// runtime package (the dependency would be circular: runtime already
// imports skills via SkillStore on the engine).
//
// The daemon adapter is two lines:
//
//	func(ctx context.Context, in envelope.VerifyInput, baseDir string) (*handle.Instance, *envelope.Manifest, error) {
//	    vi, err := instantiator.InstantiateVerified(ctx, in, baseDir)
//	    if err != nil { return nil, nil, err }
//	    return vi.Instance, vi.Manifest, nil
//	}
type InstantiateFn func(ctx context.Context, in envelope.VerifyInput, baseDir string) (*handle.Instance, *envelope.Manifest, error)

// LoadOptions bundles the inputs LoadDir needs. Production callers
// populate every field; tests pass partial structs and a stub
// Instantiate.
type LoadOptions struct {
	// Dirs is the ordered list of skill roots. Later entries override
	// earlier ones by manifest.id — the canonical "shipped < user"
	// ordering for the 5 shipped skills + any user copy under
	// <dataDir>/skills.d/.
	Dirs []string

	// TrustStore is the authority on which signing keys are accepted
	// at envelope.Verify time. The release key (for shipped skills) +
	// the local daemon key (for auto-signed user skills) are typical
	// entries.
	TrustStore envelope.TrustStore

	// AutoSign optionally signs unsigned skills with the daemon key
	// (§7.3 Tier 2 — same convention as the WASM Loader). When nil,
	// unsigned skills are rejected.
	AutoSign AutoSigner

	// Instantiate must call runtime.Instantiator.InstantiateVerified
	// (or an equivalent that funnels through it). The #388 archtest
	// pins envelope.Verify's only call site to that method.
	Instantiate InstantiateFn

	// Logger receives boot-time observability lines. When nil, log
	// is used (compatibility with how parseSkill emits today).
	Logger func(format string, args ...any)

	// Now is injected so tests can pin auto-sign timestamps.
	Now func() time.Time
}

// VerifiedSkill is the success product of LoadDir for one skill. It
// carries every artefact downstream consumers need: the parsed prompt
// body (legacy SKILL.md side — triggers + tier still come from there),
// the forged ocap Instance, and the verified envelope manifest.
type VerifiedSkill struct {
	Skill    *Skill
	Instance *handle.Instance
	Manifest *envelope.Manifest
}

// LoadDir runs the verified-load pipeline. It is intentionally
// idempotent against repeated invocations: each call produces fresh
// Instances. Callers reloading state must Close() the previous batch
// to revoke the prior handles.
//
// Per-skill failures are accumulated in the second return slice, never
// abort the whole load — one bad skill must not prevent others from
// loading, mirroring the WASM loader's behaviour.
//
// Override semantics: subdirs found under later opts.Dirs entries
// shadow earlier same-id entries. The shadowed Instance is Close()d
// before LoadDir returns so handles forged from the obsolete copy do
// not leak.
func LoadDir(ctx context.Context, opts LoadOptions) ([]*VerifiedSkill, []error) {
	if opts.Logger == nil {
		opts.Logger = func(format string, args ...any) { log.Printf(format, args...) }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Instantiate == nil {
		return nil, []error{fmt.Errorf("skills: LoadDir requires a non-nil Instantiate")}
	}

	loaded := make(map[string]*VerifiedSkill) // by manifest.id
	loadedSrc := make(map[string]string)      // id → on-disk dir, for override logs
	var errs []error

	for _, root := range opts.Dirs {
		entries, err := os.ReadDir(root)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Empty / nonexistent dir is normal (fresh install,
				// no user copy). Other dirs may still hold skills.
				continue
			}
			errs = append(errs, fmt.Errorf("readdir %s: %w", root, err))
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			skillDir := filepath.Join(root, e.Name())

			bundle, err := prepareSkillBundle(skillDir, loadOptions{
				TrustStore: opts.TrustStore,
				AutoSign:   opts.AutoSign,
				Now:        opts.Now,
				Logger:     opts.Logger,
			})
			if err != nil {
				if errors.Is(err, errSkillManifestNotFound) {
					// Legacy YAML-only skill — silently skipped here.
					// The legacy MirrorInto path still picks it up
					// during the transition window. Étape 8 closes
					// this gap.
					continue
				}
				errs = append(errs, fmt.Errorf("%s: %w", skillDir, err))
				continue
			}

			inst, manifest, err := opts.Instantiate(ctx, bundle.VerifyInput, skillDir)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: instantiate: %w", skillDir, err))
				continue
			}

			// Parse SKILL.md for prompt body + discovery metadata
			// (triggers, tier). The manifest id is the source of
			// truth for the registered name; we override the legacy
			// Name field accordingly so consumers index by manifest.id.
			parsed, err := parseSkill(skillDir)
			if err != nil {
				inst.Close()
				errs = append(errs, fmt.Errorf("%s: parse: %w", skillDir, err))
				continue
			}
			parsed.Name = manifest.ID

			// Override detection: same id from a later dir wins.
			if prev, ok := loaded[manifest.ID]; ok {
				opts.Logger("[skills] %s overridden by %s (original: %s)",
					manifest.ID, skillDir, loadedSrc[manifest.ID])
				prev.Instance.Close()
			}

			loaded[manifest.ID] = &VerifiedSkill{
				Skill:    parsed,
				Instance: inst,
				Manifest: manifest,
			}
			loadedSrc[manifest.ID] = skillDir
		}
	}

	out := make([]*VerifiedSkill, 0, len(loaded))
	for _, vs := range loaded {
		out = append(out, vs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.ID < out[j].Manifest.ID })
	return out, errs
}

// CloseAll revokes every skill in the slice. Used by the daemon when
// re-running LoadDir on a reload — the old batch's handles must die
// before the new batch goes live.
func CloseAll(skills []*VerifiedSkill) {
	for _, vs := range skills {
		if vs != nil && vs.Instance != nil {
			vs.Instance.Close()
		}
	}
}
