package marketplace

import (
	"github.com/alamparelli/alf/internal/sandbox"
)

// PermissionChecker determines whether an app has a given permission.
// Handlers use this interface to avoid importing the full marketplace package.
type PermissionChecker interface {
	HasPermission(slug, perm string) bool
	// IsTracked returns true if the app is managed by the marketplace
	// (installed from registry or explicitly enabled). Internal/default apps
	// are NOT tracked and should bypass sandboxing.
	IsTracked(slug string) bool
}

// ValidateManifest checks a manifest for common issues before publishing or enabling.
// Returns a list of errors (blocking) and warnings (informational).
// Permission/service validation is delegated to sandbox; metadata checks
// (name, slug, semver version) stay here because they concern the
// marketplace Manifest shape.
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
	if err := sandbox.ValidatePermissions(m.Permissions); err != nil {
		errors = append(errors, err.Error())
	}
	if err := sandbox.ValidateServices(m.Services); err != nil {
		errors = append(errors, err.Error())
	}
	return
}
