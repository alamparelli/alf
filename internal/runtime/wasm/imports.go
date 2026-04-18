package wasm

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Return-code convention for every host function that returns i32:
//
//	>= 0 : success (optionally a byte count written to the guest buffer)
//	-1   : not found (legitimate absence)
//	-2   : permission denied (Policy does not grant this capability)
//	-3   : buffer too small (caller must retry with a larger buffer)
//	-4   : invalid argument
//	-5   : host-internal error
const (
	rcOK          = 0
	rcNotFound    = -1
	rcDenied      = -2
	rcBufTooSmall = -3
	rcBadArg      = -4
	rcHostErr     = -5
)

// Notifier is the interface the runtime uses to emit host-side log lines
// for guest activity. Implementations can forward to structured loggers,
// the ALF eventlog, or stderr. A nil Notifier means silence.
type Notifier interface {
	GuestLog(capability, level, msg string)
}

// VaultClient is the injection point for the real vault proxy. ALF's
// internal/vault/proxy.go can implement this; tests can inject a stub.
//
// RawGET is for the http.fetch primitive (per-hostname allowlist).
type VaultClient interface {
	// Request calls an allowlisted service and returns the raw body.
	Request(ctx context.Context, service, path string) ([]byte, error)
	// RawGET issues an HTTP GET to a fully-qualified URL.
	RawGET(ctx context.Context, url string) ([]byte, error)
}

// DefaultVaultClient is a simple HTTP-based implementation used by the
// spike. Production wiring replaces it with a vault-proxy-backed client.
type DefaultVaultClient struct {
	HTTP *http.Client
}

// Request implements VaultClient by mapping a service name to a well-known
// public URL. Replace with the real vault-proxy impl when integrating.
func (d *DefaultVaultClient) Request(ctx context.Context, service, path string) ([]byte, error) {
	urlStr := vaultServiceURL(service, path)
	return httpGET(ctx, d.HTTP, urlStr)
}

// RawGET implements VaultClient.RawGET.
func (d *DefaultVaultClient) RawGET(ctx context.Context, u string) ([]byte, error) {
	return httpGET(ctx, d.HTTP, u)
}

// stashMu protects the package-level body stash used to bridge
// _len/_get paired calls within a single guest invocation.
var stashMu sync.Mutex
var stash = map[string]map[string][]byte{}

