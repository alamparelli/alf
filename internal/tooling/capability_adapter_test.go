package tooling

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alamparelli/alf/internal/capability"
)

// fakeNative is a minimal NativeTool for adapter tests.
type fakeNative struct {
	name string
	desc string
	run  func(ctx context.Context, argsJSON string) (string, error)
}

func (f fakeNative) ToolName() string { return f.name }
func (f fakeNative) Schema() ToolSchema {
	return ToolSchema{Name: f.name, Description: f.desc}
}
func (f fakeNative) Run(ctx context.Context, argsJSON string) (string, error) {
	return f.run(ctx, argsJSON)
}

func TestAdapter_ManifestFromSchema(t *testing.T) {
	nt := fakeNative{name: "bash", desc: "run a shell"}
	c := asCapability(nt)
	m := c.Manifest()
	if m.ID != capability.ID("bash") {
		t.Fatalf("ID: got %q", m.ID)
	}
	if m.Kind != capability.KindTool {
		t.Fatalf("Kind: got %v", m.Kind)
	}
	if m.Name != "bash" || m.Description != "run a shell" {
		t.Fatalf("Manifest name/desc: got %+v", m)
	}
}

func TestAdapter_ExecuteMarshalsInput(t *testing.T) {
	var seen string
	nt := fakeNative{
		name: "echo",
		run: func(_ context.Context, argsJSON string) (string, error) {
			seen = argsJSON
			return "hi", nil
		},
	}
	c := asCapability(nt)
	out, err := c.Execute(context.Background(), capability.Input{"x": 1, "y": "two"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Data != "hi" {
		t.Fatalf("Output.Data: got %v", out.Data)
	}
	if !strings.Contains(seen, `"x":1`) || !strings.Contains(seen, `"y":"two"`) {
		t.Fatalf("argsJSON: got %q", seen)
	}
}

func TestAdapter_ExecuteNilInput(t *testing.T) {
	var seen string
	nt := fakeNative{
		name: "noargs",
		run: func(_ context.Context, argsJSON string) (string, error) {
			seen = argsJSON
			return "", nil
		},
	}
	if _, err := asCapability(nt).Execute(context.Background(), nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if seen != "{}" {
		t.Fatalf("nil Input should marshal as {}; got %q", seen)
	}
}

func TestAdapter_ExecutePropagatesError(t *testing.T) {
	boom := errors.New("boom")
	nt := fakeNative{
		name: "fail",
		run: func(_ context.Context, _ string) (string, error) {
			return "", boom
		},
	}
	_, err := asCapability(nt).Execute(context.Background(), capability.Input{})
	if !errors.Is(err, boom) {
		t.Fatalf("Execute error: want boom, got %v", err)
	}
}

func TestRegistry_DualRegisterOnAttachAfter(t *testing.T) {
	tmp := t.TempDir()
	reg := NewRegistry(tmp)
	reg.RegisterNative(fakeNative{name: "a", run: func(_ context.Context, _ string) (string, error) { return "", nil }})
	reg.RegisterNative(fakeNative{name: "b", run: func(_ context.Context, _ string) (string, error) { return "", nil }})

	cr := capability.NewRegistry()
	reg.SetCapabilityRegistry(cr)

	if cr.Len() != 2 {
		t.Fatalf("back-fill: want 2, got %d", cr.Len())
	}
	if _, ok := cr.Get("a"); !ok {
		t.Fatal("missing a")
	}
	if _, ok := cr.Get("b"); !ok {
		t.Fatal("missing b")
	}
}

func TestRegistry_DualRegisterOnAttachBefore(t *testing.T) {
	tmp := t.TempDir()
	reg := NewRegistry(tmp)
	cr := capability.NewRegistry()
	reg.SetCapabilityRegistry(cr)

	reg.RegisterNative(fakeNative{name: "c", run: func(_ context.Context, _ string) (string, error) { return "ok", nil }})

	got, ok := cr.Get("c")
	if !ok {
		t.Fatal("c not mirrored")
	}
	if got.Manifest().Kind != capability.KindTool {
		t.Fatalf("Kind: got %v", got.Manifest().Kind)
	}
	out, err := got.Execute(context.Background(), capability.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Data != "ok" {
		t.Fatalf("Execute data: %v", out.Data)
	}
}

func TestRegistry_CapabilityRegistryAccessor(t *testing.T) {
	reg := NewRegistry(t.TempDir())
	if reg.CapabilityRegistry() != nil {
		t.Fatal("want nil before SetCapabilityRegistry")
	}
	cr := capability.NewRegistry()
	reg.SetCapabilityRegistry(cr)
	if reg.CapabilityRegistry() != cr {
		t.Fatal("accessor should return attached registry")
	}
}
