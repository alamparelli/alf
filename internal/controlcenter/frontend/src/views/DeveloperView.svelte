<script lang="ts">
  import { onMount } from 'svelte'
  import Card from '../components/shared/Card.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'
  import { nav } from '../stores/nav.svelte'
  import { Loader2, RefreshCw, CheckCircle, XCircle, AlertTriangle, Trash2 } from 'lucide-svelte'

  // Connection
  let connected = $state(false)
  let developer = $state('')
  let connStatus = $state<'pending'|'ok'|'err'>('pending')
  let connLabel = $state('Checking...')

  // Apps
  let localApps = $state<string[]>([])
  let marketplaceSlugs = $state<string[]>([])
  let foreignSlugs = $state<string[]>([])
  let catalogApps = $state<any[]>([])

  // Tools & Skills
  let tools = $state<any[]>([])
  let skills = $state<string[]>([])
  let selectedTools = $state<string[]>([])

  // Form
  let selectedApp = $state('')
  let slug = $state('')
  let version = $state('0.1.0')
  let name = $state('')
  let desc = $state('')
  let category = $state('')
  let icon = $state('')
  let changelog = $state('')
  let selectedSkill = $state('')

  // State
  let publishing = $state(false)
  let validating = $state(false)
  let validateResult = $state<{errors: string[], warnings: string[]} | null>(null)
  let publishResult = $state<{success?: boolean, error?: string, message?: string} | null>(null)

  async function checkConnection() {
    connStatus = 'pending'
    connLabel = 'Checking...'
    try {
      const data = await api<any>('GET', '/api/developer/status')
      if (data.connected) {
        connected = true
        developer = data.developer || ''
        connStatus = 'ok'
        connLabel = 'Connected as ' + (data.developer || 'developer')
        loadCatalog()
      } else {
        connected = false
        connStatus = 'err'
        connLabel = data.error?.includes('locked') ? 'Vault is locked' :
                    data.error?.includes('not configured') ? 'No marketplace service configured' :
                    'Connection failed'
      }
    } catch {
      connStatus = 'err'
      connLabel = 'Check failed'
    }
  }

  async function loadApps() {
    try {
      const data = await api<any>('GET', '/api/developer/apps')
      localApps = data.apps || []
    } catch { /* silent */ }
  }

  async function loadTools() {
    try {
      const data = await api<any>('GET', '/api/developer/tools')
      tools = (data.tools || []).sort((a: any, b: any) => (a.name || '').localeCompare(b.name || ''))
    } catch { /* silent */ }
  }

  async function loadSkills() {
    try {
      const data = await api<any>('GET', '/api/developer/skills')
      skills = data.skills || []
    } catch { /* silent */ }
  }

  async function loadCatalog() {
    try {
      const data = await api<any>('GET', '/api/developer/catalog')
      const all = data.apps || []
      if (developer) {
        foreignSlugs = all.filter((a: any) => a.developer !== developer && a.author !== developer).map((a: any) => a.slug)
        catalogApps = all.filter((a: any) => a.developer === developer || a.author === developer)
      } else {
        catalogApps = all
      }
    } catch { /* silent */ }
  }

  async function loadMarketplaceSlugs() {
    try {
      const data = await api<any>('GET', '/api/marketplace')
      marketplaceSlugs = (Array.isArray(data) ? data : []).map((a: any) => a.slug).filter(Boolean)
    } catch { marketplaceSlugs = [] }
  }

  async function selectApp(appName: string) {
    if (!appName) return
    slug = appName
    selectedTools = []
    try {
      const meta = await api<any>('GET', `/api/developer/app-meta?slug=${encodeURIComponent(appName)}`)
      const app = meta.app || {}
      const manifest = meta.manifest || {}
      name = manifest.name || app.name || ''
      desc = manifest.description || app.description || ''
      icon = manifest.icon || app.icon || ''
      category = manifest.category || ''
      if (manifest.version) {
        const parts = manifest.version.split('.')
        if (parts.length === 3) parts[2] = String(parseInt(parts[2] || '0') + 1)
        version = parts.join('.')
      }
      changelog = ''
      if (manifest.tools?.length) {
        selectedTools = manifest.tools.map((t: any) => t.name)
      }
    } catch { /* silent */ }
  }

  function toggleTool(toolName: string) {
    if (selectedTools.includes(toolName)) {
      selectedTools = selectedTools.filter(t => t !== toolName)
    } else {
      selectedTools = [...selectedTools, toolName]
    }
  }

  async function validate() {
    validating = true
    validateResult = null
    try {
      validateResult = await api<any>('POST', '/api/developer/validate', {
        slug, name, version, category, tools: selectedTools
      })
    } catch (e: any) {
      validateResult = { errors: [e.message || 'Validation failed'], warnings: [] }
    } finally {
      validating = false
    }
  }

  async function publish() {
    publishing = true
    publishResult = null
    try {
      publishResult = await api<any>('POST', '/api/developer/publish', {
        slug, name, version, description: desc, category, icon,
        changelog, tools: selectedTools, skill: selectedSkill
      })
      if (publishResult?.success) {
        toasts.show('Published successfully!', 'success')
        loadCatalog()
      }
    } catch (e: any) {
      publishResult = { error: e.message || 'Publish failed' }
    } finally {
      publishing = false
    }
  }

  async function unpublish(appSlug: string) {
    if (!confirm(`Unpublish "${appSlug}" from the marketplace?`)) return
    try {
      await api('POST', '/api/developer/unpublish', { slug: appSlug })
      toasts.show('Unpublished', 'success')
      loadCatalog()
    } catch (e: any) {
      toasts.show(e.message || 'Failed', 'error')
    }
  }

  function fillFromCatalog(app: any) {
    slug = app.slug || ''
    name = app.name || ''
    desc = app.description || ''
    category = app.category || ''
    icon = app.icon || ''
    const v = app.version || '0.1.0'
    const parts = v.split('.')
    if (parts.length === 3) parts[2] = String(parseInt(parts[2] || '0') + 1)
    version = parts.join('.')
    selectedApp = app.slug
    selectApp(app.slug)
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }

  let filteredApps = $derived(localApps.filter(a =>
    !foreignSlugs.includes(a) &&
    a !== 'developer'
  ))

  onMount(() => {
    checkConnection()
    loadMarketplaceSlugs().then(() => loadApps())
    loadTools()
    loadSkills()
  })
