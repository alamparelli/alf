package appsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// VaultClient talks to the per-app vault proxy socket.
// Apps never see vault tokens — the proxy injects authentication server-side.
type VaultClient struct {
	client *http.Client
	addr   string // dummy "http://localhost" for http.NewRequest
}

// NewVaultClient creates a client connected to the per-app vault proxy socket.
// Reads VAULT_PROXY_SOCK from environment. Returns an error if not set.
func NewVaultClient() (*VaultClient, error) {
	sock := os.Getenv("VAULT_PROXY_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("VAULT_PROXY_SOCK not set — vault proxy not available")
	}
	return &VaultClient{
		addr: "http://localhost",
		client: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", sock)
				},
			},
		},
	}, nil
}

// Proxy sends a request through the vault proxy to an external service.
// The proxy handles authentication — no tokens needed.
//
//	resp, err := vc.Proxy("openrouter", "POST", "/v1/chat/completions", body)
func (v *VaultClient) Proxy(service, method, path string, body io.Reader) (*http.Response, error) {
	url := fmt.Sprintf("%s/proxy/%s/%s", v.addr, service, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return v.client.Do(req)
}

// ProxyJSON sends a JSON request and decodes the JSON response.
// Convenience wrapper for the common case.
func (v *VaultClient) ProxyJSON(service, method, path string, reqBody, respBody any) error {
	var body io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(data)
	}

	resp, err := v.Proxy(service, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	if respBody != nil {
		if err := json.NewDecoder(resp.Body).Decode(respBody); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
