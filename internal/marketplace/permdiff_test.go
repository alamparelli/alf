package marketplace

import (
	"errors"
	"reflect"
	"testing"
)

// TestDiffPermissions_Empty pins the empty-input edge cases:
// nil/nil and []/[] are both no-widening; nil/[] (no old, no new)
// is the same. The function must never return nil so callers can
// just use len(added) > 0.
func TestDiffPermissions_Empty(t *testing.T) {
	tcs := []struct {
		name       string
		prev, next []string
	}{
		{"nil/nil", nil, nil},
		{"empty/empty", []string{}, []string{}},
		{"nil/empty", nil, []string{}},
		{"empty/nil", []string{}, nil},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			got := diffPermissions(tc.prev, tc.next)
			if got == nil {
				t.Error("returned nil; want empty slice")
			}
			if len(got) != 0 {
				t.Errorf("got %v, want empty", got)
			}
		})
	}
}

// TestDiffPermissions_NarrowingIsSilent pins #402's "narrowing is
// allowed silently" rule — removing perms produces no widening
// flag.
func TestDiffPermissions_NarrowingIsSilent(t *testing.T) {
	prev := []string{"storage", "bash", "network"}
	next := []string{"storage"}
	if got := diffPermissions(prev, next); len(got) != 0 {
		t.Errorf("narrowing produced added=%v; want empty", got)
	}
}

// TestDiffPermissions_WideningSurfacesAddedPerms pins the canonical
// happy widening: prev = ["storage"], next = ["storage", "bash",
// "network"] → added = ["bash", "network"], sorted.
func TestDiffPermissions_WideningSurfacesAddedPerms(t *testing.T) {
	prev := []string{"storage"}
	next := []string{"storage", "bash", "network"}
	got := diffPermissions(prev, next)
	want := []string{"bash", "network"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestDiffPermissions_OrderInsensitive pins that input ordering
// doesn't change the diff. Manifests are JSON arrays — the marketplace
// server might serialise in any order, the diff result must not.
func TestDiffPermissions_OrderInsensitive(t *testing.T) {
	a := diffPermissions([]string{"a", "b"}, []string{"b", "a", "c"})
	b := diffPermissions([]string{"b", "a"}, []string{"c", "a", "b"})
	if !reflect.DeepEqual(a, b) {
		t.Errorf("order-sensitive diff: %v vs %v", a, b)
	}
	if !reflect.DeepEqual(a, []string{"c"}) {
		t.Errorf("got %v, want [c]", a)
	}
}

// TestDiffPermissions_Duplicates pins that duplicate entries within
// either input collapse — sets, not multisets.
func TestDiffPermissions_Duplicates(t *testing.T) {
	got := diffPermissions(
		[]string{"storage", "storage", "bash"},
		[]string{"storage", "storage", "bash", "network", "network"},
	)
	want := []string{"network"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestDiffPermissions_EmptyPrevAnyNextIsWidening pins the
// "previously had nothing, now wants something" case — operator
// never approved any perm in the prior install (e.g. SEC-002 cap),
// so any non-empty next is a widening.
func TestDiffPermissions_EmptyPrevAnyNextIsWidening(t *testing.T) {
	got := diffPermissions(nil, []string{"storage", "bash"})
	want := []string{"bash", "storage"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestErrSentinelsDistinct pins that ErrPermissionWideningPending
// and ErrPermissionWideningRefused are distinct sentinels — the
// daemon's error-handling branches on errors.Is for each.
func TestErrSentinelsDistinct(t *testing.T) {
	if errors.Is(ErrPermissionWideningPending, ErrPermissionWideningRefused) {
		t.Error("Pending and Refused are the same sentinel — must be distinct for error.Is dispatch")
	}
	if errors.Is(ErrPermissionWideningRefused, ErrPermissionWideningPending) {
		t.Error("Refused and Pending are the same sentinel")
	}
}
