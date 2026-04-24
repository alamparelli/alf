package sandbox

import (
	"context"
	"reflect"
	"testing"
)

func TestDerive_ProjectsManifestView(t *testing.T) {
	sb := New()
	mv := ManifestView{
		ID: "xpost",
		Permissions: PermissionsView{
			FilePaths: []string{"/tmp/xpost/**"},
			Networks:  []string{"api.twitter.com"},
			Secrets:   []string{"xpost/*"},
		},
	}

	p, err := sb.Derive(mv, "pro")
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if !reflect.DeepEqual(p.FileAccess.ReadPaths, mv.Permissions.FilePaths) {
		t.Errorf("ReadPaths mismatch: %v vs %v", p.FileAccess.ReadPaths, mv.Permissions.FilePaths)
	}
	if !reflect.DeepEqual(p.Network.AllowedDomains, mv.Permissions.Networks) {
		t.Errorf("AllowedDomains mismatch: %v vs %v", p.Network.AllowedDomains, mv.Permissions.Networks)
	}
	if !reflect.DeepEqual(p.Secrets.KeyPatterns, mv.Permissions.Secrets) {
		t.Errorf("KeyPatterns mismatch: %v vs %v", p.Secrets.KeyPatterns, mv.Permissions.Secrets)
	}
	if p.Tier != "pro" {
		t.Errorf("Tier = %q, want pro", p.Tier)
	}
}

func TestDerive_Deterministic(t *testing.T) {
	// Same (m, tier) → same Policy, always. Mutation would break the
	// "Policy derived from Manifest + tier, never ad-hoc" rule.
	sb := New()
	mv := ManifestView{
		ID:          "echo",
		Permissions: PermissionsView{FilePaths: []string{"/a", "/b"}},
	}

	p1, _ := sb.Derive(mv, "free")
	p2, _ := sb.Derive(mv, "free")
	if !reflect.DeepEqual(p1, p2) {
		t.Errorf("Derive non-deterministic: %v vs %v", p1, p2)
	}
}

func TestDerive_DefensiveCopy(t *testing.T) {
	// Mutating the Manifest view after Derive must not poison the Policy.
	sb := New()
	paths := []string{"/only/here"}
	mv := ManifestView{Permissions: PermissionsView{FilePaths: paths}}

	p, _ := sb.Derive(mv, "free")
	paths[0] = "/tampered"

	if p.FileAccess.ReadPaths[0] != "/only/here" {
		t.Errorf("Derive leaked input slice: ReadPaths[0] = %q", p.FileAccess.ReadPaths[0])
	}
}

func TestApply_InstallsIdentityOnCtx(t *testing.T) {
	// Post-#406 section 4: ctx carries Identity (CapID + Tier), not Policy.
	// Authority lives in handles forged at Runtime.Instantiate (#391).
	sb := New()
	ctx := context.Background()
	policy := Policy{Tier: "pro"}

	sbxCtx, err := sb.Apply(ctx, ManifestView{ID: "x"}, policy)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, ok := IdentityFrom(sbxCtx)
	if !ok {
		t.Fatal("IdentityFrom: no identity on sandboxed ctx")
	}
	if got.CapID != "x" {
		t.Errorf("CapID = %q, want x", got.CapID)
	}
	if got.Tier != "pro" {
		t.Errorf("Tier = %q, want pro", got.Tier)
	}

	if _, ok := IdentityFrom(ctx); ok {
		t.Error("IdentityFrom: unsandboxed ctx should not return an identity")
	}
}

func TestApply_NoAccumulation(t *testing.T) {
	// ARCHITECTURE-v0.7.10.md §2.4 hard rule, re-cast in Identity terms:
	// one Identity per ctx. Re-applying overwrites; it must NEVER merge.
	sb := New()
	ctx := context.Background()

	ctx1, _ := sb.Apply(ctx, ManifestView{ID: "cap1"}, Policy{Tier: "free"})
	ctx2, _ := sb.Apply(ctx1, ManifestView{ID: "cap2"}, Policy{Tier: "pro"})

	got, _ := IdentityFrom(ctx2)
	if got.CapID != "cap2" || got.Tier != "pro" {
		t.Errorf("second Apply did not replace: got %+v, want cap2/pro", got)
	}

	// First ctx must still carry the first identity — Apply is pure w.r.t.
	// its input ctx; "no accumulation" is enforced by using a single key.
	got1, _ := IdentityFrom(ctx1)
	if got1.CapID != "cap1" || got1.Tier != "free" {
		t.Errorf("first ctx leaked: got %+v, want cap1/free", got1)
	}
}
