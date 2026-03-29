package marketplace

import "fmt"

// ValidPermissions is the set of permissions an app can declare.
var ValidPermissions = map[string]bool{
	"storage":   true, // read/write per-app key/value storage
	"bash":      true, // execute shell commands via /api/bash
	"upload":    true, // upload files via /api/apps/{slug}/upload
	"clipboard": true, // read/write clipboard (via parent postMessage)
	"events":    true, // emit inter-app events (via parent postMessage)
}

// PermissionChecker determines whether an app has a given permission.
// Handlers use this interface to avoid importing the full marketplace package.
type PermissionChecker interface {
	HasPermission(slug, perm string) bool
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
