package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// APIProvider implements the Provider interface via HTTP calls to an
// OpenAI-compatible API (OpenRouter, Ollama, OpenAI, Groq, etc.).
type APIProvider struct {
	name         string
	apiKey       string
	baseURL      string
	headers      map[string]string
	defaultModel string
	maxTokens    int
	auth         string // "bearer" or "none"
	history      *History
	client       *http.Client
}

// APIProviderConfig holds configuration for creating an APIProvider.
type APIProviderConfig struct {
	Name         string
	BaseURL      string
	APIKey       string            // resolved from vault or secret at startup
	Headers      map[string]string // custom headers (e.g. HTTP-Referer, X-Title)
	DefaultModel string
	MaxTokens    int    // 0 = 4096
	Auth         string // "bearer" (default), "none" (Ollama)
}

// NewAPIProviderFromConfig creates a new APIProvider from a config.
func NewAPIProviderFromConfig(cfg APIProviderConfig, history *History) *APIProvider {
	auth := cfg.Auth
	if auth == "" {
		auth = "bearer"
	}
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	return &APIProvider{
		name:         cfg.Name,
		apiKey:       cfg.APIKey,
		baseURL:      cfg.BaseURL,
		headers:      cfg.Headers,
		defaultModel: cfg.DefaultModel,
		maxTokens:    maxTokens,
		auth:         auth,
		history:      history,
		client:       &http.Client{Timeout: 5 * time.Minute},
	}
}

// NewAPIProvider creates an APIProvider for OpenRouter (backward compat).
// Deprecated: use NewAPIProviderFromConfig.
func NewAPIProvider(apiKey string, history *History) *APIProvider {
	return NewAPIProviderFromConfig(APIProviderConfig{
		Name:         "openrouter",
		BaseURL:      "https://openrouter.ai/api/v1",
		APIKey:       apiKey,
		Headers:      map[string]string{"HTTP-Referer": "https://github.com/alamparelli/alf", "X-Title": "ALF"},
		DefaultModel: "anthropic/claude-haiku-4-5",
		Auth:         "bearer",
	}, history)
}

// Name returns the backend name.
func (p *APIProvider) Name() string              { return p.name }
func (p *APIProvider) BaseURL() string            { return p.baseURL }
func (p *APIProvider) Auth() string               { return p.auth }
func (p *APIProvider) APIKey() string             { return p.apiKey }
func (p *APIProvider) Headers() map[string]string { return p.headers }

// IsOllamaCompat returns true if this backend appears to be Ollama
// (no auth required, typically local).
func (p *APIProvider) IsOllamaCompat() bool {
	return p.auth == "none"
}

// IsDirectOpenAI returns true if this backend points directly at the OpenAI API
// (not a proxy like OpenRouter that serves multiple providers).
func (p *APIProvider) IsDirectOpenAI() bool {
	return strings.Contains(p.baseURL, "api.openai.com")
}

