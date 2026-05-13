package handle

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alamparelli/alf/internal/capability"
)

// ErrHandleNonSerializable is returned by any handle's MarshalJSON (and
// future MarshalBinary) to implement §4.2 invariant 1: handle values
// never cross a serialisation boundary.
var ErrHandleNonSerializable = errors.New("handle: not serializable")

// mergeContexts returns a context derived from caller that also cancels
// when lifecycle cancels. The returned cancel must be called to release
// the AfterFunc registration and free resources.
func mergeContexts(caller, lifecycle context.Context) (context.Context, context.CancelFunc) {
	merged, cancel := context.WithCancel(caller)
	if lifecycle == nil {
		return merged, cancel
	}
	stop := context.AfterFunc(lifecycle, cancel)
	return merged, func() {
		stop()
		cancel()
	}
}

// cancelOnClose wraps an http.Response.Body so closing it cancels the
// request context, releasing goroutines spawned by context.AfterFunc.
// Safe to Close multiple times — context.CancelFunc is idempotent.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// HTTPPattern is one entry from the manifest's [[http.scopes]] block.
// Host is the exact-match target. PathPrefix is an optional literal
// prefix the URL path must satisfy. Matching is segment-aware at
// HTTPScope.Allows ("/books/v1" matches "/books/v1" and "/books/v1/X"
// but NOT "/books/v10" — defeats the prefix-collision footgun).
//
// The shape is intentionally narrow per #421: no wildcards, no regex,
// no scheme (HTTPS is implicit when RequireHTTPS is set on the parent
// scope). The envelope validator already enforces these rules at parse
// time; the handle layer trusts the inputs it receives because the
// forge is the only producer.
type HTTPPattern struct {
	Host       string // exact match, lowercase, optional ":port" suffix
	PathPrefix string // "" = any path; otherwise must start with "/"
}

// HTTPScope describes the outbound HTTP access a capability was granted.
// Patterns is the authoritative allowlist — empty means "no outbound
// HTTP authority". Methods is the upper-cased set of allowed verbs;
// empty means all methods are allowed on matched hosts. RequireHTTPS,
// when true, rejects any non-HTTPS request even if a pattern would
// otherwise match.
//
// Scope is enforced by HTTPScope.Allows at each Do() call. A lying
// manifest cannot expand this at runtime because the scope is compiled
// once at forge time from the verified manifest. The #421 forge
// always sets RequireHTTPS = true; test fixtures may set it to false
// to exercise the host-import path against httptest servers (which
// only speak HTTP).
type HTTPScope struct {
	Patterns     []HTTPPattern
	Methods      []string
	RequireHTTPS bool
}

// Allows reports whether a request to the given URL with the given method
// is within scope. Nil URL, empty Host, zero-length Patterns, and HTTPS
// requirement mismatch are all treated as denials.
func (s HTTPScope) Allows(u *url.URL, method string) bool {
	if u == nil {
		return false
	}
	if s.RequireHTTPS && !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	if len(s.Methods) > 0 && !s.allowedMethod(method) {
		return false
	}
	if len(s.Patterns) == 0 {
		return false
	}
	// u.Host carries "host" or "host:port" — we compare verbatim so a
	// pattern "homelab.local:8443" matches only when the request URL
	// also carries the explicit port. A pattern "openlibrary.org"
	// matches https://openlibrary.org/... but NOT
	// https://openlibrary.org:8443/... — explicit ports require an
	// explicit pattern.
	host := strings.ToLower(u.Host)
	if host == "" {
		return false
	}
	for _, p := range s.Patterns {
		if strings.ToLower(p.Host) != host {
			continue
		}
		if matchesPathPrefix(p.PathPrefix, u.Path) {
			return true
		}
	}
	return false
}

func (s HTTPScope) allowedMethod(m string) bool {
	m = strings.ToUpper(m)
	for _, allowed := range s.Methods {
		if strings.ToUpper(allowed) == m {
			return true
		}
	}
	return false
}

// matchesPathPrefix returns true iff path is within prefix using
// segment-aware matching: "/books/v1" matches "/books/v1" and
// "/books/v1/X" but NOT "/books/v10". Empty prefix matches any path.
func matchesPathPrefix(prefix, path string) bool {
	if prefix == "" {
		return true
	}
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, prefix+"/")
}

// HTTPHandle grants scoped outbound HTTP access. Non-serializable (via the
// noSerialize marker + MarshalJSON). Revocation: Instance.Close flips
// revoked; in-flight requests propagate cancellation through lifecycleCtx
// via context.AfterFunc, so Close() interrupts long-running responses
// without needing each handler to poll.
type HTTPHandle struct {
	_ [0]noSerialize

	owner        capability.ID
	scope        HTTPScope
	client       *http.Client
	lifecycleCtx context.Context
	revoked      atomic.Bool
}

// NewHTTPHandle constructs an HTTP handle with the given scope. The
// lifecycleCtx field is zero until the handle is wired into an Instance
// (see Instance wiring below).
func NewHTTPHandle(owner capability.ID, scope HTTPScope, client *http.Client) *HTTPHandle {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPHandle{
		owner:  owner,
		scope:  scope,
		client: client,
	}
}

// Do executes the request iff scope allows it and the handle has not been
// revoked. The request's context is merged with the handle's lifecycleCtx
// so Instance.Close() aborts in-flight operations.
func (h *HTTPHandle) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if h.revoked.Load() {
		return nil, ErrRevoked
	}
	if !h.scope.Allows(req.URL, req.Method) {
		return nil, ErrOutOfScope
	}
	opCtx, cancel := mergeContexts(ctx, h.lifecycleCtx)
	// NOTE: we cannot defer cancel() here — http.Client.Do returns with an
	// unread body, and cancelling opCtx would abort the body stream before
	// the caller reads it. We bind the cancel to the response body close
	// instead. If Do returns an error, we cancel immediately.
	req = req.WithContext(opCtx)
	resp, err := h.client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// Owner returns the capability ID this handle was forged for. Exported for
// audit/log callers; no authority is conveyed by reading it.
func (h *HTTPHandle) Owner() capability.ID { return h.owner }

// MarshalJSON implements §4.2 invariant 1: handle values never cross a
// serialisation boundary. Returning an error stops encoding/json, and the
// archtest rule (future step) forbids any encoding/* Marshal call on a
// handle type.
func (h *HTTPHandle) MarshalJSON() ([]byte, error) {
	return nil, ErrHandleNonSerializable
}

// attachLifecycle is the package-private hook used by Instance to bind an
// HTTPHandle to its lifecycle context. Keeps the field unexported so no
// capability can rebind scope or lifecycle after forge time.
func (h *HTTPHandle) attachLifecycle(ctx context.Context) { h.lifecycleCtx = ctx }
