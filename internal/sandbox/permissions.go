package sandbox

import (
	"fmt"
	"strings"
)

// ValidPermissions is the set of permission names a Capability manifest can
// declare. The vocabulary is defined here because Sandbox is the authority on
// what a declared permission means and how it maps into a Policy.
var ValidPermissions = map[string]bool{
	"storage":   true, // read/write per-capability key/value storage
	"bash":      true, // execute shell commands via /api/bash
	"tool":      true, // invoke the capability's own CLI tool via /api/tool (no raw shell)
	"upload":    true, // upload files via /api/apps/{slug}/upload
	"clipboard": true, // read/write clipboard (via parent postMessage)
	"events":    true, // emit inter-capability events (via parent postMessage)
	"network":   true, // network access in sandboxed bash (skip CLONE_NEWNET)
}

// UntrustedMaxPermissions are the only permissions allowed for untrusted
// capabilities. Untrusted = installed from outside the curated registry.
// Sandbox owns this list because trust capping is a policy decision.
var UntrustedMaxPermissions = map[string]bool{
	"storage":   true,
	"events":    true,
	"clipboard": true,
}

// ValidatePermissions checks that every name in perms is a known permission.
func ValidatePermissions(perms []string) error {
	for _, p := range perms {
		if !ValidPermissions[p] {
			return fmt.Errorf("unknown permission: %q", p)
		}
	}
	return nil
}

// ValidateServices checks that declared vault service names are safe
// (no path separators). Vault service names flow into SecretRules.
func ValidateServices(services []string) error {
	for _, s := range services {
		if s == "" {
			return fmt.Errorf("empty service name")
		}
		if strings.ContainsAny(s, "/.\\") {
			return fmt.Errorf("service name %q contains path separator", s)
		}
	}
	return nil
}

// CapPermissionsForUntrusted restricts perms to the safe subset for untrusted
// capabilities. If perms is nil (legacy/no field), returns nil unchanged so
// callers can distinguish "not declared" from "declared empty".
func CapPermissionsForUntrusted(perms []string) []string {
	if perms == nil {
		return nil
	}
	capped := make([]string, 0, len(perms))
	for _, p := range perms {
		if UntrustedMaxPermissions[p] {
			capped = append(capped, p)
		}
	}
	return capped
}

func init() {
	// SEC-006: compile-time invariant — UntrustedMaxPermissions ⊆ ValidPermissions.
	for p := range UntrustedMaxPermissions {
		if !ValidPermissions[p] {
			panic("UntrustedMaxPermissions contains unknown permission: " + p)
		}
	}
}
