<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { toasts } from '../stores/toast.svelte';
  import { events } from '../stores/events.svelte';
  import { nav } from '../stores/nav.svelte';
  import Card from '../components/shared/Card.svelte';
  import { Download, Power, PowerOff, Trash2, RefreshCw, Tag, User, FolderOpen, Package } from 'lucide-svelte';
  import { getIcon } from '../lib/icons';

  let apps = $state([]);
  let updates = $state([]);
  let loading = $state(true);
  let developerName = $state('');
  let developerSlugs = $state(new Set());

  const stateColors = {
    enabled: 'var(--green)',
    disabled: 'var(--yellow)',
    installed: 'var(--sapphire)',
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

      // Merge: local apps take priority, remote-only get "available" state
      const slugMap = new Map();
      for (const app of local) {
        slugMap.set(app.slug, { ...app });
      }
      for (const remote of catalog) {
        if (!slugMap.has(remote.slug)) {
          slugMap.set(remote.slug, { ...remote, state: 'available' });
        }
      }

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
                  {#if app.state === 'available'}
                    <button class="btn btn-sm btn-primary" onclick={() => doAction(app.slug, 'install')}>
                      <Download size={12} /> Install
                    </button>
                  {:else if app.state === 'enabled' && !isOwnApp(app)}
                    <button class="btn btn-sm btn-warning" onclick={() => doAction(app.slug, 'disable')}>
                      <PowerOff size={12} /> Disable
                    </button>
                  {:else if app.state === 'disabled'}
                    <button class="btn btn-sm btn-success" onclick={() => doAction(app.slug, 'enable')}>
                      <Power size={12} /> Enable
                    </button>
                  {:else if app.state === 'installed'}
                    <button class="btn btn-sm btn-success" onclick={() => doAction(app.slug, 'enable')}>
                      <Power size={12} /> Enable
                    </button>
                  {/if}

                  {#if app._update}
                    <button class="btn btn-sm btn-info" onclick={() => doAction(app.slug, 'update')}>
                      <RefreshCw size={12} /> Update to {app._update.remote_version}
                    </button>
                  {/if}

                  {#if app.state && app.state !== 'available' && !isOwnApp(app)}
                    <button class="btn btn-sm btn-danger" onclick={() => doAction(app.slug, 'uninstall', `Uninstall "${app.name}"? This will remove all app data.`)}>
                      <Trash2 size={12} /> Uninstall
                    </button>
                  {/if}

                  {#if isOwnApp(app)}
                    <span class="badge" style="background:var(--accent); font-size:0.7rem">Your app</span>
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
  .toolbar {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin-bottom: 1.5rem;
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
    font-size: 0.9rem;
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
    font-size: 1.5rem;
  }
  .app-title {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .badge {
    font-size: 0.7rem;
    padding: 0.1rem 0.4rem;
    border-radius: 4px;
    color: #fff;
    text-transform: uppercase;
    letter-spacing: 0.03em;
  }
  .app-meta {
    display: flex;
    gap: 1rem;
    font-size: 0.8rem;
    color: var(--text-dim);
  }
  .meta-item {
    display: flex;
    align-items: center;
    gap: 0.2rem;
  }
  .app-desc {
    font-size: 0.85rem;
    color: var(--text-dim);
    margin: 0;
  }
  .tool-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
  }
  .tool-tag {
    font-size: 0.7rem;
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
  :global(.spin) {
    animation: spin 1s linear infinite;
  }
  @keyframes spin {
    from { transform: rotate(0deg); }
    to { transform: rotate(360deg); }
  }
</style>
