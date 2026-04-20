package tooling

import (
	goexec "os/exec"
	"testing"
)

// Shim tests: the tooling package exposes thin wrappers around
// internal/sandbox/exec after the #339 C5 move. These tests keep the shim
// funcs covered so tooling's coverage floor holds; the moved symbols get
// their real coverage in internal/sandbox/exec.

func TestShim_SandboxedCmd_SmokesWithoutPanic(t *testing.T) {
	cmd := goexec.Command("/bin/echo", "shim")
	// On non-Linux the underlying func is a no-op; on Linux it mutates the
	// SysProcAttr. Either way the call must not panic.
	SandboxedCmd(cmd, "echo shim", SandboxConfig{})
}

func TestShim_SandboxServerCmd_SmokesWithoutPanic(t *testing.T) {
	cmd := goexec.Command("/bin/echo", "server")
	SandboxServerCmd(cmd, ServerSandboxConfig{})
}

func TestShim_SandboxSafeEnv_ReturnsEnv(t *testing.T) {
	env := SandboxSafeEnv("/tmp/app")
	if len(env) == 0 {
		t.Fatal("SandboxSafeEnv returned empty env")
	}
}

func TestShim_ServerSafeEnv_ReturnsEnv(t *testing.T) {
	env := ServerSafeEnv("/tmp/server")
	if len(env) == 0 {
		t.Fatal("ServerSafeEnv returned empty env")
	}
}
