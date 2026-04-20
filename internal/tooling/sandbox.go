package tooling

import (
	goexec "os/exec"

	sbexec "github.com/alamparelli/alf/internal/sandbox/exec"
)

// This file is a thin re-export shim. The Exec facet of Sandbox now lives at
// internal/sandbox/exec (moved during #339 Step 3). Tooling's native_*.go,
// executor, and registry keep using these aliases until Runtime (#340)
// rewires consumers.

// SandboxConfig is an alias for exec.SandboxConfig.
type SandboxConfig = sbexec.SandboxConfig

// ServerSandboxConfig is an alias for exec.ServerSandboxConfig.
type ServerSandboxConfig = sbexec.ServerSandboxConfig

// ResolvePath re-exports exec.ResolvePath.
func ResolvePath(dataDir, path string) string {
	return sbexec.ResolvePath(dataDir, path)
}

// CheckBoundary re-exports exec.CheckBoundary.
func CheckBoundary(dataDir, path string) (string, error) {
	return sbexec.CheckBoundary(dataDir, path)
}

// SandboxedCmd re-exports exec.SandboxedCmd.
func SandboxedCmd(cmd *goexec.Cmd, originalCommand string, cfg SandboxConfig) {
	sbexec.SandboxedCmd(cmd, originalCommand, cfg)
}

// SandboxSafeEnv re-exports exec.SandboxSafeEnv.
func SandboxSafeEnv(appDataDir string) []string {
	return sbexec.SandboxSafeEnv(appDataDir)
}

// SandboxServerCmd re-exports exec.SandboxServerCmd.
func SandboxServerCmd(cmd *goexec.Cmd, cfg ServerSandboxConfig) {
	sbexec.SandboxServerCmd(cmd, cfg)
}

// ServerSafeEnv re-exports exec.ServerSafeEnv.
func ServerSafeEnv(appDir string) []string {
	return sbexec.ServerSafeEnv(appDir)
}
