package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGrantContainerACL_RequiresSetfacl pins the documented behaviour
// when setfacl is not on PATH: the helper returns a typed error
// pointing the operator at the `acl` package install. Without this
// signal, an `alf init` on a distro without acl would silently leave
// the Tier-3 data dirs un-ACL'd, breaking every subsequent
// `alf keygen` / `alf sign` from the host. See ticket #422.
func TestGrantContainerACL_RequiresSetfacl(t *testing.T) {
	if _, err := exec.LookPath("setfacl"); err == nil {
		t.Skip("setfacl present — this test exercises the missing-binary path")
	}
	tmp := t.TempDir()
	err := grantContainerACL(tmp, daemonContainerUID)
	if err == nil {
		t.Fatal("expected error when setfacl is missing, got nil")
	}
	if !strings.Contains(err.Error(), "setfacl") {
		t.Errorf("error %q does not mention setfacl", err.Error())
	}
	if !strings.Contains(err.Error(), "acl") {
		t.Errorf("error %q does not point at the `acl` package", err.Error())
	}
}

// TestGrantContainerACL_HappyPath exercises the helper on a real
// tmpdir when setfacl IS available. Asserts that the resulting ACL
// carries the expected u:1001:rwx entry on each of keys/, trust/,
// apps/, and that the default ACL is set (so future subdirs of
// apps/<id>/ inherit the container grant).
func TestGrantContainerACL_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("setfacl"); err != nil {
		t.Skip("setfacl not available — install `acl` package to run this test")
	}
	if _, err := exec.LookPath("getfacl"); err != nil {
		t.Skip("getfacl not available — install `acl` package to run this test")
	}
	dataDir := t.TempDir()
	// init.go's caller does MkdirAll for the Tier-3 subdirs before
	// invoking grantContainerACL; mirror that here.
	for _, sub := range []string{"keys", "trust", "apps"} {
		if err := exec.Command("mkdir", "-p", filepath.Join(dataDir, sub)).Run(); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}

	if err := grantContainerACL(dataDir, daemonContainerUID); err != nil {
		t.Fatalf("grantContainerACL: %v", err)
	}

	for _, sub := range []string{"keys", "trust", "apps"} {
		target := filepath.Join(dataDir, sub)
		out, err := exec.Command("getfacl", "-p", target).CombinedOutput()
		if err != nil {
			t.Errorf("getfacl %s: %v (%s)", target, err, out)
			continue
		}
		text := string(out)
		// Expect u:1001:rwx in the existing-entries ACL.
		wantUser := "user:1001:rwx"
		if !strings.Contains(text, wantUser) {
			t.Errorf("%s: ACL missing %q, got:\n%s", target, wantUser, text)
		}
		// Expect default:user:1001:rwx so future subdirs inherit.
		wantDefault := "default:user:1001:rwx"
		if !strings.Contains(text, wantDefault) {
			t.Errorf("%s: default ACL missing %q, got:\n%s", target, wantDefault, text)
		}
	}
}
