package provider

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// APIClassifier implements the Classifier interface using an APIProvider
// instead of a persistent Claude CLI subprocess.
type APIClassifier struct {
	provider     *APIProvider
	history      *History
	systemPrompt string
	sessionKey   string
	mu           sync.Mutex
}

// NewAPIClassifier creates a classifier backed by an API provider.
func NewAPIClassifier(prov *APIProvider, history *History, systemPrompt string) *APIClassifier {
	return &APIClassifier{
		provider:     prov,
		history:      history,
		systemPrompt: systemPrompt,
		sessionKey:   "classifier",
	}
}

// Classify sends a message to the API and returns the classification result.
func (c *APIClassifier) Classify(ctx context.Context, message string) (*ClassifyResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	params := Params{
		SystemPrompts: []string{c.systemPrompt},
		SessionKey:    c.sessionKey,
	}

	result, err := c.provider.Invoke(ctx, message, params, nil)
	if err != nil {
		return nil, fmt.Errorf("api classify: %w", err)
	}

	return &ClassifyResult{
		Tier:     result.Text,
		Response: "",
	}, nil
}

// InjectContext sends a post-response context summary to the classifier history.
func (c *APIClassifier) InjectContext(tierName, access, summary string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	msg := fmt.Sprintf("[%s (%s) responded: %s]", tierName, access, summary)
	if c.history != nil {
		c.history.Append(c.sessionKey, Message{Role: "assistant", Content: msg})
	}
	return nil
}

// Restart clears the classifier history.
func (c *APIClassifier) Restart() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.history != nil {
		c.history.Clear(c.sessionKey)
	}
	log.Printf("api-classifier: history cleared")
	return nil
}

// Close is a no-op for API classifiers.
func (c *APIClassifier) Close() error { return nil }
