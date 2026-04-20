package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// HTTPEmbedder is an HTTP client for a remote embedding service.
// Follows the same registration pattern as voice.Transcriber (whisper).
type HTTPEmbedder struct {
	baseURL    string
	token      string
	instanceID string
	secret     string
	client     *http.Client
	dims       int
	mu         sync.Mutex
	ready      bool
}

// NewHTTPEmbedder creates a new HTTP embedding client. Call Start() to register.
func NewHTTPEmbedder(baseURL, instanceID, secret string, timeout time.Duration) *HTTPEmbedder {
	if instanceID == "" {
		instanceID = "alf-default"
	}
	return &HTTPEmbedder{
		baseURL:    baseURL,
		instanceID: instanceID,
		secret:     secret,
		client:     &http.Client{Timeout: timeout},
	}
}

type registerRequest struct {
	InstanceID string `json:"instance_id"`
	Secret     string `json:"secret"`
}

type registerResponse struct {
	Token string `json:"token"`
	Dims  int    `json:"dims"`
}

type embedRequest struct {
	Text string `json:"text"`
}

type embedResponse struct {
	Embedding []float32 `json:"embedding"`
}

type embedBatchRequest struct {
	Texts []string `json:"texts"`
}

type embedBatchResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Start registers this instance with the embed service and obtains a bearer token.
func (e *HTTPEmbedder) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ready {
		return nil
	}

	return e.register()
}

func (e *HTTPEmbedder) register() error {
	body, _ := json.Marshal(registerRequest{
		InstanceID: e.instanceID,
		Secret:     e.secret,
	})

	resp, err := e.client.Post(e.baseURL+"/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("register with embed service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("register: invalid shared secret")
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("register: unexpected status %d: %s", resp.StatusCode, respBody)
	}

	var result registerResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("register: decode response: %w", err)
	}

	e.token = result.Token
	e.dims = result.Dims
	e.ready = true
	log.Printf("embed: registered (instance=%s, dims=%d)", e.instanceID, e.dims)
	return nil
}

// Stop is a no-op for HTTP client.
func (e *HTTPEmbedder) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ready = false
}

// IsReady returns true if registered with the embed service.
func (e *HTTPEmbedder) IsReady() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ready
}

// Dims returns the embedding dimension count obtained during registration.
func (e *HTTPEmbedder) Dims() int {
	return e.dims
}

// Embed generates an embedding for storage (passage).
func (e *HTTPEmbedder) Embed(text string) ([]float32, error) {
	return e.doEmbed("/embed", text)
}

// EmbedQuery generates an embedding for search (query).
func (e *HTTPEmbedder) EmbedQuery(text string) ([]float32, error) {
	return e.doEmbed("/embed-query", text)
}

func (e *HTTPEmbedder) doEmbed(path, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.ready {
		return nil, fmt.Errorf("embed client not ready")
	}

	body, _ := json.Marshal(embedRequest{Text: text})
	result, err := e.doRequest(path, body)
	if err != nil {
		return nil, err
	}

	var resp embedResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}
	return resp.Embedding, nil
}

// EmbedBatch generates embeddings for multiple texts.
func (e *HTTPEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.ready {
		return nil, fmt.Errorf("embed client not ready")
	}

	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	body, _ := json.Marshal(embedBatchRequest{Texts: texts})
	result, err := e.doRequest("/embed-batch", body)
	if err != nil {
		return nil, err
	}

	var resp embedBatchResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("embed-batch: decode response: %w", err)
	}
	return resp.Embeddings, nil
}

// doRequest sends an authenticated POST. Re-registers on 401.
func (e *HTTPEmbedder) doRequest(path string, body []byte) ([]byte, error) {
	result, err := e.rawRequest(path, body)
	if err != nil && isAuthErr(err) {
		log.Printf("embed: token rejected, re-registering...")
		if regErr := e.register(); regErr != nil {
			return nil, fmt.Errorf("re-register failed: %w", regErr)
		}
		return e.rawRequest(path, body)
	}
	return result, err
}

func (e *HTTPEmbedder) rawRequest(path string, body []byte) ([]byte, error) {
	req, err := http.NewRequest("POST", e.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.token)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, &embedAuthError{}
	}
	if resp.StatusCode == 503 {
		return nil, fmt.Errorf("embed service not ready (503)")
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("embed: status %d: %s", resp.StatusCode, respBody)
	}

	return io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MB max
}

type embedAuthError struct{}

func (e *embedAuthError) Error() string { return "embed: authentication failed" }

func isAuthErr(err error) bool {
	_, ok := err.(*embedAuthError)
	return ok
}
