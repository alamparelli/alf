package marketplace

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression guard for #385-6: linkAppSkills must only symlink skill
// directories whose name matches validSkillName ([a-zA-Z0-9_-]+).
// Bundle extraction blocks traversal prefixes, but a weird-unicode or
// shell-metachar name would still reach the LLM / UI layer.

func TestLinkAppSkills_RejectsInvalidNames(t *testing.T) {
	base := t.TempDir()
	slug := "sample"
	skillsSrc := filepath.Join(base, "apps", slug, "skills")

	dirs := []string{
		"legit",         // ok
		"also-fine_42",  // ok (hyphen, underscore, digit)
		"with space",    // invalid (space)
		".hidden",       // invalid (leading dot)
		"dot.name",      // invalid (dot)
		"weird$(evil)",  // invalid (shell metachars)
		"accenté", // invalid (non-ASCII)
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(skillsSrc, d), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", d, err)
		}
	}

	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prev)

	m := &Manager{dataDir: base}
	m.linkAppSkills(slug)

	skillsDst := filepath.Join(base, "skills")

	// Valid names must have a symlink.
	for _, name := range []string{"legit", "also-fine_42"} {
		_, err := os.Lstat(filepath.Join(skillsDst, name))
		if err != nil {
			t.Errorf("expected symlink for %q to exist: %v", name, err)
		}
	}

	// Invalid names must NOT have been created.
	for _, name := range []string{"with space", ".hidden", "dot.name", "weird$(evil)", "accenté"} {
		_, err := os.Lstat(filepath.Join(skillsDst, name))
		if err == nil {
			t.Errorf("symlink for invalid name %q should not exist", name)
		}
	}

	// Each rejection should have logged a warning mentioning the app slug.
	if !strings.Contains(logs.String(), slug) {
		t.Errorf("log output missing slug marker %q: %s", slug, logs.String())
	}
	if !strings.Contains(logs.String(), "invalid name") {
		t.Errorf("log output missing rejection reason: %s", logs.String())
	}
}

func TestUnlinkAppSkills_IgnoresInvalidNames(t *testing.T) {
	// Pre-condition: an invalid-named *file* sits at the skills/<invalid>
	// path (not created by us — maybe left over from a previous install,
	// or planted by a separate actor). unlinkAppSkills MUST NOT remove it
	// just because the bundle directory happens to share that name.
	base := t.TempDir()
	slug := "sample"
	skillsSrc := filepath.Join(base, "apps", slug, "skills")
	skillsDst := filepath.Join(base, "skills")

	if err := os.MkdirAll(filepath.Join(skillsSrc, "with space"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillsDst, 0o755); err != nil {
		t.Fatal(err)
	}
	// Plant a real file at the target path that unlinkAppSkills must leave alone.
	target := filepath.Join(skillsDst, "with space")
	if err := os.WriteFile(target, []byte("do not delete me"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &Manager{dataDir: base}
	m.unlinkAppSkills(slug)

	if _, err := os.Stat(target); err != nil {
		t.Fatalf("unlinkAppSkills removed a file at an invalid-name path: %v", err)
	}
}

func TestValidSkillName(t *testing.T) {
	ok := []string{"a", "A", "0", "foo", "foo-bar", "foo_bar", "Foo-Bar_42"}
	bad := []string{"", "with space", ".dot", "dot.foo", "foo$", "foo/bar", "..", "é"}
	for _, s := range ok {
		if !validSkillName.MatchString(s) {
			t.Errorf("expected %q to be valid", s)
		}
	}
	for _, s := range bad {
		if validSkillName.MatchString(s) {
			t.Errorf("expected %q to be rejected", s)
		}
	}
}
