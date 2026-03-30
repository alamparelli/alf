package marketplace

import (
	"fmt"
	"strings"
)

// ValidPermissions is the set of permissions an app can declare.
var ValidPermissions = map[string]bool{
	"storage":   true, // read/write per-app key/value storage
	"bash":      true, // execute shell commands via /api/bash
	"upload":    true, // upload files via /api/apps/{slug}/upload
	"clipboard": true, // read/write clipboard (via parent postMessage)
	"events":    true, // emit inter-app events (via parent postMessage)
	"network":   true, // network access in sandboxed bash (skip CLONE_NEWNET)
}

// PermissionChecker determines whether an app has a given permission.
// Handlers use this interface to avoid importing the full marketplace package.
type PermissionChecker interface {
	HasPermission(slug, perm string) bool
	// IsTracked returns true if the app is managed by the marketplace
	// (installed from registry or explicitly enabled). Internal/default apps
	// are NOT tracked and should bypass sandboxing.
	IsTracked(slug string) bool
}

// ValidatePermissions checks that all declared permissions are known.
// Returns an error listing any invalid permissions.
func ValidatePermissions(perms []string) error {
	for _, p := range perms {
		if !ValidPermissions[p] {
			return fmt.Errorf("unknown permission: %q", p)
		}
	}
	return nil
}

// ValidateManifest checks a manifest for common issues before publishing or enabling.
// Returns a list of errors (blocking) and warnings (informational).
func ValidateManifest(m *Manifest) (errors []string, warnings []string) {
	if m.Name == "" {
		errors = append(errors, "name is required")
	}
	if m.Slug == "" {
		errors = append(errors, "slug is required")
	}
	if m.Version == "" {
		errors = append(errors, "version is required")
	} else {
		// Basic semver check
		parts := 0
		for _, c := range m.Version {
			if c == '.' {
				parts++
			}
		}
		if parts != 2 {
			errors = append(errors, "version must be semver (e.g. 1.0.0)")
		}
	}
	if m.Description == "" {
		warnings = append(warnings, "description is empty")
	}
	if err := ValidatePermissions(m.Permissions); err != nil {
		errors = append(errors, err.Error())
	}
	if err := ValidateServices(m.Services); err != nil {
		errors = append(errors, err.Error())
	}
	return
}

// ValidateServices checks that declared vault service names are safe.
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
