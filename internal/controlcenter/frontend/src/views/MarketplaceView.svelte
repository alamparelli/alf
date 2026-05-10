<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { toasts } from '../stores/toast.svelte';
  import { events } from '../stores/events.svelte';
  import { nav } from '../stores/nav.svelte';
  import Card from '../components/shared/Card.svelte';
  import Toggle from '../components/shared/Toggle.svelte';
  import { Download, Trash2, RefreshCw, Tag, User, FolderOpen, Package, Code2, Sparkles, ShieldAlert } from 'lucide-svelte';
  import { getIcon } from '../lib/icons';

  let apps = $state([]);
  let updates = $state([]);
  let loading = $state(true);
  let developerName = $state('');
  let developerSlugs = $state(new Set());

  const stateColors = {
    installed: 'var(--green)',
    available: 'var(--text-dim)',
  };

  async function loadAll() {
    loading = true;
    try {
      const [localRes, catalogRes, updatesRes] = await Promise.all([
        api('GET', '/api/marketplace'),
        api('GET', '/api/marketplace/catalog').catch(() => []),
        api('GET', '/api/marketplace/updates').catch(() => []),
      ]);

      const local = Array.isArray(localRes) ? localRes : [];
      const catalog = Array.isArray(catalogRes) ? catalogRes : [];
      updates = Array.isArray(updatesRes) ? updatesRes : [];

      // Catalog is the source of truth: only show apps from the remote registry.
      // Merge local state (installed/enabled/disabled) into catalog entries.
      const localMap = new Map(local.map(a => [a.slug, a]));
      const slugMap = new Map();
      for (const remote of catalog) {
        const installed = localMap.get(remote.slug);
        if (installed) {
          slugMap.set(remote.slug, { ...remote, ...installed });
        } else {
          slugMap.set(remote.slug, { ...remote, state: 'available' });
        }
      }
      // No fallback: only show apps from the remote catalog.
      // Installed apps not in the catalog are not shown in the marketplace view.

      // Attach update info
      const updateMap = new Map(updates.map(u => [u.slug, u]));
      for (const [slug, app] of slugMap) {
        const upd = updateMap.get(slug);
        if (upd) {
          app._update = upd;
        }
      }

      // Sort by category then name
      apps = [...slugMap.values()].sort((a, b) => {
        const catCmp = (a.category || '').localeCompare(b.category || '');
        if (catCmp !== 0) return catCmp;
        return (a.name || '').localeCompare(b.name || '');
      });
    } catch (e) {
      toasts.error('Failed to load marketplace: ' + e.message);
    } finally {
      loading = false;
    }
  }

  let categories = $derived(
    [...new Set(apps.map(a => a.category || 'Uncategorized'))]
  );

  function appsForCategory(cat) {
    return apps.filter(a => (a.category || 'Uncategorized') === cat);
  }

  async function doAction(slug, action, confirmMsg) {
    if (confirmMsg && !confirm(confirmMsg)) return;
    try {
      await api('POST', `/api/marketplace/${encodeURIComponent(slug)}/${action}`);
      toasts.success(`${action} successful: ${slug}`);
      // Retry loading until state actually changes or max retries
      const oldState = apps.find(a => a.slug === slug)?.state;
      let retries = 0;
      while (retries < 5) {
        await new Promise(r => setTimeout(r, 600));
        await loadAll();
        const newState = apps.find(a => a.slug === slug)?.state;
        if (newState !== oldState) break;
        retries++;
      }
      nav.refreshApps?.();
      checkBadge();
    } catch (e) {
      toasts.error(`${action} failed: ${e.message}`);
    }
  }

  // #420 — Migrate a legacy Go-kind app to wasm-app by handing the job
  // to the wasm-builder skill via a background task. The task reads
  // ~/data/apps/<slug>/manifest.json + sources, ports the backend to a
  // Go reactor-mode WASM module, writes the new wasm-app envelope, and
  // replaces the bundle. Data is preserved.
  async function migrateToWASM(app) {
    const ok = confirm(
      `Migrate "${app.name}" to wasm-app?\n\n` +
      `ALF will run wasm-builder in a background task, port the backend ` +
      `to a WASM module, and replace the legacy bundle. Your existing ` +
      `data/ directory is preserved. You can follow progress in the Tasks view.`
    );
    if (!ok) return;
    const prompt =
      `Migrate the "${app.slug}" app to wasm-app kind using the wasm-builder skill. ` +
      `The legacy bundle is at ~/data/apps/${app.slug}/ — read its manifest.json + ` +
      `manifest.toml to understand the declared tools and actions, port the backend ` +
      `to a Go reactor-mode WASM module declaring only the fs.reads/fs.writes paths ` +
      `actually needed, write a wasm-app manifest.toml envelope, and replace the ` +
      `bundle in place. Preserve the existing data/ directory across the migration.`;
    try {
      await api('POST', '/api/tasks', {
        message: prompt,
        need_validation: false,
      });
      toasts.success(`Migration task launched for "${app.slug}". Follow progress in Tasks.`);
    } catch (e) {
      toasts.error(`Migration launch failed: ${e.message}`);
    }
  }

  async function checkBadge() {
    try {
      const upd = await api('GET', '/api/marketplace/updates');
      nav.setMarketplaceBadge?.(Array.isArray(upd) && upd.length > 0);
    } catch {}
  }

  function stateLabel(state) {
    return (state || 'available').charAt(0).toUpperCase() + (state || 'available').slice(1);
  }

  async function loadDeveloperStatus() {
    try {
      const data = await api('GET', '/api/marketplace/developer');
      if (data.is_developer) {
        developerName = data.developer || '';
        // Load catalog to find which apps the dev published
        const catalog = await api('GET', '/api/marketplace/catalog').catch(() => []);
        if (Array.isArray(catalog) && developerName) {
          const devLower = developerName.toLowerCase();
          developerSlugs = new Set(
            catalog
              .filter(function(a) { return (a.author || '').toLowerCase() === devLower || (a.developer || '').toLowerCase() === devLower; })
              .map(function(a) { return a.slug; })
          );
        }
      }
    } catch {}
  }

  function isOwnApp(app) {
    return developerSlugs.has(app.slug);
  }

  onMount(() => {
    loadAll();
    checkBadge();
    loadDeveloperStatus();
    const unsub = events.subscribe('marketplace', loadAll);
    return () => unsub();
  });
