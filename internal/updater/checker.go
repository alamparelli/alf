package updater

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NotifyFunc is called when a new image version is available.
type NotifyFunc func(currentVersion, latestTag string)

// Checker periodically checks for new versions via GitHub Releases API.
type Checker struct {
	repo     string // GitHub "owner/repo" (extracted from image name)
	current  string // current running version tag (e.g. "v0.6.14")
	interval time.Duration
	notify   NotifyFunc
	client   *http.Client

	mu      sync.Mutex
	stopCh  chan struct{}
	stopped bool
}

// New creates an update checker.
// image is the full image name (e.g. "ghcr.io/alamparelli/alf").
// current is the running version (e.g. "v0.6.14").
func New(image, current string, interval time.Duration, notify NotifyFunc) *Checker {
	// Extract "owner/repo" from "ghcr.io/owner/repo".
	repo := image
	if parts := strings.SplitN(image, "/", 3); len(parts) == 3 {
		repo = parts[1] + "/" + parts[2]
	}
	return &Checker{
		repo:     repo,
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

// CheckOnce runs a single update check immediately.
func (c *Checker) CheckOnce() error {
	c.check()
	return nil
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

	if latest == "" {
		return
	}
	// Normalize: compare without "v" prefix.
	cur := strings.TrimPrefix(c.current, "v")
	lat := strings.TrimPrefix(latest, "v")
	if cur != lat && compareSemver(lat, cur) > 0 && c.notify != nil {
		c.notify(c.current, latest)
	}
}

// latestTag queries the GitHub Releases API for the latest release tag.
// Uses the public API (no auth needed for public repos, 60 req/hour).
func (c *Checker) latestTag() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", c.repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "", nil // no releases yet
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("releases API returned %d", resp.StatusCode)
	}

	var result struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse release: %w", err)
	}
	return result.TagName, nil
}

// compareSemver compares two semver strings (without "v" prefix).
// Returns >0 if a > b, <0 if a < b, 0 if equal.
func compareSemver(a, b string) int {
	ap := strings.SplitN(a, ".", 3)
	bp := strings.SplitN(b, ".", 3)
	for i := 0; i < 3; i++ {
		var av, bv int
		if i < len(ap) {
			av, _ = strconv.Atoi(ap[i])
		}
		if i < len(bp) {
			bv, _ = strconv.Atoi(bp[i])
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}
