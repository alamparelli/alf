package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NotifyFunc is called when a new image version is available AND
// its cosign signature has verified against the configured release
// identity. The digest is plumbed through so the installer can
// pull `repo@<digest>` instead of `repo:<tag>` — closing the
// "registry repushes the tag with a different digest" attack
// surface (#403).
type NotifyFunc func(currentVersion, latestTag, latestDigest string)

// Checker periodically checks for new versions via the GHCR container registry API.
type Checker struct {
	repo     string // "owner/repo" (extracted from image name)
	registry string // registry host (e.g. "ghcr.io")
	current  string // current running version (e.g. "0.6.14")
	interval time.Duration
	notify   NotifyFunc
	client   *http.Client

	// verifier is the cosign signature verifier (#403). Empty
	// means "no verification" — Notify fires on every newer tag,
	// matching the v0.7.x behaviour. Production daemons set this
	// via SetCosignVerifier so signature failures abort the notify.
	verifier *CosignVerifier

	mu            sync.Mutex
	stopCh        chan struct{}
	stopped       bool
	latestVersion string // last detected latest version (empty = not checked yet)
	latestDigest  string // matching digest for latestVersion
}

// New creates an update checker.
// image is the full image name (e.g. "ghcr.io/alamparelli/alf").
// current is the running version (e.g. "0.6.14").
func New(image, current string, interval time.Duration, notify NotifyFunc) *Checker {
	registry := "ghcr.io"
	repo := image
	if parts := strings.SplitN(image, "/", 3); len(parts) == 3 {
		registry = parts[0]
		repo = parts[1] + "/" + parts[2]
	}
	return &Checker{
		repo:     repo,
		registry: registry,
		current:  strings.TrimPrefix(current, "v"),
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

// LatestVersion returns the latest available version if an update was detected,
// or empty string if current is up-to-date or not checked yet.
func (c *Checker) LatestVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latestVersion
}

// LatestDigest returns the OCI manifest digest for the latest
// detected version (`sha256:…`). Empty when no update was detected
// yet OR when cosign verification was disabled. The installer pulls
// `repo@<digest>` rather than `repo:<tag>` to defeat tag-repush
// attacks at the registry (#403).
func (c *Checker) LatestDigest() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latestDigest
}

// SetCosignVerifier wires the #403 signature gate. After every
// latestTag pick, the checker resolves the tag's manifest digest
// via OCI HEAD and runs cosign verify against repo@digest. Notify
// fires only on success — a verification failure aborts the
// notification with an audit log line and leaves latestVersion
// unchanged.
//
// When the verifier is nil (default), checker behaviour is the
// pre-#403 path: notify on every newer tag without signature
// check. Set this from the daemon's main wiring; tests pass a stub
// verifier with a deterministic Run hook.
func (c *Checker) SetCosignVerifier(v *CosignVerifier) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.verifier = v
}

func (c *Checker) check() {
	latest, err := c.latestTag()
	if err != nil {
		log.Printf("update-check: %v", err)
		return
	}

	if latest == "" {
		log.Printf("update-check: no semver tags found in registry")
		return
	}
	if c.current == latest || compareSemver(latest, c.current) <= 0 {
		log.Printf("update-check: up-to-date (current=%s, latest=%s)", c.current, latest)
		return
	}

	log.Printf("update-check: update available %s → %s — resolving digest", c.current, latest)

	// #403: resolve the tag's manifest digest via OCI HEAD. A tag
	// that exists in /tags/list but has no manifest is a registry
	// inconsistency — refuse to notify.
	digest, err := c.resolveDigest(latest)
	if err != nil {
		log.Printf("update-check: resolve digest for %s: %v — refusing to notify", latest, err)
		return
	}
	if digest == "" {
		log.Printf("update-check: registry returned empty digest for %s — refusing to notify", latest)
		return
	}

	// #403: cosign verify the repo@digest pair. A verification
	// failure aborts the notify so a tag repush with a different
	// (unsigned or attacker-signed) digest cannot reach the
	// operator UI. When no verifier is wired, behaviour falls back
	// to the pre-#403 path (notify on every newer tag) — the
	// daemon's production wiring always sets one.
	c.mu.Lock()
	verifier := c.verifier
	c.mu.Unlock()
	if verifier != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		repo := c.registry + "/" + c.repo
		if err := verifier.Verify(ctx, repo, digest); err != nil {
			log.Printf("update-check: cosign verify %s@%s failed: %v — refusing to notify", repo, digest, err)
			return
		}
		log.Printf("update-check: cosign verify %s@%s ok", repo, digest)
	} else {
		log.Printf("update-check: no cosign verifier wired — proceeding without signature check (set ALF_DISABLE_COSIGN_VERIFY=1 to silence)")
	}

	c.mu.Lock()
	c.latestVersion = latest
	c.latestDigest = digest
	c.mu.Unlock()
	if c.notify != nil {
		c.notify(c.current, latest, digest)
	}
}

