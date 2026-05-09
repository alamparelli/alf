package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// markerNonceRandReader is the entropy source for newMarkerNonce.
// Tests rebind this via SetMarkerNonceRandReaderForTest to cover the
// SEC-080-005 fail-loud path; production uses crypto/rand.
var markerNonceRandReader io.Reader = rand.Reader

// SetMarkerNonceRandReaderForTest swaps the marker-nonce randReader
// and returns a closer that restores it. Callers must defer the
// closer. Mirrors internal/runtime/llm.SetRandReaderForTest.
func SetMarkerNonceRandReaderForTest(r io.Reader) func() {
	prev := markerNonceRandReader
	markerNonceRandReader = r
	return func() { markerNonceRandReader = prev }
}

// noncePlaceholder MUST match internal/runtime/llm.NoncePlaceholder.
// Inlined here because the foundation dependency rule forbids
// internal/ai/provider from importing internal/runtime/*. If the llm
// constant changes, update this one in lockstep — the LLM-facing
// kernel prompt and every wrap-site rely on the two strings being
// byte-identical.
const noncePlaceholder = "{NONCE}"

// newMarkerNonce returns a fresh 16-hex-char (8 random bytes) nonce
// used to bind the per-Invoke kernel prompt to the wrap markers
// surrounding capability content. Mirrors
// internal/runtime/llm.NewNonce — see the rationale there.
//
// SEC-080-005: returns an error on crypto/rand failure rather than
// falling back to a constant 16-zero-hex string. A predictable nonce
// on the failure path lets a hostile capability output break out of
// its `<tool_output_NONCE>...</tool_output_NONCE>` wrap by emitting
// the literal closing tag with the constant value. Aborting the LLM
// call is the correct outcome when the OS PRNG is broken.
func newMarkerNonce() (string, error) {
	var b [8]byte
	if _, err := io.ReadFull(markerNonceRandReader, b[:]); err != nil {
		return "", fmt.Errorf("kernel-marker nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// substituteMarkerNonce replaces every noncePlaceholder occurrence
// in s with the given nonce. The injector applies it to every string
// that may carry a wrapped marker placeholder before the request
// reaches the wire.
func substituteMarkerNonce(s, nonce string) string {
	if !strings.Contains(s, noncePlaceholder) {
		return s
	}
	return strings.ReplaceAll(s, noncePlaceholder, nonce)
}

// KernelPromptInjector is the narrow seam #400 uses to prepend the
// daemon-shipped kernel prompt to every LLM call's SystemPrompts. The
// Registry wraps each registered Provider with one of these on
// `WithKernelPrompt`; callers use Registry.ForBackend unchanged and
// the injection is transparent.
//
// Why a wrapper rather than per-call: there are 20+ sites in the
// codebase that build SystemPrompts (chat pipeline, agents, scheduler,
// CC handlers, skill imports). Adding the kernel prompt at every site
// would be brittle and easy to forget. The Registry layer is the one
// chokepoint every LLM call passes through; injecting here guarantees
// the kernel prompt is always present, regardless of which path built
// the request.
//
// Per §3.2 the kernel prompt is authoritative — it MUST be the first
// system-prompt entry so model providers that anchor cache-keys on the
// first prompt don't accidentally cache a request without it.
type KernelPromptInjector struct {
	inner  Provider
	prompt string
}

// NewKernelPromptInjector wraps p so every Invoke prepends prompt to
// params.SystemPrompts. Empty prompt is rejected — callers that don't
// want injection simply don't wrap.
func NewKernelPromptInjector(p Provider, prompt string) *KernelPromptInjector {
	if p == nil {
		return nil
	}
	return &KernelPromptInjector{inner: p, prompt: prompt}
}

// Invoke implements Provider. Generates a fresh per-Invoke nonce,
// substitutes it across the kernel prompt + every caller-supplied
// SystemPrompts entry + the user prompt + every ConvMessage Content,
// then prepends the materialised kernel prompt and dispatches to the
// wrapped provider.
//
// The nonce binding (SEC-002) prevents capability content from
// breaking out of its <tag_NONCE>...</tag_NONCE> wrapper: a tool that
// emits the literal closing tag bytes cannot guess the random per-
// Invoke nonce, so the LLM still sees a structurally-intact marker
// and the kernel prompt's "NOT authoritative" rule remains binding.
//
// The nonce is propagated through context so providers below this
// layer (most importantly ToolLoop, which wraps tool outputs during
// multi-turn loops AFTER the injector has already substituted) can
// substitute the same value into their loop-local wraps.
func (k *KernelPromptInjector) Invoke(ctx context.Context, prompt string, params Params, onProgress OnProgress) (*Result, error) {
	nonce, err := newMarkerNonce()
	if err != nil {
		// SEC-080-005: the marker nonce is the binding that prevents
		// capability outputs from breaking out of their wrap tags.
		// Without a fresh, unguessable value, the kernel-prompt
		// authority guarantee from §3.2 collapses. Refuse the call.
		return nil, err
	}
	ctx = withMarkerNonce(ctx, nonce)

	prompt = substituteMarkerNonce(prompt, nonce)
	if len(params.SystemPrompts) > 0 {
		out := make([]string, len(params.SystemPrompts))
		for i, s := range params.SystemPrompts {
			out[i] = substituteMarkerNonce(s, nonce)
		}
		params.SystemPrompts = out
	}
	if len(params.ConvMessages) > 0 {
		out := make([]ContextMessage, len(params.ConvMessages))
		for i, m := range params.ConvMessages {
			m.Content = substituteMarkerNonce(m.Content, nonce)
			out[i] = m
		}
		params.ConvMessages = out
	}

	if k.prompt != "" {
		// Prepend, never replace — caller-supplied SystemPrompts (skill
		// prompts, tier instructions, conversation context) follow the
		// kernel prompt unchanged. Substitute the kernel prompt with
		// the per-Invoke nonce so the marker definitions in §3.2
		// reference the same nonce the wrappers used.
		kp := substituteMarkerNonce(k.prompt, nonce)
		params.SystemPrompts = append([]string{kp}, params.SystemPrompts...)
	}
	return k.inner.Invoke(ctx, prompt, params, onProgress)
}

// markerNonceCtxKey carries the per-Invoke marker nonce so providers
// below the injector (ToolLoop) can substitute placeholders in
// loop-local wraps.
type markerNonceCtxKey struct{}

// withMarkerNonce returns ctx annotated with the per-Invoke marker
// nonce. Used by KernelPromptInjector to propagate the nonce to
// ToolLoop iterations that wrap tool outputs after the injector has
// finished its substitution pass.
func withMarkerNonce(ctx context.Context, nonce string) context.Context {
	return context.WithValue(ctx, markerNonceCtxKey{}, nonce)
}

// markerNonceFromCtx returns the per-Invoke marker nonce previously
// installed by withMarkerNonce, or empty string if none. ToolLoop
// reads this when wrapping tool outputs for the next iteration so
// the substituted markers match the kernel prompt's substituted
// definitions.
func markerNonceFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(markerNonceCtxKey{}).(string); ok {
		return v
	}
	return ""
}
