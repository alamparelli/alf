package main

import (
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	cc "github.com/alamparelli/alf/internal/controlcenter"
	"github.com/alamparelli/alf/internal/firewall"
	"github.com/alamparelli/alf/internal/provider"
	"github.com/alamparelli/alf/internal/router"
	"github.com/alamparelli/alf/internal/secrets"
	"github.com/alamparelli/alf/internal/tooling"
	"github.com/alamparelli/alf/internal/vault"
)

// readAuthToken reads CC_AUTH_TOKEN from vault-data (daemon-only, mode 700)
// first, then falls back to Docker secret. The vault-data path is not readable
// by the LLM subprocess (alf/uid 1000).
func readAuthToken() string {
	// Primary: vault-data (protected by directory permissions).
	data, err := os.ReadFile("/opt/alf/vault-data/.cc_auth_token")
	if err == nil {
		if tok := strings.TrimSpace(string(data)); tok != "" {
			return tok
		}
	}
	// Fallback: Docker secret (for backwards compatibility / migration).
	return secrets.ReadSecret("CC_AUTH_TOKEN")
}

// vaultPassword reads the master password from the persisted password file
// in the vault data directory (written by CC unlock/setup handler).
// Docker secrets are NOT used — vault-data is owned by alfd (mode 700),
// so the LLM subprocess (alf/uid 1000) cannot read the password.
func vaultPassword(mgr *vault.Manager) string {
	if mgr != nil {
		data, err := os.ReadFile(mgr.PasswordFile())
		if err == nil {
			if pw := strings.TrimSpace(string(data)); pw != "" {
				return pw
			}
		}
	}
	return ""
}

// registerBackends registers API backends from config.json into the registry.
// API keys are resolved from the vault only.
func registerBackends(registry *provider.Registry, cfg *cc.Config, apiHistory *provider.History, vaultMgr *vault.Manager) {
	if len(cfg.Backends) == 0 {
		log.Println("No API backends configured")
		cc.SetAllowedBackends(registry.BackendNames())
		return
	}
	for name, bcfg := range cfg.Backends {
		if name == "codex" {
			continue // Codex is a CLI provider, not an API backend; registered separately.
		}
		apiKey := resolveBackendAPIKey(name, bcfg, vaultMgr)
		if bcfg.Auth != "none" && apiKey == "" {
			log.Printf("backend %s: skipped (no API key in vault)", name)
			continue
		}
		auth := bcfg.Auth
		if auth == "" {
			auth = "bearer"
		}
		prov := provider.NewAPIProviderFromConfig(provider.APIProviderConfig{
			Name:         name,
			BaseURL:      bcfg.BaseURL,
			APIKey:       apiKey,
			Headers:      bcfg.Headers,
			DefaultModel: bcfg.DefaultModel,
			MaxTokens:    bcfg.MaxTokens,
			Auth:         auth,
		}, apiHistory)
		registry.Register(name, prov)
	}
	// Update AllowedBackends for tier validation.
	cc.SetAllowedBackends(registry.BackendNames())
}

// resolveBackendAPIKey resolves the API key for a backend from the vault.
func resolveBackendAPIKey(name string, bcfg cc.BackendConfig, vaultMgr *vault.Manager) string {
	if bcfg.Auth == "none" {
		log.Printf("backend %s: auth=none, no key needed", name)
		return ""
	}
	vaultKey := name + "_api_key"
	if vaultMgr == nil {
		log.Printf("backend %s: vault manager is nil, cannot resolve key", name)
		return ""
	}
	v, err := vaultMgr.GetSecret(vaultKey)
	if err != nil {
		log.Printf("backend %s: vault key %q error: %v", name, vaultKey, err)
		return ""
	}
	if v == "" {
		log.Printf("backend %s: vault key %q exists but is empty", name, vaultKey)
		return ""
	}
	log.Printf("backend %s: API key loaded from vault (%d chars)", name, len(v))
	return v
}

