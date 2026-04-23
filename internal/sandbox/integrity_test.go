package sandbox

import (
	"context"
	"errors"
	"testing"
)

type fakeChecker struct {
	called bool
	err    error
	gotID  string
}

func (f *fakeChecker) Verify(capID string) error {
	f.called = true
	f.gotID = capID
	return f.err
}

func TestApply_NoChecker_IsNoop(t *testing.T) {
	sb := New()
	ctx, err := sb.Apply(context.Background(), ManifestView{ID: "x"}, Policy{})
	if err != nil {
		t.Fatalf("Apply without checker: %v", err)
	}
	if _, ok := PolicyFrom(ctx); !ok {
		t.Error("Policy missing on sandboxed ctx")
	}
}

func TestApply_Checker_Pass(t *testing.T) {
	c := &fakeChecker{}
	sb := New(WithIntegrity(c))

	ctx, err := sb.Apply(context.Background(), ManifestView{ID: "xpost"}, Policy{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !c.called {
		t.Error("checker.Verify not invoked")
	}
	if c.gotID != "xpost" {
		t.Errorf("Verify got capID %q, want xpost", c.gotID)
	}
	if _, ok := PolicyFrom(ctx); !ok {
		t.Error("Policy not installed after successful Verify")
	}
}

func TestApply_Checker_Fail_AbortsBeforePolicyInstall(t *testing.T) {
	boom := errors.New("quarantined")
	c := &fakeChecker{err: boom}
	sb := New(WithIntegrity(c))

	ctx, err := sb.Apply(context.Background(), ManifestView{ID: "evil"}, Policy{})
	if err == nil {
		t.Fatal("expected error from failing Verify, got nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error chain does not wrap the checker error: %v", err)
	}
	if ctx != nil {
		t.Error("failing Apply must return nil ctx so callers cannot accidentally run the capability")
	}
}

func TestWithIntegrity_Nil_IsAccepted(t *testing.T) {
	// Passing nil means "no integrity wire-in" — should behave like New()
	// with no option. Guards against consumers that always call WithIntegrity
	// with a possibly-nil guard (e.g. during daemon startup before the guard
	// is initialised).
	var c IntegrityChecker
	sb := New(WithIntegrity(c))

	_, err := sb.Apply(context.Background(), ManifestView{ID: "x"}, Policy{})
	if err != nil {
		t.Fatalf("Apply with nil-checker option: %v", err)
	}
}
