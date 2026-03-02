package updater

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// NotifyFunc is called when a new image version is available.
type NotifyFunc func(currentVersion, latestTag string)

// Checker periodically checks for new Docker image versions.
type Checker struct {
	image    string // e.g. "ghcr.io/user/alf"
	current  string // current running version tag
	interval time.Duration
	notify   NotifyFunc
	client   *http.Client

	mu      sync.Mutex
	stopCh  chan struct{}
	stopped bool
}

// New creates an update checker.
// image is the full image name (e.g. "ghcr.io/alamparelli/alf").
// current is the running version (e.g. "v0.1.30").
func New(image, current string, interval time.Duration, notify NotifyFunc) *Checker {
	return &Checker{
		image:    image,
		current:  current,
		interval: interval,
		notify:   notify,
		client:   &http.Client{Timeout: 15 * time.Second},
		stopCh:   make(chan struct{}),
	}
}

// Start begins periodic checking in a goroutine.
func (c *Checker) Start() {
	go func() {
		// Check once on start after a short delay.
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-timer.C:
			c.check()
		case <-c.stopCh:
			timer.Stop()
			return
		}

		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.check()
			case <-c.stopCh:
				return
			}
		}
	}()
}

// Stop halts the periodic checker.
func (c *Checker) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stopped {
		c.stopped = true
		close(c.stopCh)
	}
}

func (c *Checker) check() {
	latest, err := c.latestTag()
	if err != nil {
		log.Printf("update-check: %v", err)
		return
	}

	if latest != "" && latest != c.current && c.notify != nil {
		c.notify(c.current, latest)
	}
}

// latestTag queries the GHCR API for the latest tag.
func (c *Checker) latestTag() (string, error) {
	// Image format: ghcr.io/OWNER/REPO
	parts := strings.SplitN(c.image, "/", 3)
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid image format: %s", c.image)
	}
	registry, repo := parts[0], parts[1]+"/"+parts[2]

	// GHCR requires token auth even for public repos (OCI distribution spec).
	token, err := c.fetchToken(registry, repo)
	if err != nil {
		return "", fmt.Errorf("auth token: %w", err)
	}

	url := fmt.Sprintf("https://%s/v2/%s/tags/list", registry, repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch tags: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("tags API returned %d", resp.StatusCode)
	}

	var result struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse tags: %w", err)
	}

	// Find the latest semver tag (v*).
	var latest string
	for _, tag := range result.Tags {
		if strings.HasPrefix(tag, "v") && tag > latest {
			latest = tag
		}
	}
	return latest, nil
}

// fetchToken gets an anonymous bearer token from the registry's token service.
func (c *Checker) fetchToken(registry, repo string) (string, error) {
	url := fmt.Sprintf("https://%s/token?service=%s&scope=repository:%s:pull", registry, registry, repo)
	resp, err := c.client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Token, nil
}