</script>

<div class="view-marketplace">
  <h2>Marketplace</h2>
  <div class="toolbar">
    <button class="btn btn-ghost" onclick={loadAll} disabled={loading}>
      <RefreshCw size={14} class={loading ? 'spin' : ''} /> Refresh
    </button>
  </div>

  <!-- Developer mode toggle -->
  <Card>
    <div class="dev-toggle">
      <div class="dev-toggle-info">
        <Code2 size={18} />
        <div>
          <strong>Developer Tools</strong>
          <span class="dev-toggle-desc">Publish apps to the marketplace registry</span>
        </div>
      </div>
      <Toggle checked={nav.developerMode} label="" onchange={() => nav.toggleDeveloperMode()} />
    </div>
  </Card>

  <!-- #420 §4.1 lockdown notice — legacy Go-kind apps no longer load. -->
  <Card>
    <div class="lockdown-notice">
      <ShieldAlert size={18} />
      <div>
        <strong>Lockdown active</strong>
        <span class="lockdown-desc">
          Legacy Go-kind apps are refused at boot under the §4.1 isolation
          rule. Click <em>Migrate to WASM</em> on any installed app to convert
          it to a wasm-app — the wasm-builder skill ports the backend in a
          background task and your data is preserved.
        </span>
      </div>
    </div>
  </Card>

  {#if loading}
    <div class="loading">Loading marketplace...</div>
  {:else if apps.length === 0}
    <div class="empty">No apps available.</div>
  {:else}
    {#each categories as cat}
      <div class="category-section">
        <h3 class="category-heading"><FolderOpen size={16} /> {cat}</h3>
        <div class="app-grid">
          {#each appsForCategory(cat) as app (app.slug)}
            <Card>
              <div class="app-card">
                <div class="app-header">
                  <span class="app-icon">
                    {#if getIcon(app.icon)}
                      <svelte:component this={getIcon(app.icon)} size={20} />
                    {:else}
                      <Package size={20} />
                    {/if}
                  </span>
                  <div class="app-title">
                    <strong>{app.name}</strong>
                    {#if app.state && app.state !== 'available'}
                      <span class="badge" style="background:{stateColors[app.state] || stateColors.available}">
                        {stateLabel(app.state)}
                      </span>
                    {/if}
                  </div>
                </div>

                <div class="app-meta">
                  {#if app.version}
                    <span class="meta-item"><Tag size={12} /> {app.version}</span>
                  {/if}
                  {#if app.author}
                    <span class="meta-item"><User size={12} /> {app.author}</span>
                  {/if}
                </div>

                {#if app.description}
                  <p class="app-desc">{app.description}</p>
                {/if}

                {#if app.tools?.length}
                  <div class="tool-tags">
                    {#each app.tools as tool}
                      <span class="tool-tag">{tool.name}</span>
                    {/each}
                  </div>
                {/if}

                <div class="app-actions">
                  {#if app.state === 'available' && !isOwnApp(app)}
                    <button class="btn btn-sm btn-primary" onclick={() => doAction(app.slug, 'install')}>
                      <Download size={12} /> Install
                    </button>
                  {/if}

                  {#if app._update}
                    <button class="btn btn-sm btn-info" onclick={() => doAction(app.slug, 'update')}>
                      <RefreshCw size={12} /> Update to {app._update.remote_version}
                    </button>
                  {/if}

                  {#if app.state === 'installed' && !isOwnApp(app)}
                    <button class="btn btn-sm btn-accent" title="Convert this app to wasm-app via the wasm-builder skill. Data preserved." onclick={() => migrateToWASM(app)}>
                      <Sparkles size={12} /> Migrate to WASM
                    </button>
                    <button class="btn btn-sm btn-danger" onclick={() => doAction(app.slug, 'uninstall', `Uninstall "${app.name}"? This will remove all app data.`)}>
                      <Trash2 size={12} /> Uninstall
                    </button>
                  {/if}

                  {#if isOwnApp(app)}
                    <span class="badge" style="background:var(--accent); font-size:var(--font-xs, 11px)">Your app</span>
                  {/if}
                </div>
              </div>
            </Card>
          {/each}
        </div>
      </div>
    {/each}
  {/if}
</div>

<style>
  .view-marketplace {
    padding: 8px 0;
    width: 100%;
  }
  .view-marketplace h2 {
    margin-bottom: 16px;
  }
  .loading, .empty {
    text-align: center;
    padding: 3rem;
    color: var(--text-dim);
  }
  .category-section {
    margin-bottom: 2rem;
  }
  .category-heading {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    font-size: var(--font-sm, 13px);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-dim);
    border-bottom: 1px solid var(--border);
    padding-bottom: 0.4rem;
    margin-bottom: 1rem;
  }
  .app-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
    gap: 1rem;
  }
  .app-card {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .app-header {
    display: flex;
    align-items: center;
    gap: 0.6rem;
  }
  .app-icon {
    font-size: var(--font-xl, 24px);
  }
  .app-title {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .badge {
    color: #fff;
  }
  .app-meta {
    display: flex;
    gap: 1rem;
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
  }
  .meta-item {
    display: flex;
    align-items: center;
    gap: 0.2rem;
  }
  .app-desc {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    margin: 0;
  }
  .tool-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
  }
  .tool-tag {
    font-size: var(--font-xs, 11px);
    padding: 0.1rem 0.4rem;
    border-radius: 3px;
    background: var(--bg-input);
    color: var(--text-dim);
  }
  .app-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    margin-top: 0.3rem;
  }
  .dev-toggle {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  .dev-toggle-info {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .dev-toggle-info strong {
    display: block;
    font-size: var(--font-sm, 13px);
  }
  .dev-toggle-desc {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
  }
  .lockdown-notice {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    color: var(--text);
  }
  .lockdown-notice :global(svg) {
    flex-shrink: 0;
    margin-top: 2px;
    color: var(--accent);
  }
  .lockdown-notice strong {
    display: block;
    font-size: var(--font-sm, 13px);
  }
  .lockdown-desc {
    display: block;
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    margin-top: 2px;
  }
  .lockdown-desc em {
    font-style: normal;
    color: var(--accent);
    font-weight: 600;
  }
</style>
