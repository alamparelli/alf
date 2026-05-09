package marketplace

import (
	"errors"
	"sort"
)

// PermissionRatifier is the daemon-supplied callback the marketplace
// manager calls when an Update widens an app's declared permissions
// (#402). Returns the queue ID assigned by the admin pending store
// (for inclusion in the error message) and any error from the
// enqueue itself.
//
// Wired by the daemon at boot. The marketplace package never imports
// internal/admin/pending directly (admin-boundary archtest); the
// callback is the seam.
//
// Payload shape (caller-provided to the queue):
//
//	slug:          "<app slug>"
//	old_perms:     comma-joined sorted list ("" if none)
//	new_perms:     comma-joined sorted list
//	added_perms:   comma-joined sorted list (the widening diff)
//
// The pending queue's KindPermissionWiden item carries this in its
// Payload string-keyed map; the admin CLI / CC ratification page
// reads it back when the operator runs `alf pending` / approves.
type PermissionRatifier func(slug string, oldPerms, newPerms, addedPerms []string) (queueID string, err error)

// ErrPermissionWideningPending is returned by Update when the new
// manifest declares broader permissions than the cached install
// state and the daemon has wired a PermissionRatifier. The error
// message carries the queue ID so the operator sees the next step
// without needing to re-run `alf pending`.
//
// Distinct from ErrPermissionWideningRefused which is returned when
// no ratifier is wired — that path refuses outright (no fallback to
// silent widening).
var ErrPermissionWideningPending = errors.New("marketplace: update permissions widened — operator ratification required")

// ErrPermissionWideningRefused is returned by Update when the new
// manifest widens permissions but no PermissionRatifier callback is
// available (degraded boot, daemon misconfigured). The widening is
// REFUSED: no on-disk state changes, the running app keeps its old
// version. The operator wires the ratifier or accepts the legacy
// flow via SetPermissionRatifier.
var ErrPermissionWideningRefused = errors.New("marketplace: update permissions widened and no ratifier wired — refusing to silently widen")

// diffPermissions returns the sorted list of permissions present in
// `next` but absent from `prev`. Both inputs are treated as sets;
// duplicates within either input collapse, ordering of inputs is
// irrelevant.
//
// A nil or empty `next` means "no permissions declared" — by the
// install-time SEC-002 rule that's already capped to a safe default,
// not "all allowed", so the diff against a non-empty prev is always
// non-widening. A nil or empty `prev` makes any non-empty `next` a
// widening (the operator never approved the perms in the first
// place).
//
// Returned slice is never nil — empty means "no widening".
func diffPermissions(prev, next []string) []string {
	prevSet := make(map[string]struct{}, len(prev))
	for _, p := range prev {
		if p == "" {
			continue
		}
		prevSet[p] = struct{}{}
	}
	added := make([]string, 0, len(next))
	seen := make(map[string]struct{}, len(next))
	for _, p := range next {
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		if _, alreadyHad := prevSet[p]; !alreadyHad {
			added = append(added, p)
		}
	}
	sort.Strings(added)
	return added
}
