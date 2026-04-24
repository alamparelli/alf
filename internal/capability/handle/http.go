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

// HTTPScope describes the outbound HTTP access a capability was granted.
// Hosts accept either an exact hostname ("api.example.com") or a
// leading-wildcard pattern ("*.example.com") that matches any subdomain
// of the suffix. Methods is the upper-cased set of allowed verbs; empty
// means all methods are allowed on matched hosts.
//
// Scope is enforced by HTTPScope.Allows at each Do() call. A lying
// manifest cannot expand this at runtime because the scope is compiled
// once at forge time from the verified manifest.
type HTTPScope struct {
	Hosts   []string
	Methods []string
}

// Allows reports whether a request to the given URL with the given method
// is within scope. Nil URL, empty hostname, and zero-length Hosts are all
// treated as denials.
func (s HTTPScope) Allows(u *url.URL, method string) bool {
	if u == nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if !s.allowedHost(host) {
		return false
	}
	if len(s.Methods) > 0 && !s.allowedMethod(method) {
		return false
	}
	return true
}

func (s HTTPScope) allowedHost(host string) bool {
	host = strings.ToLower(host)
	for _, pattern := range s.Hosts {
		pattern = strings.ToLower(pattern)
		if pattern == host {
			return true
		}
		if strings.HasPrefix(pattern, "*.") {
			suffix := pattern[1:]
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
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