// BuildHostModule registers the "alf" module with host imports filtered
// by Policy. Only permitted functions are linked; absence is enforcement.
func BuildHostModule(ctx context.Context, rt wazero.Runtime, name string, policy Policy, storage *Storage, vault VaultClient, notifier Notifier) error {
	b := rt.NewHostModuleBuilder("alf")

	if policy.LogEnabled {
		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, ptr, length uint32) {
				msg := readString(m, ptr, length)
				notify(notifier, name, "info", msg)
			}).
			Export("log_info")

		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, ptr, length uint32) {
				msg := readString(m, ptr, length)
				notify(notifier, name, "error", msg)
			}).
			Export("log_error")
	}

	if policy.StorageEnabled && storage != nil {
		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, keyPtr, keyLen, valPtr, valLen uint32) int32 {
				key := readString(m, keyPtr, keyLen)
				val, ok := m.Memory().Read(valPtr, valLen)
				if !ok {
					return rcBadArg
				}
				cp := make([]byte, len(val))
				copy(cp, val)
				if err := storage.Put(key, cp); err != nil {
					log.Printf("[wasm] storage.put(%q): %v", key, err)
					return rcHostErr
				}
				return rcOK
			}).
			Export("storage_put")

		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, keyPtr, keyLen uint32) int32 {
				key := readString(m, keyPtr, keyLen)
				val, ok := storage.Get(key)
				if !ok {
					return rcNotFound
				}
				return int32(len(val))
			}).
			Export("storage_get_len")

		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, keyPtr, keyLen, outPtr, outCap uint32) int32 {
				key := readString(m, keyPtr, keyLen)
				val, ok := storage.Get(key)
				if !ok {
					return rcNotFound
				}
				if uint32(len(val)) > outCap {
					return rcBufTooSmall
				}
				if !m.Memory().Write(outPtr, val) {
					return rcBadArg
				}
				return int32(len(val))
			}).
			Export("storage_get")

		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, keyPtr, keyLen uint32) int32 {
				key := readString(m, keyPtr, keyLen)
				if err := storage.Delete(key); err != nil {
					return rcHostErr
				}
				return rcOK
			}).
			Export("storage_delete")
	}

	if len(policy.VaultServices) > 0 && vault != nil {
		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, svcPtr, svcLen, pathPtr, pathLen uint32) int32 {
				service := readString(m, svcPtr, svcLen)
				if !policy.VaultAllowed(service) {
					return rcDenied
				}
				body, err := vault.Request(ctx, service, readString(m, pathPtr, pathLen))
				if err != nil {
					log.Printf("[wasm] vault.request(%s): %v", service, err)
					return rcHostErr
				}
				stashBody(m.Name(), vaultStashKey(service), body)
				return int32(len(body))
			}).
			Export("vault_request_len")

		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, svcPtr, svcLen, pathPtr, pathLen, outPtr, outCap uint32) int32 {
				service := readString(m, svcPtr, svcLen)
				if !policy.VaultAllowed(service) {
					return rcDenied
				}
				body, ok := popStashed(m.Name(), vaultStashKey(service))
				if !ok {
					b2, err := vault.Request(ctx, service, readString(m, pathPtr, pathLen))
					if err != nil {
						return rcHostErr
					}
					body = b2
				}
				if uint32(len(body)) > outCap {
					stashBody(m.Name(), vaultStashKey(service), body)
					return rcBufTooSmall
				}
				m.Memory().Write(outPtr, body)
				return int32(len(body))
			}).
			Export("vault_request")
	}

	if len(policy.HTTPHosts) > 0 && vault != nil {
		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, urlPtr, urlLen, outPtr, outCap uint32) int32 {
				raw := readString(m, urlPtr, urlLen)
				u, err := url.Parse(raw)
				if err != nil {
					return rcBadArg
				}
				if !policy.HTTPAllowed(u.Hostname()) {
					return rcDenied
				}
				body, err := vault.RawGET(ctx, raw)
				if err != nil {
					return rcHostErr
				}
				if uint32(len(body)) > outCap {
					return rcBufTooSmall
				}
				m.Memory().Write(outPtr, body)
				return int32(len(body))
			}).
			Export("http_fetch")
	}

	if policy.MemoryEnabled {
		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, ptr, length uint32) int32 {
				text := readString(m, ptr, length)
				notify(notifier, name, "memory", "remember: "+truncate(text, 120))
				return rcOK
			}).
			Export("memory_remember")
	}

	if policy.EventsEnabled {
		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, kindPtr, kindLen, payloadPtr, payloadLen uint32) int32 {
				kind := readString(m, kindPtr, kindLen)
				notify(notifier, name, "event", fmt.Sprintf("emit %s (%d bytes)", kind, payloadLen))
				return rcOK
			}).
			Export("events_emit")
	}

	if _, err := b.Instantiate(ctx); err != nil {
		return fmt.Errorf("instantiate host module: %w", err)
	}
	return nil
}

// notify is a nil-safe shortcut around Notifier.GuestLog.
func notify(n Notifier, cap, level, msg string) {
	if n == nil {
		return
	}
	n.GuestLog(cap, level, msg)
}

func readString(m api.Module, ptr, length uint32) string {
	buf, ok := m.Memory().Read(ptr, length)
	if !ok {
		return ""
	}
	return string(buf)
}

func vaultStashKey(service string) string { return "vault:" + service }

func stashBody(modName, key string, body []byte) {
	stashMu.Lock()
	defer stashMu.Unlock()
	mm, ok := stash[modName]
	if !ok {
		mm = map[string][]byte{}
		stash[modName] = mm
	}
	mm[key] = body
}

func popStashed(modName, key string) ([]byte, bool) {
	stashMu.Lock()
	defer stashMu.Unlock()
	mm, ok := stash[modName]
	if !ok {
		return nil, false
	}
	body, ok := mm[key]
	if ok {
		delete(mm, key)
	}
	return body, ok
}

// clearStash drops all cached state for the named module. Called by
// runtime.run after a module instance is disposed.
func clearStash(name string) {
	stashMu.Lock()
	defer stashMu.Unlock()
	delete(stash, name)
}

// vaultServiceURL maps a service name + path to a default public URL.
// Production wiring routes through ALF's vault-proxy and ignores this helper.
func vaultServiceURL(service, path string) string {
	switch service {
	case "coingecko":
		return "https://api.coingecko.com/api/v3" + ensurePrefix(path, "/")
	case "httpbin":
		return "https://httpbin.org" + ensurePrefix(path, "/")
	default:
		return "https://example.invalid" + ensurePrefix(path, "/")
	}
}

func ensurePrefix(s, prefix string) string {
	if strings.HasPrefix(s, prefix) {
		return s
	}
	return prefix + s
}

func httpGET(ctx context.Context, client *http.Client, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
