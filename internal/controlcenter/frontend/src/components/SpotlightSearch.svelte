<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte'
  import { nav, SYSTEM_TABS } from '../stores/nav.svelte'
  import { api } from '../lib/api'
  import {
    MessageCircle, Home, Terminal, Layers, CalendarClock, Users,
    SlidersHorizontal, Shield, Lock, Store, Settings, ScrollText,
    BookOpen, Search, FileText, FileCode, FileImage, FileSpreadsheet,
    File, Package, X, FolderOpen, Zap, Filter, Mail
  } from 'lucide-svelte'
  import { getIcon } from '../lib/icons'
  import { spotlightSettings } from '../stores/spotlight.svelte'

  let open = $state(false)
  let query = $state('')
  let selectedIndex = $state(0)
  let searchInput: HTMLInputElement
  let debounceTimer: ReturnType<typeof setTimeout> | null = null

  // Results
  let systemResults = $state<typeof SYSTEM_TABS>([])
  let appResults = $state<any[]>([])
  let marketplaceResults = $state<any[]>([])
  let fileResults = $state<any[]>([])
  let docResults = $state<any[]>([])
  let loading = $state(false)
  let showFilters = $state(false)

  // Folder filter: user can exclude folders from file search
  // Always-hidden dirs (internal, never useful in search)
  const alwaysHidden = new Set(['.git', '.claude', '.cache', '.local', 'node_modules', 'go-path', 'docs'])
  let availableFolders = $state<string[]>([])

  async function loadFolders() {
    try {
      const data = await api<any>('/api/workspace?path=')
      if (data.type === 'directory' && data.entries) {
        availableFolders = data.entries
          .filter((e: any) => e.is_dir && !e.name.startsWith('.') && !alwaysHidden.has(e.name))
          .map((e: any) => e.name)
          .sort()
      }
    } catch { /* silent */ }
  }

  function loadExcluded(): string[] {
    try {
      const v = localStorage.getItem('spotlight:exclude')
      return v ? JSON.parse(v) : []
    } catch { return [] }
  }

  function saveExcluded(dirs: string[]) {
    localStorage.setItem('spotlight:exclude', JSON.stringify(dirs))
  }

  let excludedDirs = $state<string[]>(loadExcluded())

  function toggleFolder(dir: string) {
    if (excludedDirs.includes(dir)) {
      excludedDirs = excludedDirs.filter(d => d !== dir)
    } else {
      excludedDirs = [...excludedDirs, dir]
    }
    saveExcluded(excludedDirs)
    if (query) searchRemote(query)
  }

  // Icon map for system tabs
  const iconMap: Record<string, any> = {
    'message-circle': MessageCircle,
    home: Home,
    terminal: Terminal,
    layers: Layers,
    'calendar-clock': CalendarClock,
    users: Users,
    zap: Zap,
    'sliders-horizontal': SlidersHorizontal,
    shield: Shield,
    lock: Lock,
    store: Store,
    settings: Settings,
    'scroll-text': ScrollText,
    'book-open': BookOpen,
    'mail': Mail,
  }

  // File icon by extension
  function fileIcon(ext: string) {
    const codeExts = ['js', 'ts', 'go', 'py', 'rs', 'rb', 'sh', 'json', 'yaml', 'yml', 'toml', 'xml', 'html', 'css', 'svelte', 'vue', 'jsx', 'tsx']
    const imageExts = ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'ico']
    const spreadsheetExts = ['csv', 'xls', 'xlsx']
    if (codeExts.includes(ext)) return FileCode
    if (imageExts.includes(ext)) return FileImage
    if (spreadsheetExts.includes(ext)) return FileSpreadsheet
    if (['md', 'txt', 'doc', 'docx', 'pdf'].includes(ext)) return FileText
    return File
  }

  // All flattened results for keyboard nav
  let allResults = $derived.by(() => {
    const items: { type: string; data: any }[] = []
    for (const t of systemResults) items.push({ type: 'system', data: t })
    for (const a of appResults) items.push({ type: 'app', data: a })
    for (const m of marketplaceResults) items.push({ type: 'marketplace', data: m })
    for (const f of fileResults) items.push({ type: 'file', data: f })
    for (const d of docResults) items.push({ type: 'doc', data: d })
    return items
  })

  function filterSystem(q: string) {
    if (!q) { systemResults = []; return }
    const lower = q.toLowerCase()
    systemResults = SYSTEM_TABS.filter(t =>
      t.label.toLowerCase().includes(lower) || t.view.toLowerCase().includes(lower)
    )
  }

  async function searchRemote(q: string) {
    if (!q) {
      appResults = []; marketplaceResults = []; fileResults = []; docResults = []
      return
    }
    loading = true
    try {
      const excludeParam = excludedDirs.length > 0 ? `&exclude=${encodeURIComponent(excludedDirs.join(','))}` : ''
      const data = await api<any>(`/api/search?q=${encodeURIComponent(q)}${excludeParam}`)
      appResults = data.apps || []
      marketplaceResults = (data.marketplace || []).filter((a: any) => a.state !== 'disabled')
      fileResults = data.files || []
      docResults = data.docs || []
    } catch {
      appResults = []; marketplaceResults = []; fileResults = []; docResults = []
    } finally {
      loading = false
    }
  }

  function onQueryChange(q: string) {
    filterSystem(q)
    selectedIndex = 0
    if (debounceTimer) clearTimeout(debounceTimer)
    debounceTimer = setTimeout(() => searchRemote(q), 300)
  }

  $effect(() => { onQueryChange(query) })

  function selectResult(item: { type: string; data: any }) {
    close()
    switch (item.type) {
      case 'system':
        nav.navigateTo(item.data.view)
        break
      case 'app':
        nav.navigateTo(`page:${item.data.slug || item.data.name}`)
        break
      case 'marketplace':
        nav.navigateTo('marketplace')
        break
      case 'file':
        nav.navigateTo('home')
        // Dispatch event so HomeView can open the file or directory
        setTimeout(() => {
          const eventName = item.data.is_dir ? 'alf:open-dir' : 'alf:open-file'
          window.dispatchEvent(new CustomEvent(eventName, { detail: { path: item.data.path } }))
        }, 100)
        break
      case 'doc':
        nav.navigateTo(`docs:${item.data.id || item.data.name}`)
        break
    }
  }

  function close() {
    open = false
    query = ''
    selectedIndex = 0
  }

  function handleKeydown(e: KeyboardEvent) {
    // Global: Cmd+<key> / Ctrl+<key>
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === spotlightSettings.shortcutKey) {
      e.preventDefault()
      open = !open
      if (open) {
        // Focus after DOM update
        setTimeout(() => searchInput?.focus(), 0)
      }
      return
    }

    if (!open) return

    if (e.key === 'Escape') {
      e.preventDefault()
      close()
      return
    }

    if (e.key === 'ArrowDown') {
      e.preventDefault()
      if (allResults.length > 0) {
        selectedIndex = (selectedIndex + 1) % allResults.length
        tick().then(() => document.querySelector('.spotlight-item.selected')?.scrollIntoView({ block: 'nearest' }))
      }
      return
    }

    if (e.key === 'ArrowUp') {
      e.preventDefault()
      if (allResults.length > 0) {
        selectedIndex = (selectedIndex - 1 + allResults.length) % allResults.length
        tick().then(() => document.querySelector('.spotlight-item.selected')?.scrollIntoView({ block: 'nearest' }))
      }
      return
    }

    if (e.key === 'Enter') {
      e.preventDefault()
      if (allResults[selectedIndex]) {
        selectResult(allResults[selectedIndex])
      }
      return
    }
  }

  function handleOpenSpotlight() {
    open = true
    setTimeout(() => searchInput?.focus(), 0)
  }

  onMount(() => {
    window.addEventListener('keydown', handleKeydown)
    window.addEventListener('alf:open-spotlight', handleOpenSpotlight)
    loadFolders()
  })

  onDestroy(() => {
    window.removeEventListener('keydown', handleKeydown)
    window.removeEventListener('alf:open-spotlight', handleOpenSpotlight)
    if (debounceTimer) clearTimeout(debounceTimer)
  })

  // State badge color
  function stateBadgeClass(state: string): string {
    switch (state) {
      case 'enabled': return 'badge-green'
      case 'installed': return 'badge-blue'
      case 'disabled': return 'badge-dim'
      case 'available': return 'badge-yellow'
      default: return 'badge-dim'
    }
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="spotlight-overlay" onclick={close}>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="spotlight-modal" onclick={(e: MouseEvent) => e.stopPropagation()}>
      <div class="spotlight-input-row">
        <Search size={16} />
        <input
          type="text"
          bind:this={searchInput}
          bind:value={query}
          placeholder="Search tabs, apps, files, docs..."
          class="spotlight-input"
          autocomplete="off"
          spellcheck="false"
        />
        <button class="spotlight-close" onclick={close}><X size={14} /></button>
      </div>

      <div class="spotlight-results">
        {#if allResults.length === 0 && query && !loading}
          <div class="spotlight-empty">No results found</div>
        {/if}

        {#if loading && allResults.length === 0}
          <div class="spotlight-empty">Searching...</div>
        {/if}

        <!-- System Tabs -->
        {#if systemResults.length > 0}
          <div class="spotlight-category">System</div>
          {#each systemResults as tab, i}
            {@const globalIdx = allResults.findIndex(r => r.type === 'system' && r.data === tab)}
            <button
              class="spotlight-item" class:selected={selectedIndex === globalIdx}
              onclick={() => selectResult({ type: 'system', data: tab })}
              onmouseenter={() => selectedIndex = globalIdx}
            >
              <span class="spotlight-icon">
                {#if iconMap[tab.icon]}
                  <svelte:component this={iconMap[tab.icon]} size={16} />
                {/if}
              </span>
              <span class="spotlight-label">{tab.label}</span>
              <span class="spotlight-hint">Tab</span>
            </button>
          {/each}
        {/if}

        <!-- Apps -->
        {#if appResults.length > 0}
          <div class="spotlight-category">Apps</div>
          {#each appResults as app}
            {@const globalIdx = allResults.findIndex(r => r.type === 'app' && r.data === app)}
            <button
              class="spotlight-item" class:selected={selectedIndex === globalIdx}
              onclick={() => selectResult({ type: 'app', data: app })}
              onmouseenter={() => selectedIndex = globalIdx}
            >
              <span class="spotlight-icon">
                {#if getIcon(app.icon)}
                  <svelte:component this={getIcon(app.icon)} size={16} />
                {:else}
                  <Package size={16} />
                {/if}
              </span>
              <span class="spotlight-label">{app.display_name || app.name}</span>
              {#if app.state}
                <span class="spotlight-badge {stateBadgeClass(app.state)}">{app.state}</span>
              {/if}
            </button>
          {/each}
        {/if}

        <!-- Marketplace -->
        {#if marketplaceResults.length > 0}
          <div class="spotlight-category">Marketplace</div>
          {#each marketplaceResults as app}
            {@const globalIdx = allResults.findIndex(r => r.type === 'marketplace' && r.data === app)}
            <button
              class="spotlight-item" class:selected={selectedIndex === globalIdx}
              onclick={() => selectResult({ type: 'marketplace', data: app })}
              onmouseenter={() => selectedIndex = globalIdx}
            >
              <span class="spotlight-icon">
                {#if getIcon(app.icon)}
                  <svelte:component this={getIcon(app.icon)} size={16} />
                {:else}
                  <Store size={16} />
                {/if}
              </span>
              <span class="spotlight-label">{app.display_name || app.name}</span>
              {#if app.state}
                <span class="spotlight-badge {stateBadgeClass(app.state)}">{app.state}</span>
              {/if}
              {#if app.category}
                <span class="spotlight-hint">{app.category}</span>
              {/if}
            </button>
          {/each}
        {/if}

        <!-- Files -->
        {#if fileResults.length > 0}
          <div class="spotlight-category">Files</div>
          {#each fileResults as file}
            {@const globalIdx = allResults.findIndex(r => r.type === 'file' && r.data === file)}
            {@const Icon = file.is_dir ? FolderOpen : fileIcon(file.extension || '')}
            <button
              class="spotlight-item" class:selected={selectedIndex === globalIdx}
              onclick={() => selectResult({ type: 'file', data: file })}
              onmouseenter={() => selectedIndex = globalIdx}
            >
              <span class="spotlight-icon">
                <svelte:component this={Icon} size={16} />
              </span>
              <span class="spotlight-label">{file.name}</span>
              <span class="spotlight-hint spotlight-path">{file.path.length > 40 ? '...' + file.path.slice(-37) : file.path}</span>
            </button>
          {/each}
        {/if}

        <!-- Docs -->
        {#if docResults.length > 0}
          <div class="spotlight-category">Docs</div>
          {#each docResults as doc}
            {@const globalIdx = allResults.findIndex(r => r.type === 'doc' && r.data === doc)}
            <button
              class="spotlight-item" class:selected={selectedIndex === globalIdx}
              onclick={() => selectResult({ type: 'doc', data: doc })}
              onmouseenter={() => selectedIndex = globalIdx}
            >
              <span class="spotlight-icon"><BookOpen size={16} /></span>
              <span class="spotlight-label">{doc.title.length > 60 ? doc.title.slice(0, 60) + '...' : doc.title}</span>
              {#if doc.summary}
                <span class="spotlight-hint">{doc.summary.length > 80 ? doc.summary.slice(0, 80) + '...' : doc.summary}</span>
              {/if}
            </button>
          {/each}
        {/if}
      </div>

      {#if showFilters}
        <div class="spotlight-filters">
          <div class="filter-label">Folders to search</div>
          <div class="filter-grid">
            {#each availableFolders as dir}
              <label class="filter-item">
                <input type="checkbox" checked={!excludedDirs.includes(dir)} onchange={() => toggleFolder(dir)} />
                {dir}
              </label>
            {/each}
          </div>
        </div>
      {/if}

      <div class="spotlight-footer">
        <span class="kbd">↑↓</span> Navigate
        <span class="kbd">↵</span> Open
        <span class="kbd">esc</span> Close
        <span class="footer-spacer"></span>
        <button class="filter-toggle" class:active={showFilters} onclick={() => showFilters = !showFilters} title="Filter folders">
          <Filter size={12} />
          {#if excludedDirs.length > 0}
            <span class="filter-count">{excludedDirs.length}</span>
          {/if}
        </button>
      </div>
    </div>
  </div>
{/if}

<style>
  .spotlight-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 1000;
    display: flex;
    justify-content: center;
    padding-top: 15vh;
  }

  .spotlight-modal {
    width: 560px;
    max-width: 92vw;
    max-height: 480px;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 12px;
    box-shadow: 0 16px 48px rgba(0, 0, 0, 0.3);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    align-self: flex-start;
  }

  .spotlight-input-row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
    color: var(--text-dim);
  }

  .spotlight-input {
    flex: 1;
    background: none;
    border: none;
    outline: none;
    color: var(--text);
    font-family: inherit;
    font-size: 0.95rem;
  }

  .spotlight-close {
    background: none;
    border: none;
    color: var(--text-dim);
    cursor: pointer;
    padding: 4px;
    display: flex;
    align-items: center;
  }

  .spotlight-results {
    flex: 1;
    overflow-y: auto;
    padding: 4px 0;
  }

  .spotlight-category {
    padding: 6px 16px 4px;
    font-size: 0.7rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-dim);
  }

  .spotlight-item {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 8px 16px;
    background: none;
    border: none;
    color: var(--text);
    font-family: inherit;
    font-size: 0.85rem;
    cursor: pointer;
    text-align: left;
    transition: background 0.1s;
    overflow: hidden;
  }

  .spotlight-item:hover,
  .spotlight-item.selected {
    background: var(--bg-input);
  }

  .spotlight-icon {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    color: var(--text-dim);
  }

  .emoji-icon {
    font-size: 1rem;
  }

  .spotlight-label {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .spotlight-hint {
    font-size: 0.72rem;
    color: var(--text-dim);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .spotlight-path {
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .spotlight-badge {
    font-size: 0.65rem;
    padding: 1px 6px;
    border-radius: 4px;
    font-weight: 500;
    text-transform: uppercase;
  }

  .badge-green { background: rgba(61,139,61,0.15); color: var(--green); }
  .badge-blue { background: rgba(88,166,255,0.15); color: var(--accent); }
  .badge-yellow { background: rgba(210,153,34,0.15); color: #d29922; }
  .badge-dim { background: var(--bg-input); color: var(--text-dim); }

  .spotlight-empty {
    padding: 24px 16px;
    text-align: center;
    color: var(--text-dim);
    font-size: 0.85rem;
  }

  .spotlight-footer {
    padding: 8px 16px;
    border-top: 1px solid var(--border);
    font-size: 0.7rem;
    color: var(--text-dim);
    display: flex;
    gap: 12px;
    align-items: center;
  }

  .kbd {
    display: inline-block;
    padding: 1px 5px;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 3px;
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.65rem;
    margin-right: 2px;
  }

  .footer-spacer {
    flex: 1;
  }

  .filter-toggle {
    display: flex;
    align-items: center;
    gap: 3px;
    background: none;
    border: 1px solid var(--border);
    border-radius: 4px;
    color: var(--text-dim);
    padding: 2px 6px;
    cursor: pointer;
    font-size: 0.65rem;
  }

  .filter-toggle:hover,
  .filter-toggle.active {
    color: var(--accent);
    border-color: var(--accent);
  }

  .filter-count {
    background: var(--accent);
    color: var(--bg);
    border-radius: 50%;
    width: 14px;
    height: 14px;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.6rem;
    font-weight: 600;
  }

  .spotlight-filters {
    padding: 8px 16px;
    border-top: 1px solid var(--border);
  }

  .filter-label {
    font-size: 0.7rem;
    color: var(--text-dim);
    margin-bottom: 6px;
    font-weight: 500;
  }

  .filter-grid {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 12px;
  }

  .filter-item {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 0.75rem;
    color: var(--text);
    cursor: pointer;
  }

  .filter-item input[type="checkbox"] {
    accent-color: var(--accent);
  }
</style>
