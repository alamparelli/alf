//go:build !linux

package tooling

import "os/exec"

// dropToAlfUser is a no-op on non-Linux platforms (dev/test on macOS).
// In production (Linux container), this drops to uid 1000.
func dropToAlfUser(_ *exec.Cmd) {}