// apiRequest is the OpenAI chat completions request body.
type apiRequest struct {
	Model              string            `json:"model"`
	Messages           []apiMessage      `json:"messages"`
	Stream             bool              `json:"stream"`
	MaxTokens          int               `json:"max_tokens,omitempty"`
	Tools              json.RawMessage   `json:"tools,omitempty"`
	ToolChoice         string            `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool             `json:"parallel_tool_calls,omitempty"`
	Reasoning          *apiReasoning     `json:"reasoning,omitempty"`
	StreamOptions      *apiStreamOpts    `json:"stream_options,omitempty"`
}

// apiReasoning configures reasoning/thinking for OpenRouter-compatible models.
type apiReasoning struct {
	Effort string `json:"effort,omitempty"`    // "low", "medium", "high"
	Enabled bool  `json:"enabled,omitempty"`   // true to enable with defaults
}

type apiStreamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type apiMessage struct {
	Role       string        `json:"-"`
	Content    string        `json:"-"`
	ToolCalls  []apiToolCall `json:"-"`
	ToolCallID string        `json:"-"`
}

// MarshalJSON implements custom JSON encoding for OpenAI-compatible messages.
// - Assistant messages with tool_calls emit "content": null (required by OpenRouter).
// - Tool result messages always emit "content" (even if empty).
// - Other messages omit "content" when empty.
func (m apiMessage) MarshalJSON() ([]byte, error) {
	msg := make(map[string]any)
	msg["role"] = m.Role

	switch {
	case len(m.ToolCalls) > 0:
		// Assistant with tool_calls: content must be null, not absent.
		msg["content"] = nil
		msg["tool_calls"] = m.ToolCalls
		if m.Content != "" {
			msg["content"] = m.Content
		}
	case m.ToolCallID != "":
		// Tool result: always include content + tool_call_id.
		msg["content"] = m.Content
		msg["tool_call_id"] = m.ToolCallID
	default:
		// System/user/assistant text: omit content only if empty.
		if m.Content != "" {
			msg["content"] = m.Content
		}
	}
	return json.Marshal(msg)
}

// UnmarshalJSON implements custom JSON decoding for apiMessage.
func (m *apiMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role       string        `json:"role"`
		Content    string        `json:"content"`
		ToolCalls  []apiToolCall `json:"tool_calls,omitempty"`
		ToolCallID string        `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.Role = raw.Role
	m.Content = raw.Content
	m.ToolCalls = raw.ToolCalls
	m.ToolCallID = raw.ToolCallID
	return nil
}

type apiToolCall struct {
	ID       string        `json:"id"`
	Type     string        `json:"type"`
	Function apiToolCallFn `json:"function"`
}

type apiToolCallFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// apiStreamResult is the parsed result of a streamed API response.
type apiStreamResult struct {
	Text         string
	ToolCalls    []apiToolCall
	FinishReason string // "stop", "tool_calls", "length"
	Model        string
	InputTokens  int
	OutputTokens int
}

