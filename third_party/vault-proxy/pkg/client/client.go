// Package client is a portable HTTP client for vault-server.
// Any Go project can import this — no internal dependencies.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultAddr  = "http://127.0.0.1:8390"
	envAddr      = "VAULT_ADDR"
	envToken     = "VAULT_TOKEN"
	envProxySock = "VAULT_PROXY_SOCK"
)

// Client talks to the vault server. Portable — any Go tool can import this.
type Client struct {
	Addr   string
	Token  string
	client *http.Client
}

// New creates a client from environment variables.
// Detection order:
//  1. VAULT_PROXY_SOCK → Unix socket (no token needed, proxy injects it)
//  2. VAULT_ADDR with "unix:" prefix → Unix socket with token
//  3. VAULT_ADDR or default TCP address
func New() *Client {
	if sock := os.Getenv(envProxySock); sock != "" {
		return NewWithSocket(sock, "")
	}
	addr := os.Getenv(envAddr)
	if strings.HasPrefix(addr, "unix:") {
		return NewWithSocket(strings.TrimPrefix(addr, "unix:"), os.Getenv(envToken))
	}
	if addr == "" {
		addr = defaultAddr
	}
	return &Client{
		Addr:   strings.TrimRight(addr, "/"),
		Token:  os.Getenv(envToken),
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// NewWithToken creates a client with explicit addr and token.
func NewWithToken(addr, token string) *Client {
	if addr == "" {
		addr = defaultAddr
	}
	return &Client{
		Addr:   strings.TrimRight(addr, "/"),
		Token:  token,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// NewWithSocket creates a client that connects via a Unix domain socket.
// The Addr is set to "http://localhost" as a dummy for http.NewRequest.
func NewWithSocket(socketPath, token string) *Client {
	return &Client{
		Addr:  "http://localhost",
		Token: token,
		client: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

// Health checks the vault server status.
func (c *Client) Health() (string, error) {
	resp, err := c.do("GET", "/health", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Status, nil
}

// Unlock authenticates with the master password. Returns an admin token.
func (c *Client) Unlock(password string) (string, error) {
	body, _ := json.Marshal(map[string]string{"password": password})
	resp, err := c.do("POST", "/auth/unlock", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", readError(resp)
	}

	var token struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return "", err
	}
	return token.ID, nil
}

// Lock locks the vault.
func (c *Client) Lock() error {
	resp, err := c.do("POST", "/auth/lock", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	return nil
}

// ListServices returns all configured services (no secrets).
func (c *Client) ListServices() ([]ServiceInfo, error) {
	resp, err := c.do("GET", "/services", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var services []ServiceInfo
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return nil, err
	}
	return services, nil
}

// AddService creates or updates a service. The payload is raw JSON
// matching vault-server's POST /services schema.
func (c *Client) AddService(jsonPayload io.Reader) error {
	resp, err := c.do("POST", "/services", jsonPayload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readError(resp)
	}
	return nil
}

// UpdateService updates an existing service via PUT /services/{name}.
func (c *Client) UpdateService(name string, jsonPayload io.Reader) error {
	resp, err := c.do("PUT", "/services/"+name, jsonPayload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readError(resp)
	}
	return nil
}

// RemoveService deletes a service by name.
func (c *Client) RemoveService(name string) error {
	resp, err := c.do("DELETE", "/services/"+name, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readError(resp)
	}
	return nil
}

// TestService tests connectivity of a service by proxying a GET to its base URL.
func (c *Client) TestService(name string) error {
	resp, err := c.do("GET", "/proxy/"+name+"/", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return readError(resp)
	}
	return nil
}

// CreateToken creates a new token with the given scope.
func (c *Client) CreateToken(scope string) (string, error) {
	body, _ := json.Marshal(map[string]string{"scope": scope})
	resp, err := c.do("POST", "/tokens", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return "", readError(resp)
	}

	var token struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return "", err
	}
	return token.ID, nil
}

// ListTokens returns active tokens (no secret material).
func (c *Client) ListTokens() ([]TokenInfo, error) {
	resp, err := c.do("GET", "/tokens", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var tokens []TokenInfo
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

// RevokeToken revokes a token by ID.
func (c *Client) RevokeToken(id string) error {
	resp, err := c.do("DELETE", "/tokens/"+id, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readError(resp)
	}
	return nil
}

// Proxy sends an HTTP request through the vault proxy.
// Returns the raw response for the caller to handle (streaming, etc).
func (c *Client) Proxy(service, method, path string, body io.Reader) (*http.Response, error) {
	endpoint := fmt.Sprintf("/proxy/%s/%s", service, strings.TrimPrefix(path, "/"))
	return c.do(method, endpoint, body)
}

// UploadFile uploads a file to the vault.
func (c *Client) UploadFile(name, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("name", name); err != nil {
		return err
	}
	part, err := w.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	w.Close()

	req, err := http.NewRequest("POST", c.Addr+"/files", &buf)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readError(resp)
	}
	return nil
}

// ListFiles returns info for all stored files.
func (c *Client) ListFiles() ([]FileInfo, error) {
	resp, err := c.do("GET", "/files", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var files []FileInfo
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, err
	}
	return files, nil
}

// GetFile downloads a file's raw bytes.
func (c *Client) GetFile(name string) ([]byte, error) {
	resp, err := c.do("GET", "/files/"+name, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}
	return io.ReadAll(resp.Body)
}

// DeleteFile removes a file from the vault.
func (c *Client) DeleteFile(name string) error {
	resp, err := c.do("DELETE", "/files/"+name, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readError(resp)
	}
	return nil
}

// SSHExec executes a command on a remote host via the vault SSH proxy.
func (c *Client) SSHExec(service, command string, timeoutSecs int) (*SSHExecResult, error) {
	body, _ := json.Marshal(map[string]any{
		"command": command,
		"timeout": timeoutSecs,
	})
	resp, err := c.do("POST", "/ssh/"+service+"/exec", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, readError(resp)
	}
	var result SSHExecResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SSHUpload uploads data to a remote path via the vault SFTP proxy.
func (c *Client) SSHUpload(service, remotePath string, data io.Reader, mode string) error {
	if mode == "" {
		mode = "0644"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("remote_path", remotePath)
	w.WriteField("mode", mode)
	part, err := w.CreateFormFile("file", filepath.Base(remotePath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, data); err != nil {
		return err
	}
	w.Close()

	req, err := http.NewRequest("POST", c.Addr+"/ssh/"+service+"/upload", &buf)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readError(resp)
	}
	return nil
}

// SSHDownload downloads a file from a remote host via the vault SFTP proxy.
// The caller must close the returned ReadCloser.
func (c *Client) SSHDownload(service, remotePath string) (io.ReadCloser, error) {
	body, _ := json.Marshal(map[string]string{"remote_path": remotePath})
	resp, err := c.do("POST", "/ssh/"+service+"/download", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, readError(resp)
	}
	return resp.Body, nil
}

// SSHSessionURL returns the WebSocket URL for an interactive SSH session.
func (c *Client) SSHSessionURL(service string) string {
	addr := c.Addr
	addr = strings.Replace(addr, "http://", "ws://", 1)
	addr = strings.Replace(addr, "https://", "wss://", 1)
	return addr + "/ssh/" + service + "/session"
}

// Do sends a raw HTTP request to vault-server.
// The caller is responsible for closing the response body.
func (c *Client) Do(method, path string, body io.Reader) (*http.Response, error) {
	return c.do(method, path, body)
}

// DoRequest sends a pre-built HTTP request through the client's transport.
// Use this for requests that need custom headers (e.g. multipart uploads).
// The request URL must use c.Addr as the base.
func (c *Client) DoRequest(req *http.Request) (*http.Response, error) {
	if c.Token != "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return c.client.Do(req)
}

func (c *Client) do(method, path string, body io.Reader) (*http.Response, error) {
	url := c.Addr + path
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.client.Do(req)
}

func readError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
}