// registerCodex registers the OpenAI Codex CLI provider.
// Uses API key from vault if available; otherwise relies on codex login (auth.json).
func registerCodex(registry *provider.Registry, dataDir string, timeout time.Duration, vaultMgr *vault.Manager, cred *syscall.Credential) {
	var apiKey string
	if vaultMgr != nil {
		apiKey, _ = vaultMgr.GetSecret("codex_api_key")
	}

	// Check if codex binary exists before registering.
	if _, err := exec.LookPath("codex"); err != nil {
		log.Println("codex: skipped (binary not found)")
		return
	}

	prov := provider.NewCodexProvider(dataDir, timeout, apiKey, cred)
	registry.RegisterProvider("codex", prov)
	if apiKey != "" {
		log.Printf("codex: registered (API key %d chars)", len(apiKey))
	} else {
		log.Println("codex: registered (using codex login auth)")
	}

	// Update allowed backends so tiers UI shows codex.
	cc.SetAllowedBackends(registry.BackendNames())
}

func resolveTierParams(tierName string, tiers *cc.TiersConfig, dataDir string, reg *tooling.Registry, provRegistry *provider.Registry) tierParams {
	for _, t := range tiers.Tiers {
		if t.Name == tierName {
			model := t.Model
			backend := t.Backend
			// Auto-detect API backend from model name (e.g. "x-ai/grok-4" → first registered backend).
			if (backend == "" || backend == "cli") && strings.Contains(model, "/") {
				if provRegistry != nil {
					names := provRegistry.BackendNames()
					if len(names) > 0 {
						backend = names[0]
						log.Printf("[chat] tier %q: auto-detected backend=%s for model=%s", tierName, backend, model)
					}
				}
			}
			// For CLI backend, resolve short names to full model IDs.
			// For API backends, use the model string as-is.
			if backend == "" || backend == "cli" {
				model = router.ResolveModel(t.Model)
			}
			// Resolve tool wildcards into concrete tool names.
			tools := t.Tools
			if len(tools) == 1 && tools[0] == "*" {
				tools = tooling.ResolveWildcard(dataDir, reg)
				if len(tools) > 0 {
					log.Printf("[chat] tier %q: wildcard resolved to %d tools", tierName, len(tools))
				}
			} else if len(tools) == 1 && tools[0] == "*native" {
				// Only native Go tools (bash, read_file, grep, glob, write_file).
				if reg != nil {
					tools = reg.NativeToolNames()
				} else {
					tools = nil
				}
				if len(tools) > 0 {
					log.Printf("[chat] tier %q: native wildcard resolved to %d tools", tierName, len(tools))
				}
			}
			return tierParams{
				Model:                model,
				Tools:                tools,
				WriteCapable:         t.WriteCapable,
				Effort:               t.Effort,
				MaxTurns:             t.MaxTurns,
				OrchestratorMaxTurns: t.OrchestratorMaxTurns,
				MaxIterations:        t.MaxIterations,
				TimeoutMin:           t.TimeoutMin,
				Backend:              backend,
				SystemPrompt:         t.SystemPrompt,
				ContextWeight:        t.EffectiveContextWeight(),
			}
		}
	}
	// Tier not found - use defaults.
	return tierParams{Model: "claude-haiku-4-5"}
}

// autoEnableAgentTier enables orchestrator tiers in-memory when agent teams are configured.
// Does NOT modify the tiers.json file - only affects the runtime state.
func autoEnableAgentTier(tierStore cc.TierStore) {
	tiers := tierStore.Current()
	for i := range tiers.Tiers {
		if tiers.Tiers[i].IsOrchestrator() && !tiers.Tiers[i].Enabled {
			tiers.Tiers[i].Enabled = true
			log.Printf("auto-enabled orchestrator tier %q (agent teams found)", tiers.Tiers[i].Name)
			return
		}
	}
}

// firstFallbackTier returns DefaultFallback from config, or the first enabled
// tier, or the first tier overall. Never hardcodes a tier name.
func firstFallbackTier(tierStore cc.TierStore) string {
	cur := tierStore.Current()
	if cur.DefaultFallback != "" {
		return cur.DefaultFallback
	}
	for _, t := range cur.Tiers {
		if t.Enabled {
			return t.Name
		}
	}
	if len(cur.Tiers) > 0 {
		return cur.Tiers[0].Name
	}
	return ""
}

