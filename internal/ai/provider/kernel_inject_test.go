package provider

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// failingMarkerReader pins the SEC-080-005 fail-loud path for the
// provider-side marker nonce. Mirrors failingReader in
// internal/runtime/llm/kernel_prompt_test.go.
type failingMarkerReader struct{ err error }

func (f failingMarkerReader) Read(p []byte) (int, error) { return 0, f.err }

func TestKernelPromptInjector_FailsLoudOnMarkerRandError(t *testing.T) {
	want := errors.New("simulated entropy exhaustion")
	restore := SetMarkerNonceRandReaderForTest(failingMarkerReader{err: want})
	defer restore()

	stub := &stubProvider{}
	wrapped := NewKernelPromptInjector(stub, "KERNEL")
	_, err := wrapped.Invoke(context.Background(), "p", Params{}, nil)
	if err == nil {
		t.Fatal("Invoke returned no error on rand failure (regression: fallback to constant marker nonce?)")
	}
	if !errors.Is(err, want) {
		t.Fatalf("Invoke error did not wrap rand error: got %v", err)
	}
	if stub.gotParams.SystemPrompts != nil {
		t.Fatal("Invoke proceeded to call inner provider despite rand failure (regression: predictable marker nonce reached the wire)")
	}
}

func TestNewMarkerNonce_NoConstantFallbackInSource(t *testing.T) {
	src, err := os.ReadFile("kernel_inject.go")
	if err != nil {
		t.Skipf("source read: %v", err)
	}
	if strings.Contains(string(src), `"0000000000000000"`) {
		t.Fatal("kernel_inject.go reintroduced the SEC-080-005 zero-nonce fallback constant")
	}
}

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

// TestKernelPromptInjector_InnerExposesWrapped pins the #425 accessor:
// call sites that need to wrap an APIProvider with the agentic ToolLoop
// must be able to reach the underlying provider through the kernel-
// prompt wrapper. Without this, the type assertion
// `prov.(*APIProvider)` at pipeline.go:592 silently fails on every
// API-tier chat (the runtime always wires a kernel prompt at boot via
// registry.SetKernelPrompt).
func TestKernelPromptInjector_InnerExposesWrapped(t *testing.T) {
	stub := &stubProvider{}
	wrapped := NewKernelPromptInjector(stub, "KERNEL")
	if wrapped.Inner() != stub {
		t.Errorf("Inner() = %v, want stubProvider %v", wrapped.Inner(), stub)
	}
	if wrapped.Prompt() != "KERNEL" {
		t.Errorf("Prompt() = %q, want %q", wrapped.Prompt(), "KERNEL")
	}
}

// TestKernelPromptInjector_RewrapPreservesInvariant pins that the
// unwrap → mutate → re-wrap recipe round-trips cleanly: after stripping
// the injector, wrapping the inner with a fictional middleware, and
// re-wrapping with NewKernelPromptInjector(newChain, inj.Prompt()), the
// kernel prompt still lands as the sole SystemPrompts entry. This is
// the recipe pipeline.go uses to insert the ToolLoop without breaking
// §3.2 (kernel prompt is the first system-prompt entry).
func TestKernelPromptInjector_RewrapPreservesInvariant(t *testing.T) {
	stub := &stubProvider{}
	inj := NewKernelPromptInjector(stub, "KERNEL")

	// Simulate the call site: unwrap, leave the inner unchanged, re-wrap.
	innerProv := inj.Inner()
	rewrapped := NewKernelPromptInjector(innerProv, inj.Prompt())

	if _, err := rewrapped.Invoke(context.Background(), "p", Params{}, nil); err != nil {
		t.Fatal(err)
	}
	got := stub.gotParams.SystemPrompts
	if len(got) != 1 || got[0] != "KERNEL" {
		t.Errorf("re-wrap broke §3.2 invariant; SystemPrompts = %v, want [KERNEL]", got)
	}
}