// resolveDigest issues an OCI HEAD against the manifest URL for
// the named tag and returns the Docker-Content-Digest header. The
// digest binds the tag to a specific image content hash — pulling
// `repo@<digest>` defeats tag-repush attacks at the registry.
//
// Implementation: anonymous bearer token + HEAD against
// /v2/<repo>/manifests/<tag>. The Accept header negotiates the
// modern OCI manifest media type so GHCR returns the right digest
// (legacy V2 schema 1 returned non-deterministic hashes).
func (c *Checker) resolveDigest(tag string) (string, error) {
	tokenURL := fmt.Sprintf("https://%s/token?service=%s&scope=repository:%s:pull", c.registry, c.registry, c.repo)
	tokenResp, err := c.client.Get(tokenURL)
	if err != nil {
		return "", fmt.Errorf("fetch registry token: %w", err)
	}
	defer tokenResp.Body.Close()
	if tokenResp.StatusCode != 200 {
		return "", fmt.Errorf("registry token endpoint returned %d", tokenResp.StatusCode)
	}
	var tokenResult struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenResult); err != nil {
		return "", fmt.Errorf("parse registry token: %w", err)
	}

	manifestURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", c.registry, c.repo, tag)
	req, err := http.NewRequest("HEAD", manifestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tokenResult.Token)
	// Accept both OCI image manifest and image index — GHCR serves
	// an index for multi-arch images (linux/amd64 + linux/arm64).
	// The signed digest is the index digest; cosign verifies the
	// index, which transitively covers the per-arch manifests.
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.docker.distribution.manifest.v2+json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("manifest HEAD returned %d", resp.StatusCode)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", errors.New("manifest HEAD missing Docker-Content-Digest header")
	}
	return digest, nil
}

var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// latestTag queries the GHCR container registry for the highest semver tag.
// Uses the OCI distribution API with anonymous token auth, following pagination.
func (c *Checker) latestTag() (string, error) {
	// Step 1: obtain anonymous bearer token.
	tokenURL := fmt.Sprintf("https://%s/token?service=%s&scope=repository:%s:pull", c.registry, c.registry, c.repo)
	tokenResp, err := c.client.Get(tokenURL)
	if err != nil {
		return "", fmt.Errorf("fetch registry token: %w", err)
	}
	defer tokenResp.Body.Close()

	if tokenResp.StatusCode != 200 {
		return "", fmt.Errorf("registry token endpoint returned %d", tokenResp.StatusCode)
	}
	var tokenResult struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenResult); err != nil {
		return "", fmt.Errorf("parse registry token: %w", err)
	}

	// Step 2: list all tags via OCI distribution spec, following pagination.
	var best string
	nextURL := fmt.Sprintf("https://%s/v2/%s/tags/list", c.registry, c.repo)

	for i := 0; nextURL != "" && i < 50; i++ { // cap at 50 pages as safety
		req, err := http.NewRequest("GET", nextURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+tokenResult.Token)

		resp, err := c.client.Do(req)
		if err != nil {
			return "", fmt.Errorf("fetch tags: %w", err)
		}

		if resp.StatusCode == 404 {
			resp.Body.Close()
			return "", nil // no packages yet
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return "", fmt.Errorf("tags API returned %d", resp.StatusCode)
		}

		var tagsResult struct {
			Tags []string `json:"tags"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tagsResult); err != nil {
			resp.Body.Close()
			return "", fmt.Errorf("parse tags: %w", err)
		}
		resp.Body.Close()

		for _, t := range tagsResult.Tags {
			if !semverRe.MatchString(t) {
				continue
			}
			if best == "" || compareSemver(t, best) > 0 {
				best = t
			}
		}

		nextURL = parseNextLink(resp.Header.Get("Link"), c.registry)
	}

	return best, nil
}

// parseNextLink extracts the next page URL from the Link header.
// Format: </v2/owner/repo/tags/list?last=xxx&n=0>; rel="next"
func parseNextLink(header, registry string) string {
	if header == "" {
		return ""
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start < 0 || end < 0 || end <= start {
			continue
		}
		path := part[start+1 : end]
		if strings.HasPrefix(path, "/") {
			return "https://" + registry + path
		}
		return path
	}
	return ""
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
