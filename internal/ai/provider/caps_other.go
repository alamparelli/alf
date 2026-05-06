//go:build !linux

package provider

import "syscall"

// dropAmbientCaps is a no-op on non-Linux platforms (kept symmetric
// with caps_linux.go so call sites don't need build tags).
func dropAmbientCaps(spa *syscall.SysProcAttr) {
	_ = spa
}

// capDropWrap on non-Linux returns the original argv unchanged. macOS
// dev hosts lack setpriv and don't have PR_CAP_AMBIENT semantics.
// Production-relevant cap drops only matter on the Linux daemon image.
func capDropWrap(name string, args []string) (string, []string) {
	return name, args
}
