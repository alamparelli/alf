package admin

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/admin/pending"
)

// newPendingEnv builds an Env wired against a tmp PendingDir + a
// scripted Stdin for the ratify confirm flow. Tests that need to
// pre-seed the queue receive the *DirStore back so they can Append
// without going through the unexposed daemon path.
func newPendingEnv(t *testing.T, stdin string) (Env, *pending.DirStore, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pending")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	now := func() time.Time { return time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC) }
	store, err := pending.NewDirStore(dir, now)
	if err != nil {
		t.Fatalf("NewDirStore: %v", err)
	}
	env := Env{
		PendingDir: dir,
		Stdin:      strings.NewReader(stdin),
		Stdout:     stdout,
		Stderr:     stderr,
		IsTerminal: func() bool { return true },
		Now:        now,
	}
	return env, store, stdout, stderr
}

func TestPending_EmptyQueue(t *testing.T) {
	env, _, stdout, _ := newPendingEnv(t, "")
	if err := Pending(env, nil); err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if !strings.Contains(stdout.String(), "No pending ratifications") {
		t.Errorf("expected empty-queue message, got: %s", stdout.String())
	}
}

func TestPending_ListsItems(t *testing.T) {
	env, store, stdout, _ := newPendingEnv(t, "")
	id1, _ := store.Append(context.Background(), pending.Item{
		Kind:    pending.KindTrustAdd,
		Payload: map[string]string{"fp": "ABCDEF1234567890"},
	})
	id2, _ := store.Append(context.Background(), pending.Item{
		Kind:    pending.KindBundleInstall,
		Payload: map[string]string{"path": "/tmp/x.zip", "signer": "FF00"},
	})

	if err := Pending(env, nil); err != nil {
		t.Fatalf("Pending: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, id1) {
		t.Errorf("output missing id1: %s", out)
	}
	if !strings.Contains(out, id2) {
		t.Errorf("output missing id2: %s", out)
	}
	if !strings.Contains(out, string(pending.KindTrustAdd)) {
		t.Errorf("output missing kind: %s", out)
	}
	if !strings.Contains(out, "fp=ABCDEF1234567890") {
		t.Errorf("output missing payload: %s", out)
	}
}

func TestPending_HelpExitsZero(t *testing.T) {
	env, _, stdout, _ := newPendingEnv(t, "")
	if err := Pending(env, []string{"--help"}); err != nil {
		t.Fatalf("Pending --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: alf pending") {
		t.Errorf("--help output missing usage: %s", stdout.String())
	}
}

func TestPending_RejectsUnknownSubcommand(t *testing.T) {
	env, _, _, _ := newPendingEnv(t, "")
	err := Pending(env, []string{"approve"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("got %v, want unknown-subcommand error", err)
	}
}

func TestRatify_ApprovesByDefault(t *testing.T) {
	env, store, stdout, _ := newPendingEnv(t, "yes\n")
	id, _ := store.Append(context.Background(), pending.Item{
		Kind:    pending.KindPermissionWiden,
		Payload: map[string]string{"diff": "+http"},
	})

	if err := Ratify(env, []string{id}); err != nil {
		t.Fatalf("Ratify: %v", err)
	}
	if !strings.Contains(stdout.String(), "Approved "+id) {
		t.Errorf("expected approval line, got: %s", stdout.String())
	}

	items, _ := store.List(context.Background())
	if len(items) != 0 {
		t.Errorf("after Ratify approve: %d items remain, want 0", len(items))
	}
}

func TestRatify_DenyFlag(t *testing.T) {
	env, store, stdout, _ := newPendingEnv(t, "yes\n")
	id, _ := store.Append(context.Background(), pending.Item{Kind: pending.KindTrustAdd})

	if err := Ratify(env, []string{id, "--deny"}); err != nil {
		t.Fatalf("Ratify --deny: %v", err)
	}
	if !strings.Contains(stdout.String(), "Denied "+id) {
		t.Errorf("expected denial line, got: %s", stdout.String())
	}
	items, _ := store.List(context.Background())
	if len(items) != 0 {
		t.Errorf("after Ratify deny: %d items remain, want 0", len(items))
	}
}

func TestRatify_DenyFlagFirst(t *testing.T) {
	// Argument-order independence: --deny before <id> works too.
	env, store, _, _ := newPendingEnv(t, "yes\n")
	id, _ := store.Append(context.Background(), pending.Item{Kind: pending.KindTrustAdd})
	if err := Ratify(env, []string{"--deny", id}); err != nil {
		t.Fatalf("Ratify --deny <id>: %v", err)
	}
	items, _ := store.List(context.Background())
	if len(items) != 0 {
		t.Errorf("items remain after deny: %d", len(items))
	}
}

func TestRatify_RefusesNonTTY(t *testing.T) {
	env, store, _, _ := newPendingEnv(t, "yes\n")
	env.IsTerminal = func() bool { return false }
	id, _ := store.Append(context.Background(), pending.Item{Kind: pending.KindTrustAdd})
	err := Ratify(env, []string{id})
	if !errors.Is(err, ErrNonInteractive) {
		t.Fatalf("got %v, want ErrNonInteractive", err)
	}
	// Item should still be in queue.
	items, _ := store.List(context.Background())
	if len(items) != 1 {
		t.Errorf("non-TTY refusal should leave queue intact, got %d items", len(items))
	}
}

func TestRatify_AbortedOnNonYes(t *testing.T) {
	env, store, _, _ := newPendingEnv(t, "no\n")
	id, _ := store.Append(context.Background(), pending.Item{Kind: pending.KindTrustAdd})
	err := Ratify(env, []string{id})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("got %v, want aborted error", err)
	}
	items, _ := store.List(context.Background())
	if len(items) != 1 {
		t.Errorf("aborted Ratify should leave queue intact, got %d items", len(items))
	}
}

func TestRatify_UnknownID(t *testing.T) {
	env, _, _, _ := newPendingEnv(t, "yes\n")
	err := Ratify(env, []string{"000000000099"})
	if err == nil || !strings.Contains(err.Error(), "no pending item") {
		t.Fatalf("got %v, want unknown-id error", err)
	}
}

func TestRatify_MissingID(t *testing.T) {
	env, _, _, _ := newPendingEnv(t, "")
	err := Ratify(env, nil)
	if err == nil || !strings.Contains(err.Error(), "missing <id>") {
		t.Fatalf("got %v, want missing-id error", err)
	}
}

func TestRatify_MultipleIDsRejected(t *testing.T) {
	env, _, _, _ := newPendingEnv(t, "")
	err := Ratify(env, []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "only one") {
		t.Fatalf("got %v, want multiple-id error", err)
	}
}

func TestRatify_UnknownFlag(t *testing.T) {
	env, _, _, _ := newPendingEnv(t, "")
	err := Ratify(env, []string{"--whatever"})
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("got %v, want unknown-flag error", err)
	}
}

func TestRatify_HelpExitsZero(t *testing.T) {
	env, _, stdout, _ := newPendingEnv(t, "")
	if err := Ratify(env, []string{"--help"}); err != nil {
		t.Fatalf("Ratify --help: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: alf ratify") {
		t.Errorf("--help output missing usage: %s", stdout.String())
	}
}

func TestRatify_ShowsItemDetailsBeforePrompt(t *testing.T) {
	env, store, stdout, _ := newPendingEnv(t, "yes\n")
	id, _ := store.Append(context.Background(), pending.Item{
		Kind:      pending.KindBundleInstall,
		Payload:   map[string]string{"path": "/srv/bundle.zip", "signer": "DEADBEEF"},
		CreatedBy: "skill:installer",
	})
	if err := Ratify(env, []string{id}); err != nil {
		t.Fatalf("Ratify: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"Approve pending item", id, "bundle.install", "skill:installer", "/srv/bundle.zip", "DEADBEEF"} {
		if !strings.Contains(out, want) {
			t.Errorf("Ratify output missing %q\nfull output:\n%s", want, out)
		}
	}
}