</script>

<div class="dev-view">
  <h2>Developer Tools</h2>

  <!-- Connection -->
  <Card>
    <h3>Marketplace Connection</h3>
    <div class="conn-status">
      <span class="conn-dot {connStatus}"></span>
      <span>{connLabel}</span>
    </div>
    {#if connStatus === 'err'}
      <p class="hint" style="margin-top:8px">
        Configure a <code>marketplace</code> service in <button class="link-btn" onclick={() => nav.navigateTo('vault')}>Vault</button> with your registry URL and API key.
      </p>
    {/if}
  </Card>

  <!-- Publish -->
  <Card>
    <h3>Publish to Marketplace</h3>

    <div class="form-group">
      <label>Source App</label>
      <select class="input mono" bind:value={selectedApp} onchange={() => selectApp(selectedApp)}>
        <option value="">-- Pick a local app --</option>
        {#each filteredApps as app}
          <option value={app}>{app}</option>
        {/each}
      </select>
      <span class="hint">Local app directory. Auto-fills fields from manifest.</span>
    </div>

    <div class="form-row">
      <div class="form-group">
        <label>Slug</label>
        <input class="input mono" bind:value={slug} placeholder="my-app">
      </div>
      <div class="form-group">
        <label>Version</label>
        <input class="input mono" bind:value={version} placeholder="0.1.0">
      </div>
    </div>

    <div class="form-group">
      <label>Display Name</label>
      <input class="input" bind:value={name} placeholder="My App">
    </div>
    <div class="form-group">
      <label>Description</label>
      <input class="input" bind:value={desc} placeholder="One-liner describing what the app does">
    </div>
    <div class="form-row">
      <div class="form-group">
        <label>Category</label>
        <input class="input" bind:value={category} placeholder="utilities">
      </div>
      <div class="form-group">
        <label>Icon</label>
        <input class="input mono" bind:value={icon} placeholder="box (Lucide icon name)">
      </div>
    </div>
    <div class="form-group">
      <label>What's New</label>
      <input class="input" bind:value={changelog} placeholder="e.g. Added dark mode, fixed export bug">
      <span class="hint">Changelog shown to users when they update.</span>
    </div>

    <hr class="sep">

    <div class="form-group">
      <label>Bundled Tools</label>
      <span class="hint">Toggle tools to include in the app package.</span>
      <div class="tool-chips">
        {#if tools.length === 0}
          <span class="empty">No user tools found.</span>
        {:else}
          {#each tools as tool}
            <button
              class="tool-chip"
              class:selected={selectedTools.includes(tool.name)}
              onclick={() => toggleTool(tool.name)}
              title={tool.description || ''}
            >{tool.name}</button>
          {/each}
        {/if}
      </div>
    </div>

    <div class="form-group">
      <label>Bundled Skill</label>
      <select class="input mono" bind:value={selectedSkill}>
        <option value="">None</option>
        {#each skills as skill}
          <option value={skill}>{skill}</option>
        {/each}
      </select>
    </div>

    <div class="form-actions">
      <button class="btn" onclick={validate} disabled={validating}>
        {#if validating}<Loader2 size={14} class="spin" />{/if} Validate
      </button>
      <button class="btn btn-primary" onclick={publish} disabled={publishing || !connected || !slug || !name}>
        {#if publishing}<Loader2 size={14} class="spin" />{/if} Publish
      </button>
    </div>

    {#if validateResult}
      <div class="status-box" class:error={validateResult.errors?.length} class:success={!validateResult.errors?.length}>
        {#each validateResult.errors || [] as e}
          <div><XCircle size={14} /> {e}</div>
        {/each}
        {#each validateResult.warnings || [] as w}
          <div><AlertTriangle size={14} /> {w}</div>
        {/each}
        {#if !validateResult.errors?.length && !validateResult.warnings?.length}
          <div><CheckCircle size={14} /> All checks passed</div>
        {/if}
      </div>
    {/if}

    {#if publishResult}
      <div class="status-box" class:error={publishResult.error} class:success={publishResult.success}>
        {publishResult.success ? publishResult.message || 'Published!' : publishResult.error}
      </div>
    {/if}
  </Card>

  <!-- Catalog -->
  <Card>
    <div style="display:flex;justify-content:space-between;align-items:center">
      <h3 style="margin:0">Marketplace Catalog</h3>
      <button class="btn btn-sm" onclick={checkConnection}><RefreshCw size={13} /> Refresh</button>
    </div>
    {#if catalogApps.length === 0}
      <p class="empty">No apps in catalog.</p>
    {:else}
      <div class="catalog-list">
        {#each catalogApps as app}
          <div class="catalog-item" onclick={() => fillFromCatalog(app)}>
            <div>
              <strong>{app.name || app.slug}</strong>
              <span class="app-slug">{app.slug}</span>
            </div>
            <div class="catalog-actions">
              {#if app.version}
                <span class="app-ver">v{app.version}</span>
              {/if}
              <button class="btn btn-sm btn-danger" onclick={(e: MouseEvent) => { e.stopPropagation(); unpublish(app.slug) }}>
                <Trash2 size={12} />
              </button>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </Card>
</div>

<style>
  .dev-view { width: 100%; max-width: 760px; margin: 0 auto; padding: 8px 0; }
  .dev-view h2 { margin-bottom: 16px; }
  .dev-view h3 { margin-bottom: 8px; font-size: var(--font-md, 15px); }

  .conn-status { display: flex; align-items: center; gap: 8px; font-size: var(--font-sm, 13px); }
  .conn-dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; }
  .conn-dot.ok { background: var(--green); }
  .conn-dot.err { background: var(--red); }
  .conn-dot.pending { background: var(--yellow, orange); }

  .form-group label { display: block; font-size: var(--font-sm, 13px); color: var(--text-dim); margin-bottom: 2px; }
  .form-row { display: flex; gap: 0.75rem; }
  .form-row > * { flex: 1; }
  .hint { font-size: var(--font-xs, 11px); color: var(--text-dim); margin-top: 4px; display: block; }
  .mono { font-family: 'JetBrains Mono', 'SF Mono', monospace; font-size: var(--font-sm, 13px); }
  .sep { border: none; border-top: 1px solid var(--border); margin: 1rem 0; }

  select.input { cursor: pointer; appearance: auto; }

  .tool-chips { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 6px; }
  .tool-chip {
    font-size: var(--font-xs, 11px); padding: 2px 8px; background: var(--bg);
    border-radius: 4px; color: var(--text-dim); cursor: pointer; transition: all 0.15s; border: none;
  }
  .tool-chip.selected { background: var(--accent); color: var(--on-accent); }
  .tool-chip:hover { background: color-mix(in srgb, var(--accent) 15%, var(--bg)); }

  .form-actions { display: flex; gap: 8px; margin-top: 0.75rem; }
  .btn-danger { color: var(--red); }

  .status-box {
    margin-top: 0.6rem; padding: 0.5rem 0.65rem; border-radius: var(--radius, 8px);
    font-size: var(--font-sm, 13px); white-space: pre-wrap; word-break: break-all;
    font-family: 'JetBrains Mono', monospace;
  }
  .status-box > div { display: flex; align-items: center; gap: 6px; margin: 2px 0; }
  .status-box.error { background: color-mix(in srgb, var(--red) 12%, transparent); color: var(--red); }
  .status-box.success { background: color-mix(in srgb, var(--green) 12%, transparent); color: var(--green); }

  .catalog-list { margin-top: 8px; }
  .catalog-item {
    display: flex; justify-content: space-between; align-items: center;
    padding: 0.5rem; border-bottom: 1px solid var(--border); cursor: pointer;
    font-size: var(--font-sm, 13px); border-radius: 4px; transition: background 0.15s;
  }
  .catalog-item:hover { background: var(--bg); }
  .catalog-item:last-child { border-bottom: none; }
  .catalog-actions { display: flex; align-items: center; gap: 8px; }
  .app-slug { font-family: 'JetBrains Mono', monospace; font-size: var(--font-xs, 11px); color: var(--text-dim); margin-left: 6px; }
  .app-ver { font-size: var(--font-xs, 11px); color: var(--text-dim); background: var(--bg); padding: 0.1rem 0.4rem; border-radius: var(--radius, 8px); }
  .empty { color: var(--text-dim); font-size: var(--font-sm, 13px); font-style: italic; padding: 0.5rem 0; }
  .link-btn { background: none; border: none; color: var(--accent); cursor: pointer; font: inherit; padding: 0; text-decoration: underline; }

  @keyframes spin { to { transform: rotate(360deg); } }
  :global(.spin) { animation: spin 1s linear infinite; }
</style>
