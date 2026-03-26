package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/alamparelli/alf/internal/vault"
)

// tokenHealth mirrors the vault-proxy /health/tokens response.
type tokenHealth struct {
	Name        string `json:"name"`
	AuthType    string `json:"auth_type"`
	ExpiresAt   int64  `json:"expires_at"`
	TokenStatus string `json:"token_status"`
}

// vaultTokenChecker periodically checks OAuth2/SA token health and alerts via Telegram.
type vaultTokenChecker struct {
	vaultMgr *vault.Manager
	sendAlert func(msg string) // sends alert to user (e.g., Telegram)
	alerted   map[string]time.Time // tracks last alert per service to avoid spam
}

func newVaultTokenChecker(mgr *vault.Manager, sendAlert func(string)) *vaultTokenChecker {
	return &vaultTokenChecker{
		vaultMgr:  mgr,
		sendAlert: sendAlert,
		alerted:   make(map[string]time.Time),
	}
}

// Check polls /health/tokens and sends alerts for expired/expiring tokens.
func (c *vaultTokenChecker) Check() error {
	if c.vaultMgr == nil {
		return nil
	}

	addr := c.vaultMgr.Addr()
	token := c.vaultMgr.ProxyToken()
	if addr == "" || token == "" {
		return nil // vault not ready
	}

	req, err := http.NewRequest("GET", addr+"/health/tokens", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("vault health check: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("vault health check: %s — %s", resp.Status, string(body))
	}

	var tokens []tokenHealth
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return fmt.Errorf("vault health check: parse: %w", err)
	}

	for _, t := range tokens {
		switch t.TokenStatus {
		case "expired":
			c.alert(t.Name, fmt.Sprintf("⚠️ Vault: token for **%s** (%s) has expired. Re-authenticate via the vault.", t.Name, t.AuthType))
		case "expiring":
			remaining := time.Until(time.Unix(t.ExpiresAt, 0)).Round(time.Minute)
			c.alert(t.Name, fmt.Sprintf("⏳ Vault: token for **%s** (%s) expires in %s. Auto-refresh should handle this — if it persists, re-authenticate.", t.Name, t.AuthType, remaining))
		}
	}

	return nil
}

// alert sends a message but avoids spamming the same service more than once per hour.
func (c *vaultTokenChecker) alert(service, msg string) {
	if lastAlert, ok := c.alerted[service]; ok && time.Since(lastAlert) < time.Hour {
		return
	}
	c.alerted[service] = time.Now()
	log.Printf("[vault] %s", msg)
	if c.sendAlert != nil {
		c.sendAlert(msg)
	}
}
