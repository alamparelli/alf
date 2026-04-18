package host

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Return-code convention used by every host function that returns i32:
//
//	>= 0 : success (optionally a byte count written to the guest's buffer)
//	-1   : not found (no data, legitimate absence)
//	-2   : permission denied (the Policy does not grant this capability)
//	-3   : buffer too small (caller must retry with a larger buffer)
//	-4   : invalid argument
//	-5   : host-internal error (a bug or transient failure)
const (
	rcOK          = 0
	rcNotFound    = -1
	rcDenied      = -2
	rcBufTooSmall = -3
	rcBadArg      = -4
	rcHostErr     = -5
)

// BuildHostModule registers an "alf" module in the given wazero runtime.
// Only the functions permitted by the Policy are wired. The rest are absent,
// so a guest that tries to import them fails at instantiation (link time).
func BuildHostModule(ctx context.Context, rt wazero.Runtime, name string, policy Policy, storage *Storage, vaultHTTP *http.Client) error {
	b := rt.NewHostModuleBuilder("alf")

	// --- Always available: the module loader has already decided this guest
	// is allowed to run at all. Log is extremely useful for debugging and
	// we treat it as universally safe. A stricter policy could gate it too.
	if policy.LogEnabled {
		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, ptr, length uint32) {
				msg := readString(m, ptr, length)
				log.Printf("[guest:%s] %s", name, msg)
			}).
			Export("log_info")

		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, ptr, length uint32) {
				msg := readString(m, ptr, length)
				log.Printf("[guest:%s] ERROR: %s", name, msg)
			}).
			Export("log_error")
	}

	// --- Storage (scoped per-capability) ---
	if policy.StorageEnabled && storage != nil {
		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, keyPtr, keyLen, valPtr, valLen uint32) int32 {
				key := readString(m, keyPtr, keyLen)
				val, ok := m.Memory().Read(valPtr, valLen)
				if !ok {
					return rcBadArg
				}
				cp := make([]byte, len(val))
				copy(cp, val) // detach from guest memory
				if err := storage.Put(key, cp); err != nil {
					log.Printf("[host] storage.put(%q): %v", key, err)
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

	// --- Vault (per-service allowlist). In real ALF this would proxy to
	// the vault-server; for the spike we call the public URL directly,
	// but *only* if the declared service is whitelisted. This preserves the
	// Policy contract — the guest cannot reach arbitrary hosts.
	if len(policy.VaultServices) > 0 && vaultHTTP != nil {
		// Register the functions even if a specific service is denied at
		// call time — presence of *any* vault service means the interface
		// exists; the per-call check lives inside the function body.
		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, svcPtr, svcLen, pathPtr, pathLen uint32) int32 {
				service := readString(m, svcPtr, svcLen)
				if !policy.VaultAllowed(service) {
					return rcDenied
				}
				urlStr := vaultServiceURL(service, readString(m, pathPtr, pathLen))
				body, err := httpGET(ctx, vaultHTTP, urlStr)
				if err != nil {
					log.Printf("[host] vault.request(%s): %v", service, err)
					return rcHostErr
				}
				stashBody(m, vaultStashKey(service), body)
				return int32(len(body))
			}).
			Export("vault_request_len")

		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, svcPtr, svcLen, pathPtr, pathLen, outPtr, outCap uint32) int32 {
				service := readString(m, svcPtr, svcLen)
				if !policy.VaultAllowed(service) {
					return rcDenied
				}
				body, ok := popStashed(m, vaultStashKey(service))
				if !ok {
					// Caller did not call _len first; fetch directly.
					urlStr := vaultServiceURL(service, readString(m, pathPtr, pathLen))
					b2, err := httpGET(ctx, vaultHTTP, urlStr)
					if err != nil {
						return rcHostErr
					}
					body = b2
				}
				if uint32(len(body)) > outCap {
					// Re-stash so caller can retry with a bigger buffer.
					stashBody(m, vaultStashKey(service), body)
					return rcBufTooSmall
				}
				m.Memory().Write(outPtr, body)
				return int32(len(body))
			}).
			Export("vault_request")
	}

	// --- Raw HTTP (per-host allowlist). Stricter than vault: only GETs,
	// only allowlisted hostnames.
	if len(policy.HTTPHosts) > 0 && vaultHTTP != nil {
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
				body, err := httpGET(ctx, vaultHTTP, raw)
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

	// --- Memory (stubbed for the spike). Real ALF would wire to memstore. ---
	if policy.MemoryEnabled {
		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, ptr, length uint32) int32 {
				text := readString(m, ptr, length)
				log.Printf("[host] memory.remember (stub): %q", truncate(text, 80))
				return rcOK
			}).
			Export("memory_remember")
	}

	// --- Events (stubbed) ---
	if policy.EventsEnabled {
		b.NewFunctionBuilder().
			WithFunc(func(ctx context.Context, m api.Module, kindPtr, kindLen, payloadPtr, payloadLen uint32) int32 {
				kind := readString(m, kindPtr, kindLen)
				log.Printf("[host] events.emit (stub): kind=%q payload=%d bytes", kind, payloadLen)
				return rcOK
			}).
			Export("events_emit")
	}

	if _, err := b.Instantiate(ctx); err != nil {
		return fmt.Errorf("instantiate host module: %w", err)
	}
	return nil
}

// readString reads a UTF-8 string from guest memory.
func readString(m api.Module, ptr, length uint32) string {
	buf, ok := m.Memory().Read(ptr, length)
	if !ok {
		return ""
	}
	return string(buf)
}

// vaultServiceURL maps a service name + path to a real URL.
// In real ALF, this is vault-server's /proxy/<service>/<path>. For the spike,
// we map well-known services to their public APIs so examples run without
// extra infra.
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

// --- A tiny per-module stash so _len and _get can share a buffer across calls.
// Keyed by module instance + service. The spike keeps this in a map on the
// Module's user data — for a real runtime this would be a concurrent-safe
// cache with TTL.

func vaultStashKey(service string) string { return "vault:" + service }

// Note: wazero api.Module does not expose a user-data slot. For the spike,
// we use a package-level map keyed by module name; it is fine because each
// invocation creates and drops its module instance.
var stash = map[string]map[string][]byte{}

func stashBody(m api.Module, key string, body []byte) {
	mm, ok := stash[m.Name()]
	if !ok {
		mm = map[string][]byte{}
		stash[m.Name()] = mm
	}
	mm[key] = body
}

func popStashed(m api.Module, key string) ([]byte, bool) {
	mm, ok := stash[m.Name()]
	if !ok {
		return nil, false
	}
	body, ok := mm[key]
	if ok {
		delete(mm, key)
	}
	return body, ok
}

// ClearStash is called by the runtime after a module instance is closed.
func ClearStash(name string) { delete(stash, name) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