// watchConfigFiles polls config files for changes and sends reload events.
// tiersPathFn is called each tick so the watcher follows runtime tiers_file changes.
func watchConfigFiles(configDir string, dataDir string, tiersPathFn func() string, reloadCh chan cc.ReloadEvent) {
	type watchEntry struct {
		path  string
		event cc.ReloadEvent
		isDir bool // watch directory modtime (file add/remove)
	}

	staticEntries := []watchEntry{
		{filepath.Join(configDir, "config.json"), cc.ReloadConfig, false},
		{filepath.Join(configDir, "firewall.json"), cc.ReloadFirewall, false},
		{filepath.Join(dataDir, "agents", "teams"), cc.ReloadAgents, true},
	}

	modTimes := make(map[string]time.Time)
	for _, e := range staticEntries {
		if info, err := os.Stat(e.path); err == nil {
			modTimes[e.path] = info.ModTime()
		}
	}
	if p := tiersPathFn(); p != "" {
		if info, err := os.Stat(p); err == nil {
			modTimes[p] = info.ModTime()
		}
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Build the full entry list each tick so tiers path changes are picked up.
		entries := append(staticEntries, watchEntry{tiersPathFn(), cc.ReloadTiers, false})

		for _, e := range entries {
			info, err := os.Stat(e.path)
			if err != nil {
				if prev, ok := modTimes[e.path]; ok && !prev.IsZero() {
					delete(modTimes, e.path)
				}
				continue
			}
			// For directories, use the latest modtime of the dir itself or any file inside.
			mt := info.ModTime()
			if e.isDir {
				if dirEntries, err := os.ReadDir(e.path); err == nil {
					for _, de := range dirEntries {
						if fi, err := de.Info(); err == nil && fi.ModTime().After(mt) {
							mt = fi.ModTime()
						}
					}
				}
			}
			prev := modTimes[e.path]
			if !mt.Equal(prev) {
				modTimes[e.path] = mt
				if !prev.IsZero() {
					log.Printf("config watcher: %s changed, reloading", filepath.Base(e.path))
					select {
					case reloadCh <- e.event:
					default:
					}
				}
			}
		}
	}
}

// applyDNS writes /etc/resolv.conf with the configured DNS servers.
// Required for gVisor runtime which cannot use Docker's internal DNS (127.0.0.11).
func applyDNS(cfg *cc.Config) {
	servers := cfg.EffectiveDNS()
	var content string
	for _, s := range servers {
		content += "nameserver " + s + "\n"
	}
	if err := os.WriteFile("/etc/resolv.conf", []byte(content), 0o644); err != nil {
		// Read-only mount or permission denied — not fatal, just log.
		log.Printf("dns: could not write /etc/resolv.conf: %v (using existing)", err)
		return
	}
	log.Printf("dns: resolv.conf updated (%s)", strings.Join(servers, ", "))
}

// resolveTimezone loads an IANA timezone from config, falling back to TZ env then UTC.
func resolveTimezone(tz string) *time.Location {
	if tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil {
			log.Printf("warning: invalid timezone %q, falling back to TZ env or UTC: %v", tz, err)
		} else {
			log.Printf("scheduler: using timezone %s", tz)
			return loc
		}
	}
	// time.Local already respects the TZ environment variable.
	return time.Local
}

// syncVaultHostsToFirewall fetches vault service base_urls and registers
// their hostnames with the firewall so requests are tagged as "vault".
func syncVaultHostsToFirewall(mgr *vault.Manager, fw *firewall.Proxy) {
	if mgr == nil || fw == nil {
		return
	}
	services, err := mgr.Client().ListServices()
	if err != nil {
		log.Printf("[firewall] failed to list vault services for host tagging: %v", err)
		return
	}
	var hosts []string
	for _, svc := range services {
		if u, err := url.Parse(svc.BaseURL); err == nil && u.Hostname() != "" {
			hosts = append(hosts, u.Hostname())
		}
	}
	if len(hosts) > 0 {
		fw.SetVaultHosts(hosts)
		log.Printf("[firewall] vault hosts registered: %s", strings.Join(hosts, ", "))
	}
}

func parseAllowedChatIDs(s string) map[int64]bool {
	result := make(map[int64]bool)
	if s == "" {
		return result
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			result[id] = true
		}
	}
	return result
}