// TestKernelInjector_UnwrapsToAPIProvider pins the #425 bug-fix pattern
// end-to-end at the type level: when SetKernelPrompt has wrapped an
// APIProvider, the call sites in pipeline.go and orchestrator.go can
// reach the underlying *APIProvider via Inner() without losing the
// kernel-prompt prefix on re-wrap. This is the exact sequence those
// sites apply before NewToolLoop, so a regression here would break the
// agentic tool loop on every API-tier chat.
func TestKernelInjector_UnwrapsToAPIProvider(t *testing.T) {
	api := NewAPIProvider("test-key", nil)
	inj := NewKernelPromptInjector(api, "KERNEL")

	// Step 1: unwrap (mirrors pipeline.go:592 after fix).
	var underlying Provider = inj
	var capturedInj *KernelPromptInjector
	if k, ok := underlying.(*KernelPromptInjector); ok {
		capturedInj = k
		underlying = k.Inner()
	}
	if capturedInj == nil {
		t.Fatal("KernelPromptInjector type assertion failed — Inner() unreachable from outside the package?")
	}

	// Step 2: assert the underlying is the *APIProvider we wrapped.
	apiProv, ok := underlying.(*APIProvider)
	if !ok {
		t.Fatalf("Inner() did not yield *APIProvider; got %T", underlying)
	}
	if apiProv != api {
		t.Fatal("Inner() returned a different *APIProvider than we wrapped")
	}

	// Step 3: re-wrap with the captured kernel prompt — this is the
	// final step in the fix pattern.
	rewrapped := NewKernelPromptInjector(apiProv, capturedInj.Prompt())
	if rewrapped.Prompt() != "KERNEL" {
		t.Errorf("rewrap lost kernel prompt; got %q", rewrapped.Prompt())
	}
	if rewrapped.Inner() != apiProv {
		t.Error("rewrap lost inner provider identity")
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

// TestKernelPromptInjector_SubstitutesNonceAcrossInputs pins SEC-002:
// the per-Invoke nonce must be substituted in the kernel prompt, in
// every caller-supplied system prompt, in the user prompt, and in
// every ConvMessage Content. After Invoke, no {NONCE} placeholder
// may remain in any of those strings.
func TestKernelPromptInjector_SubstitutesNonceAcrossInputs(t *testing.T) {
	stub := &stubProvider{}
	wrapped := NewKernelPromptInjector(stub,
		`KERNEL: <tool_output_{NONCE}> markers</tool_output_{NONCE}>`)

	params := Params{
		SystemPrompts: []string{
			`<capability_content_{NONCE} source="skill:foo">body</capability_content_{NONCE}>`,
			"plain prompt with no nonce",
		},
		ConvMessages: []ContextMessage{
			{Role: "tool", Content: `<tool_output_{NONCE}>previous-iter</tool_output_{NONCE}>`},
		},
	}
	prompt := `user said: <fetched_content_{NONCE}>page</fetched_content_{NONCE}>`

	if _, err := wrapped.Invoke(context.Background(), prompt, params, nil); err != nil {
		t.Fatal(err)
	}
	for i, sp := range stub.gotParams.SystemPrompts {
		if strings.Contains(sp, noncePlaceholder) {
			t.Errorf("SystemPrompts[%d] still carries {NONCE}: %q", i, sp)
		}
	}
	for i, m := range stub.gotParams.ConvMessages {
		if strings.Contains(m.Content, noncePlaceholder) {
			t.Errorf("ConvMessages[%d].Content still carries {NONCE}: %q", i, m.Content)
		}
	}
	// Kernel prompt is at index 0 of SystemPrompts after injection.
	kp := stub.gotParams.SystemPrompts[0]
	if !strings.Contains(kp, "KERNEL: <tool_output_") {
		t.Errorf("kernel prompt nonce substitution missing: %q", kp)
	}
	// First caller prompt also substituted — same nonce as kernel.
	cp1 := stub.gotParams.SystemPrompts[1]
	if !strings.Contains(cp1, "<capability_content_") {
		t.Errorf("caller prompt 1 nonce substitution missing: %q", cp1)
	}
	// Extract nonce from kernel and confirm caller prompt + ConvMessage
	// share the same nonce — binding is the whole point.
	idx := strings.Index(kp, "<tool_output_")
	if idx < 0 {
		t.Fatalf("kernel prompt malformed: %q", kp)
	}
	end := strings.Index(kp[idx:], ">")
	kernelNonce := kp[idx+len("<tool_output_") : idx+end]
	if !strings.Contains(cp1, kernelNonce) {
		t.Errorf("caller prompt missing kernel nonce %q: %q", kernelNonce, cp1)
	}
	convContent := stub.gotParams.ConvMessages[0].Content
	if !strings.Contains(convContent, kernelNonce) {
		t.Errorf("ConvMessage missing kernel nonce %q: %q", kernelNonce, convContent)
	}
}

// TestKernelPromptInjector_NoncesDifferAcrossInvokes pins that
// successive Invokes get distinct nonces. Without this, an attacker
// who learns one Invoke's nonce could craft closing-tag bytes for a
// future tool output. crypto/rand backs newMarkerNonce.
func TestKernelPromptInjector_NoncesDifferAcrossInvokes(t *testing.T) {
	stub := &stubProvider{}
	wrapped := NewKernelPromptInjector(stub, `K_{NONCE}`)

	seen := make(map[string]struct{}, 50)
	for i := 0; i < 50; i++ {
		if _, err := wrapped.Invoke(context.Background(), "p", Params{}, nil); err != nil {
			t.Fatal(err)
		}
		kp := stub.gotParams.SystemPrompts[0]
		// kernel prompt looks like "K_<nonce>"
		if !strings.HasPrefix(kp, "K_") {
			t.Fatalf("unexpected kernel prompt shape: %q", kp)
		}
		nonce := strings.TrimPrefix(kp, "K_")
		if len(nonce) != 16 {
			t.Fatalf("nonce wrong length: %q", nonce)
		}
		if _, dup := seen[nonce]; dup {
			t.Fatalf("duplicate nonce across Invokes: %q", nonce)
		}
		seen[nonce] = struct{}{}
	}
}

// TestKernelPromptInjector_NoncePropagatedViaContext pins that
// downstream providers (ToolLoop) can read the per-Invoke nonce from
// ctx so loop-local wraps materialise with the same nonce.
func TestKernelPromptInjector_NoncePropagatedViaContext(t *testing.T) {
	var seenNonce string
	capturer := stubProviderFn(func(ctx context.Context, _ string, _ Params, _ OnProgress) (*Result, error) {
		seenNonce = markerNonceFromCtx(ctx)
		return &Result{}, nil
	})
	wrapped := NewKernelPromptInjector(capturer, "K")

	if _, err := wrapped.Invoke(context.Background(), "p", Params{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(seenNonce) != 16 {
		t.Errorf("expected 16-hex nonce in ctx, got %q", seenNonce)
	}
}

// stubProviderFn is a Provider whose Invoke runs an arbitrary fn —
// used to capture the per-Invoke ctx state.
type stubProviderFn func(ctx context.Context, prompt string, params Params, onProgress OnProgress) (*Result, error)

func (f stubProviderFn) Invoke(ctx context.Context, prompt string, params Params, onProgress OnProgress) (*Result, error) {
	return f(ctx, prompt, params, onProgress)
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
