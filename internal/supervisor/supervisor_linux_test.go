//go:build linux

package supervisor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestBuildCmd_SandboxAlwaysEnabled(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "app1")
	os.MkdirAll(appDir, 0o755)
	script := filepath.Join(appDir, "run.sh")
	os.WriteFile(script, []byte("#!/bin/sh"), 0o755)

	s := New(dir)
	p := &managedProc{
		config:  ServiceConfig{Command: "./run.sh"},
		appSlug: "app1",
		workDir: appDir,
	}

	cmd, err := s.buildCmd(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cmd.SysProcAttr == nil {
		t.Fatal("sandboxed cmd should have SysProcAttr set")
	}
	flags := cmd.SysProcAttr.Cloneflags
	if flags&syscall.CLONE_NEWNS == 0 {
		t.Error("sandboxed cmd should have CLONE_NEWNS in Cloneflags")
	}
	if flags&syscall.CLONE_NEWPID == 0 {
		t.Error("sandboxed cmd should have CLONE_NEWPID in Cloneflags")
	}
	if flags&syscall.CLONE_NEWNET != 0 {
		t.Error("server sandbox should NOT have CLONE_NEWNET (needs network)")
	}
}
