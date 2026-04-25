package provider

import (
	"context"
	"testing"
)

// stubProvider records the params it was invoked with so the test can
// assert what reached the underlying provider after the wrapper ran.
type stubProvider struct {
	gotParams Params
}

func (s *stubProvider) Invoke(_ context.Context, _ string, params Params, _ OnProgress) (*Result, error) {
	s.gotParams = params
	return &Result{Text: "ok"}, nil
}

func TestKernelPromptInjector_PrependsToSystemPrompts(t *testing.T) {
	stub := &stubProvider{}
	wrapped := NewKernelPromptInjector(stub, "KERNEL")

	in := Params{SystemPrompts: []string{"caller-prompt-1", "caller-prompt-2"}}
	if _, err := wrapped.Invoke(context.Background(), "p", in, nil); err != nil {
		t.Fatal(err)
	}
	got := stub.gotParams.SystemPrompts
	want := []string{"KERNEL", "caller-prompt-1", "caller-prompt-2"}
	if len(got) != len(want) {
		t.Fatalf("got %d prompts, want %d: %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestKernelPromptInjector_EmptyPromptIsNoOp(t *testing.T) {
	stub := &stubProvider{}
	wrapped := NewKernelPromptInjector(stub, "")

	in := Params{SystemPrompts: []string{"caller"}}
	if _, err := wrapped.Invoke(context.Background(), "p", in, nil); err != nil {
		t.Fatal(err)
	}
	got := stub.gotParams.SystemPrompts
	if len(got) != 1 || got[0] != "caller" {
		t.Errorf("empty kernel prompt should not modify SystemPrompts; got %v", got)
	}
}

func TestKernelPromptInjector_NoCallerPromptsStillInjects(t *testing.T) {
	stub := &stubProvider{}
	wrapped := NewKernelPromptInjector(stub, "KERNEL")

	if _, err := wrapped.Invoke(context.Background(), "p", Params{}, nil); err != nil {
		t.Fatal(err)
	}
	got := stub.gotParams.SystemPrompts
	if len(got) != 1 || got[0] != "KERNEL" {
		t.Errorf("kernel prompt should be sole entry when caller had none; got %v", got)
	}
}

func TestKernelPromptInjector_NilProviderReturnsNil(t *testing.T) {
	if got := NewKernelPromptInjector(nil, "KERNEL"); got != nil {
		t.Errorf("nil inner provider should return nil wrapper, got %+v", got)
	}
}

func TestRegistry_ForBackend_WrapsWhenKernelPromptSet(t *testing.T) {
	stub := &stubProvider{}
	r := &Registry{
		backends: map[string]*APIProvider{},
		generic:  map[string]Provider{"stub": stub},
	}
	r.SetKernelPrompt("KERNEL")

	got := r.ForBackend("stub")
	if _, ok := got.(*KernelPromptInjector); !ok {
		t.Errorf("ForBackend should return wrapped provider when kernel prompt set, got %T", got)
	}

	// Round-trip: invoking the wrapped provider should record the
	// kernel-prompt prefix at the underlying stub.
	if _, err := got.Invoke(context.Background(), "p", Params{SystemPrompts: []string{"caller"}}, nil); err != nil {
		t.Fatal(err)
	}
	if len(stub.gotParams.SystemPrompts) != 2 || stub.gotParams.SystemPrompts[0] != "KERNEL" {
		t.Errorf("wrapper did not prepend kernel prompt; got %v", stub.gotParams.SystemPrompts)
	}
}

func TestRegistry_ForBackend_NoWrapWhenKernelPromptUnset(t *testing.T) {
	stub := &stubProvider{}
	r := &Registry{
		backends: map[string]*APIProvider{},
		generic:  map[string]Provider{"stub": stub},
	}
	// No SetKernelPrompt call.

	got := r.ForBackend("stub")
	if _, ok := got.(*KernelPromptInjector); ok {
		t.Errorf("ForBackend should NOT wrap when kernel prompt unset, got %T", got)
	}
}
