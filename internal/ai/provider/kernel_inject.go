package provider

import "context"

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

// Invoke implements Provider. Prepends the kernel prompt and dispatches
// to the wrapped provider.
func (k *KernelPromptInjector) Invoke(ctx context.Context, prompt string, params Params, onProgress OnProgress) (*Result, error) {
	if k.prompt != "" {
		// Prepend, never replace — caller-supplied SystemPrompts (skill
		// prompts, tier instructions, conversation context) follow the
		// kernel prompt unchanged.
		params.SystemPrompts = append([]string{k.prompt}, params.SystemPrompts...)
	}
	return k.inner.Invoke(ctx, prompt, params, onProgress)
}
