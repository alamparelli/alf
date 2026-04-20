package capability

import (
	"context"
	"sync"
	"testing"
)

// stubCap is a minimal Capability used only in registry tests.
type stubCap struct {
	manifest Manifest
}

func (s stubCap) Manifest() Manifest         { return s.manifest }
func (s stubCap) Permissions() PermissionSet { return s.manifest.Permissions }
func (s stubCap) Execute(_ context.Context, _ Input) (Output, error) {
	return Output{Data: "ok"}, nil
}

func newStub(id ID, k Kind) stubCap {
	return stubCap{manifest: Manifest{ID: id, Kind: k, Name: string(id)}}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	c := newStub("bash", KindTool)
	if err := r.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("bash")
	if !ok {
		t.Fatal("Get: expected capability, got nothing")
	}
	if got.Manifest().ID != "bash" {
		t.Fatalf("Get: want id=bash, got %q", got.Manifest().ID)
	}
	if r.Len() != 1 {
		t.Fatalf("Len: want 1, got %d", r.Len())
	}
}

func TestRegistry_RegisterNilRejected(t *testing.T) {
	r := NewRegistry()
	err := r.Register(nil)
	if err == nil {
		t.Fatal("Register(nil): expected error, got nil")
	}
}

func TestRegistry_RegisterEmptyIDRejected(t *testing.T) {
	r := NewRegistry()
	c := stubCap{manifest: Manifest{ID: "", Kind: KindTool}}
	err := r.Register(c)
	if err == nil {
		t.Fatal("Register(empty ID): expected error, got nil")
	}
}

func TestRegistry_RegisterDuplicateRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(newStub("bash", KindTool)); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(newStub("bash", KindTool))
	if err == nil {
		t.Fatal("duplicate Register: expected error, got nil")
	}
}

func TestRegistry_ReplaceUpserts(t *testing.T) {
	r := NewRegistry()
	first := newStub("x", KindTool)
	if err := r.Replace(first); err != nil {
		t.Fatalf("Replace (add): %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len after insert: want 1, got %d", r.Len())
	}
	second := stubCap{manifest: Manifest{ID: "x", Kind: KindSkill, Name: "renamed"}}
	if err := r.Replace(second); err != nil {
		t.Fatalf("Replace (overwrite): %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len after overwrite: want 1, got %d", r.Len())
	}
	got, _ := r.Get("x")
	if got.Manifest().Kind != KindSkill {
		t.Fatalf("Replace did not overwrite: kind=%v", got.Manifest().Kind)
	}
}

func TestRegistry_ReplaceNilRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Replace(nil); err == nil {
		t.Fatal("Replace(nil): expected error")
	}
}

func TestRegistry_ReplaceEmptyIDRejected(t *testing.T) {
	r := NewRegistry()
	c := stubCap{manifest: Manifest{ID: ""}}
	if err := r.Replace(c); err == nil {
		t.Fatal("Replace(empty ID): expected error")
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get(missing): expected not found, got capability")
	}
}

func TestRegistry_AllDeterministic(t *testing.T) {
	r := NewRegistry()
	for _, id := range []ID{"c", "a", "b"} {
		if err := r.Register(newStub(id, KindTool)); err != nil {
			t.Fatalf("Register %q: %v", id, err)
		}
	}
	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All: want 3, got %d", len(all))
	}
	got := []ID{all[0].Manifest().ID, all[1].Manifest().ID, all[2].Manifest().ID}
	want := []ID{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("All order: want %v, got %v", want, got)
		}
	}
}

func TestRegistry_ByKind(t *testing.T) {
	r := NewRegistry()
	must := func(c Capability) {
		if err := r.Register(c); err != nil {
			t.Fatalf("Register %q: %v", c.Manifest().ID, err)
		}
	}
	must(newStub("bash", KindTool))
	must(newStub("grep", KindTool))
	must(newStub("commit-push", KindSkill))
	must(newStub("xpost", KindApp))

	tools := r.ByKind(KindTool)
	if len(tools) != 2 {
		t.Fatalf("ByKind(Tool): want 2, got %d", len(tools))
	}
	if tools[0].Manifest().ID != "bash" || tools[1].Manifest().ID != "grep" {
		t.Fatalf("ByKind(Tool) order: got %q,%q", tools[0].Manifest().ID, tools[1].Manifest().ID)
	}

	skills := r.ByKind(KindSkill)
	if len(skills) != 1 || skills[0].Manifest().ID != "commit-push" {
		t.Fatalf("ByKind(Skill): got %+v", skills)
	}

	apps := r.ByKind(KindApp)
	if len(apps) != 1 || apps[0].Manifest().ID != "xpost" {
		t.Fatalf("ByKind(App): got %+v", apps)
	}
}

func TestRegistry_ConcurrentRegister(t *testing.T) {
	r := NewRegistry()
	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		id := ID(byteID(i))
		go func(id ID) {
			defer wg.Done()
			if err := r.Register(newStub(id, KindTool)); err != nil {
				errs <- err
			}
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Register: %v", err)
		}
	}
	if r.Len() != n {
		t.Fatalf("Len after concurrent register: want %d, got %d", n, r.Len())
	}
}

func byteID(i int) string {
	// Deterministic unique IDs without fmt.
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
	n := len(letters)
	if i < n {
		return string(letters[i]) + "0"
	}
	return string(letters[i/n]) + string(letters[i%n])
}

// Sanity: ensure stubCap satisfies Capability at compile time.
var _ Capability = stubCap{}

// Sanity: Execute returns the stub's data.
func TestStubExecute(t *testing.T) {
	c := newStub("x", KindTool)
	out, err := c.Execute(context.Background(), Input{"k": "v"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Data != "ok" {
		t.Fatalf("Execute data: got %v", out.Data)
	}
}