// BuildMessages constructs the messages array from a prompt and params.
// Exported so ToolLoop can reuse the message building logic.
func (p *APIProvider) BuildMessages(prompt string, params Params) []apiMessage {
	var messages []apiMessage

	// System prompts.
	if len(params.SystemPrompts) > 0 {
		combined := strings.Join(params.SystemPrompts, "\n\n")
		messages = append(messages, apiMessage{Role: "system", Content: combined})
	}

	// Conversation history: prefer unified ConvMessages, fall back to per-key History.
	if len(params.ConvMessages) > 0 {
		for _, m := range params.ConvMessages {
			msg := apiMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
			for _, tc := range m.ToolCalls {
				msg.ToolCalls = append(msg.ToolCalls, apiToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: apiToolCallFn{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
			messages = append(messages, msg)
		}
	} else if params.SessionKey != "" && p.history != nil {
		hist := p.history.Get(params.SessionKey)
		for _, m := range hist {
			messages = append(messages, apiMessage{Role: m.Role, Content: m.Content})
		}
	}

	// Current user message.
	messages = append(messages, apiMessage{Role: "user", Content: prompt})
	return messages
}

// DoRequest sends a request with pre-built messages and optional tools,
// returning a rich result with tool_calls if present.
func (p *APIProvider) DoRequest(ctx context.Context, messages []apiMessage, model string, tools json.RawMessage, effort string, onProgress OnProgress) (*apiStreamResult, error) {
	reqBody := apiRequest{
		Model:         model,
		Messages:      messages,
		Stream:        true,
		MaxTokens:     p.maxTokens,
		Tools:         tools,
		StreamOptions: &apiStreamOpts{IncludeUsage: true},
	}
	// Disable parallel tool calls for non-OpenAI backends.
	// Many providers (xAI/Grok, Google, Ollama) generate malformed JSON when
	// parallel_tool_calls is enabled. Only direct OpenAI handles it reliably.
	if !p.IsDirectOpenAI() && len(tools) > 0 {
		f := false
		reqBody.ParallelToolCalls = &f
	}
	// Ollama-specific: set tool_choice and disable stream_options.
	if p.IsOllamaCompat() && len(tools) > 0 {
		reqBody.ToolChoice = "auto"
		reqBody.StreamOptions = nil
	}
	// Reasoning support (OpenRouter / OpenAI-compatible).
	if effort != "" && !p.IsOllamaCompat() {
		reqBody.Reasoning = &apiReasoning{Effort: effort, Enabled: true}
	}
	return p.doStreamRequest(ctx, reqBody, onProgress, 0)
}

// Invoke sends a prompt to the API and returns the result.
// Uses the full streaming parser so tool_calls are handled gracefully
// even when no ToolLoop is configured (tool_calls text is returned as-is).
func (p *APIProvider) Invoke(ctx context.Context, prompt string, params Params, onProgress OnProgress) (*Result, error) {
	model := params.Model
	if model == "" {
		model = p.defaultModel
	}
	if model == "" {
		model = "anthropic/claude-haiku-4-5"
	}

	log.Printf("api[%s]: invoke (model=%s, prompt=%d chars)", p.name, model, len(prompt))
	logLLMCtx(ctx, "invoke", map[string]any{
		"provider": "api", "backend": p.name, "model": model,
		"prompt_len": len(prompt), "prompt": trunc(prompt, 2000),
	})

	messages := p.BuildMessages(prompt, params)

	reqBody := apiRequest{
		Model:         model,
		Messages:      messages,
		Stream:        true,
		MaxTokens:     p.maxTokens,
		StreamOptions: &apiStreamOpts{IncludeUsage: true},
	}
	// Reasoning support (OpenRouter / OpenAI-compatible).
	if params.Effort != "" && !p.IsOllamaCompat() {
		reqBody.Reasoning = &apiReasoning{Effort: params.Effort, Enabled: true}
	}

	resp, err := p.doStreamRequest(ctx, reqBody, onProgress, 0)
	if err != nil {
		return nil, err
	}

	text := resp.Text
	// If the model returned tool_calls but no text (no ToolLoop to handle them),
	// surface the tool call info as text instead of returning empty.
	if text == "" && len(resp.ToolCalls) > 0 {
		var parts []string
		for _, tc := range resp.ToolCalls {
			parts = append(parts, fmt.Sprintf("[tool_call: %s(%s)]", tc.Function.Name, tc.Function.Arguments))
		}
		text = "Model attempted tool calls but no tool loop is configured: " + strings.Join(parts, ", ")
		log.Printf("api[%s]: model returned tool_calls without ToolLoop: %v", p.name, parts)
	}

	result := &Result{
		Text:         text,
		Model:        model,
		InputTokens:  resp.InputTokens,
		OutputTokens: resp.OutputTokens,
	}

	logLLMCtx(ctx, "result", map[string]any{
		"provider": "api", "backend": p.name, "model": model,
		"input_tokens": resp.InputTokens, "output_tokens": resp.OutputTokens,
		"response_len": len(text), "response": trunc(text, 2000),
	})

	// Append to legacy history (only for keyed sessions without ConvMessages).
	if len(params.ConvMessages) == 0 && params.SessionKey != "" && p.history != nil {
		p.history.Append(params.SessionKey, Message{Role: "user", Content: prompt})
		p.history.Append(params.SessionKey, Message{Role: "assistant", Content: result.Text})
	}

	return result, nil
}

// doStreamRequest sends a request and parses the SSE response into an
// apiStreamResult that includes tool_calls if any.
func (p *APIProvider) doStreamRequest(ctx context.Context, reqBody apiRequest, onProgress OnProgress, attempt int) (*apiStreamResult, error) {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if p.auth != "none" && p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api request (%s): %w", p.name, err)
	}
	defer resp.Body.Close()

	// Rate limit retry.
	if resp.StatusCode == 429 && attempt < 3 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("api[%s]: 429 rate limited (attempt %d/3): %s", p.name, attempt+1, truncBody(body))
		wait := time.Duration(1<<uint(attempt)) * time.Second
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return p.doStreamRequest(ctx, reqBody, onProgress, attempt+1)
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api[%s] error %d: %s", p.name, resp.StatusCode, truncBody(body))
	}

	// Parse SSE stream with tool_calls support.
	var resultText strings.Builder
	toolCalls := make(map[int]*apiToolCall) // keyed by index for incremental assembly
	var finishReason string
	var inputTokens, outputTokens int

	// Try to extract usage from response headers (OpenRouter sends these on non-streaming
	// responses; for streaming they may appear after the body is consumed — see SSE parsing below).
	if v := resp.Header.Get("X-Prompt-Tokens"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			inputTokens = n
		}
	}
	if v := resp.Header.Get("X-Completion-Tokens"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			outputTokens = n
		}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]
		if payload == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   *string `json:"content"`
					Reasoning *string `json:"reasoning"` // OpenRouter reasoning delta
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id,omitempty"`
						Type     string `json:"type,omitempty"`
						Function struct {
							Name      string `json:"name,omitempty"`
							Arguments string `json:"arguments,omitempty"`
						} `json:"function"`
					} `json:"tool_calls,omitempty"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage,omitempty"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}

		// Extract usage from final SSE chunk (OpenAI/OpenRouter stream_options).
		if chunk.Usage != nil {
			if chunk.Usage.PromptTokens > 0 {
				inputTokens = chunk.Usage.PromptTokens
			}
			if chunk.Usage.CompletionTokens > 0 {
				outputTokens = chunk.Usage.CompletionTokens
			}
		}

		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil {
				finishReason = *choice.FinishReason
			}
			// Reasoning/thinking delta (OpenRouter).
			if choice.Delta.Reasoning != nil && *choice.Delta.Reasoning != "" {
				if onProgress != nil {
					onProgress(StreamEvent{Type: "thinking", Text: *choice.Delta.Reasoning})
				}
			}
			if choice.Delta.Content != nil && *choice.Delta.Content != "" {
				resultText.WriteString(*choice.Delta.Content)
				if onProgress != nil {
					onProgress(StreamEvent{Type: "text_delta", Text: *choice.Delta.Content})
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := toolCalls[tc.Index]
				if !ok {
					existing = &apiToolCall{Type: "function"}
					toolCalls[tc.Index] = existing
				}
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Function.Name += tc.Function.Name
					if onProgress != nil {
						onProgress(StreamEvent{Type: "tool_use", Detail: existing.Function.Name})
					}
				}
				if tc.Function.Arguments != "" {
					existing.Function.Arguments += tc.Function.Arguments
					if onProgress != nil {
						onProgress(StreamEvent{Type: "tool_input", Detail: existing.Function.Name, Text: tc.Function.Arguments})
					}
				}
			}
		}
	}

	duration := time.Since(start)
	text := strings.TrimSpace(resultText.String())

	// Collect tool calls in order.
	var calls []apiToolCall
	for i := 0; i < len(toolCalls); i++ {
		if tc, ok := toolCalls[i]; ok {
			calls = append(calls, *tc)
		}
	}

	log.Printf("api[%s]: response %dms %d chars %d tool_calls finish=%s model=%s",
		p.name, duration.Milliseconds(), len(text), len(calls), finishReason, reqBody.Model)

	// For non-tool responses, empty text is an error. Retry once.
	if text == "" && len(calls) == 0 {
		if attempt < 1 {
			log.Printf("api[%s]: empty response (attempt %d), retrying", p.name, attempt)
			return p.doStreamRequest(ctx, reqBody, onProgress, attempt+1)
		}
		return nil, fmt.Errorf("api[%s] returned empty response", p.name)
	}

	return &apiStreamResult{
		Text:         text,
		ToolCalls:    calls,
		FinishReason: finishReason,
		Model:        reqBody.Model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, nil
}

func truncBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
