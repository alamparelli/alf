package voice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Transcriber is an HTTP client for the whisper-service.
// It replaces the previous Python subprocess and whisper.cpp CGO backends
// with a single platform-independent implementation.
type Transcriber struct {
	baseURL    string
	token      string
	instanceID string
	secret     string
	client     *http.Client
	mu         sync.Mutex
	ready      bool
}

// New creates a new Transcriber HTTP client. Call Start() to register with the whisper-service.
func New(baseURL, instanceID, secret string, timeout time.Duration) (*Transcriber, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("whisper service URL is required")
	}
	if secret == "" {
		return nil, fmt.Errorf("whisper shared secret is required")
	}
	if instanceID == "" {
		instanceID = "alf-default"
	}
	return &Transcriber{
		baseURL:    baseURL,
		instanceID: instanceID,
		secret:     secret,
		client:     &http.Client{Timeout: timeout},
	}, nil
}

// Start registers this instance with the whisper-service and obtains a bearer token.
func (t *Transcriber) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ready {
		return nil
	}

	return t.register()
}

func (t *Transcriber) register() error {
	body, _ := json.Marshal(map[string]string{
		"instance_id": t.instanceID,
		"secret":      t.secret,
	})

	resp, err := t.client.Post(t.baseURL+"/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("register with whisper service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("register: invalid shared secret")
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register: unexpected status %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Token      string `json:"token"`
		InstanceID string `json:"instance_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("register: decode response: %w", err)
	}

	t.token = result.Token
	t.ready = true
	log.Printf("voice: registered with whisper service (instance=%s)", t.instanceID)
	return nil
}

// Stop is a no-op — there's no subprocess to kill.
func (t *Transcriber) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ready = false
}

// IsReady returns true if registered with the whisper-service.
func (t *Transcriber) IsReady() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ready
}

// Transcribe sends an audio file to the whisper-service and returns the result.
// Automatically re-registers on 401 (token revoked after service restart).
func (t *Transcriber) Transcribe(audioPath string) (*TranscribeResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.ready {
		return nil, fmt.Errorf("transcriber not ready")
	}

	result, err := t.doTranscribe(audioPath)
	if err != nil && t.isAuthError(err) {
		log.Printf("voice: token rejected, re-registering...")
		if regErr := t.register(); regErr != nil {
			return nil, fmt.Errorf("re-register failed: %w", regErr)
		}
		return t.doTranscribe(audioPath)
	}
	return result, err
}

func (t *Transcriber) doTranscribe(audioPath string) (*TranscribeResult, error) {
	f, err := os.Open(audioPath)
	if err != nil {
		return nil, fmt.Errorf("open audio file: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, fmt.Errorf("copy audio data: %w", err)
	}
	writer.Close()

	req, err := http.NewRequest("POST", t.baseURL+"/transcribe", &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+t.token)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transcribe request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return nil, &authError{}
	}
	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("transcribe: status %d: %s", resp.StatusCode, respBody)
	}

	var result TranscribeResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode transcription: %w", err)
	}
	if result.Error != "" {
		return nil, fmt.Errorf("whisper: %s", result.Error)
	}
	if result.Text == "" {
		return nil, fmt.Errorf("empty transcription")
	}
	return &result, nil
}

// DownloadAndTranscribe downloads a voice file from Telegram, transcribes it, cleans up.
func (t *Transcriber) DownloadAndTranscribe(client *http.Client, botToken, fileID string) (*TranscribeResult, error) {
	return downloadAndTranscribe(t.Transcribe, client, botToken, fileID)
}

// IsAvailable pings the whisper-service /health endpoint.
func IsAvailable(baseURL string) bool {
	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

type authError struct{}

func (e *authError) Error() string { return "authentication failed" }

func (t *Transcriber) isAuthError(err error) bool {
	_, ok := err.(*authError)
	return ok
}
