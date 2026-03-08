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
	"strings"
	"time"
)

// APIProvider implements the Provider interface via HTTP calls to an
// OpenAI-compatible API (e.g. OpenRouter).
type APIProvider struct {
	apiKey  string
	baseURL string
	history *History
	client  *http.Client
}

// NewAPIProvider creates a new APIProvider for OpenRouter.
func NewAPIProvider(apiKey string, history *History) *APIProvider {
	return &APIProvider{
		apiKey:  apiKey,
		baseURL: "https://openrouter.ai/api/v1",
		history: history,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

// apiRequest is the OpenAI chat completions request body.
type apiRequest struct {
	Model     string       `json:"model"`
	Messages  []apiMessage `json:"messages"`
	Stream    bool         `json:"stream"`
	MaxTokens int          `json:"max_tokens,omitempty"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Invoke sends a prompt to the API and returns the result.
func (p *APIProvider) Invoke(ctx context.Context, prompt string, params Params, onProgress OnProgress) (*Result, error) {
	model := params.Model
	if model == "" {
		model = "anthropic/claude-haiku-4-5"
	}

	// Build messages array.
	var messages []apiMessage

	// System prompts.
	if len(params.SystemPrompts) > 0 {
		combined := strings.Join(params.SystemPrompts, "\n\n")
		messages = append(messages, apiMessage{Role: "system", Content: combined})
	}

	// History (only for keyed sessions, not stateless classify calls).
	if params.SessionKey != "" && p.history != nil {
		hist := p.history.Get(params.SessionKey)
		for _, m := range hist {
			messages = append(messages, apiMessage{Role: m.Role, Content: m.Content})
		}
	}

	// Current user message.
	messages = append(messages, apiMessage{Role: "user", Content: prompt})

	maxTokens := 4096
	reqBody := apiRequest{
		Model:     model,
		Messages:  messages,
		Stream:    true,
		MaxTokens: maxTokens,
	}

	result, err := p.doRequest(ctx, reqBody, onProgress)
	if err != nil {
		return nil, err
	}

	// Append to history.
	if params.SessionKey != "" && p.history != nil {
		p.history.Append(params.SessionKey, Message{Role: "user", Content: prompt})
		p.history.Append(params.SessionKey, Message{Role: "assistant", Content: result.Text})
	}

	result.Model = model
	return result, nil
}

func (p *APIProvider) doRequest(ctx context.Context, reqBody apiRequest, onProgress OnProgress) (*Result, error) {
	return p.doRequestWithRetry(ctx, reqBody, onProgress, 0)
}

func (p *APIProvider) doRequestWithRetry(ctx context.Context, reqBody apiRequest, onProgress OnProgress, attempt int) (*Result, error) {
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/alamparelli/alf")
	req.Header.Set("X-Title", "ALF")

	start := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api request: %w", err)
	}
	defer resp.Body.Close()

	// Rate limit retry.
	if resp.StatusCode == 429 && attempt < 3 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("api: 429 rate limited (attempt %d/3): %s", attempt+1, truncBody(body))
		wait := time.Duration(1<<uint(attempt)) * time.Second
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return p.doRequestWithRetry(ctx, reqBody, onProgress, attempt+1)
	}

	// Context overflow: truncate history and retry once.
	if resp.StatusCode == 400 && attempt == 0 {
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		if strings.Contains(bodyStr, "context") && (strings.Contains(bodyStr, "length") || strings.Contains(bodyStr, "too long") || strings.Contains(bodyStr, "maximum")) {
			log.Printf("api: context overflow, truncating history and retrying")
			// Drop first half of non-system messages.
			var trimmed []apiMessage
			for _, m := range reqBody.Messages {
				if m.Role == "system" {
					trimmed = append(trimmed, m)
				}
			}
			var nonSystem []apiMessage
			for _, m := range reqBody.Messages {
				if m.Role != "system" {
					nonSystem = append(nonSystem, m)
				}
			}
			half := len(nonSystem) / 2
			if half%2 != 0 {
				half++ // keep pairs aligned
			}
			if half < len(nonSystem) {
				trimmed = append(trimmed, nonSystem[half:]...)
			} else {
				// Keep at least the last message.
				trimmed = append(trimmed, nonSystem[len(nonSystem)-1])
			}
			reqBody.Messages = trimmed
			return p.doRequestWithRetry(ctx, reqBody, onProgress, 1)
		}
	}

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("api error %d: %s", resp.StatusCode, truncBody(body))
	}

	// Parse SSE stream.
	var resultText strings.Builder
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
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				resultText.WriteString(choice.Delta.Content)
				if onProgress != nil {
					onProgress(StreamEvent{Type: "text_delta", Text: choice.Delta.Content})
				}
			}
		}
	}

	duration := time.Since(start)
	text := strings.TrimSpace(resultText.String())
	log.Printf("api: response %dms %d chars model=%s", duration.Milliseconds(), len(text), reqBody.Model)

	if text == "" {
		return nil, fmt.Errorf("api returned empty response")
	}

	return &Result{
		Text:  text,
		Model: reqBody.Model,
	}, nil
}

func truncBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
