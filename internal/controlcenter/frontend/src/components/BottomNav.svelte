<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { nav, SYSTEM_TABS } from '../stores/nav.svelte'
  import { apps } from '../stores/apps.svelte'
  import { api } from '../lib/api'
  import {
    MessageCircle, Home, Terminal, Layers, CalendarClock,
    Users, SlidersHorizontal, Shield, Lock, Store, Settings, ScrollText,
    BookOpen, Zap, FolderOpen, Package, Search,
    FileText, FileCode, FileImage, File, Folder
  } from 'lucide-svelte'
  import { getIcon } from '../lib/icons'

  const ICON_MAP: Record<string, any> = {
    'message-circle': MessageCircle,
    'home': Home,
    'folder-open': FolderOpen,
    'terminal': Terminal,
    'layers': Layers,
    'calendar-clock': CalendarClock,
    'users': Users,
    'zap': Zap,
    'sliders-horizontal': SlidersHorizontal,
    'shield': Shield,
    'lock': Lock,
    'store': Store,
    'settings': Settings,
    'scroll-text': ScrollText,
    'book-open': BookOpen,
  }

  function fileIcon(name: string) {
    const ext = name.split('.').pop()?.toLowerCase() || ''
    const code = ['js','ts','go','py','rs','sh','json','yaml','yml','toml','xml','html','css','svelte']
    const img = ['png','jpg','jpeg','gif','svg','webp']
    if (code.includes(ext)) return FileCode
    if (img.includes(ext)) return FileImage
    if (['md','txt','doc','pdf'].includes(ext)) return FileText
    return File
  }

  let { open = $bindable(false) }: { open?: boolean } = $props()
  let searchQuery = $state('')
  let searchInput: HTMLInputElement
  let panelEl: HTMLDivElement
  let closing = $state(false)
  let visible = $state(false)

  // Lens-style search results
  let fileResults = $state<any[]>([])
  let docResults = $state<any[]>([])
  let searchLoading = $state(false)
  let debounceTimer: ReturnType<typeof setTimeout> | null = null

  async function searchRemote(q: string) {
    if (!q || q.length < 2) {
      fileResults = []; docResults = []
      return
    }
    searchLoading = true
    try {
      const data = await api<any>(`/api/search?q=${encodeURIComponent(q)}`)
      fileResults = data.files || []
      docResults = data.docs || []
    } catch {
      fileResults = []; docResults = []
    } finally {
      searchLoading = false
    }
  }

  $effect(() => {
    const q = searchQuery.trim()
    if (debounceTimer) clearTimeout(debounceTimer)
    if (!q || q.length < 2) {
      fileResults = []; docResults = []
      return
    }
    debounceTimer = setTimeout(() => searchRemote(q), 250)
  })

  function openFile(f: any) {
    haptic()
    dismiss()
    nav.navigateTo('home')
    setTimeout(() => {
      const evt = f.is_dir ? 'alf:open-dir' : 'alf:open-file'
      window.dispatchEvent(new CustomEvent(evt, { detail: { path: f.path } }))
    }, 100)
  }

  function openDoc(d: any) {
    haptic()
    dismiss()
    nav.navigateTo(`docs:${d.id || d.name}`)
  }

  // Allow external opening via custom event
  function handleOpenMenu() { open = true; haptic() }
  onMount(() => { window.addEventListener('alf:open-menu', handleOpenMenu) })
  onDestroy(() => {
    window.removeEventListener('alf:open-menu', handleOpenMenu)
    if (debounceTimer) clearTimeout(debounceTimer)
  })

  // Animate open/close
  $effect(() => {
    if (open) {
      closing = false
      visible = true
    }
  })

  function dismiss() {
    closing = true
    searchQuery = ''
    fileResults = []; docResults = []
    setTimeout(() => {
      open = false
      closing = false
      visible = false
    }, 200)
  }

  // Drag-to-dismiss
  let dragStartY = 0
  let dragCurrentY = 0
  let dragging = false

  function haptic(ms = 10) {
    navigator?.vibrate?.(ms)
  }

  function onHandleTouchStart(e: TouchEvent) {
    dragStartY = e.touches[0].clientY
    dragging = true
  }

  function onHandleTouchMove(e: TouchEvent) {
    if (!dragging) return
    e.preventDefault()
    dragCurrentY = e.touches[0].clientY - dragStartY
    if (dragCurrentY < 0) dragCurrentY = 0
    if (panelEl) panelEl.style.transform = `translateY(${dragCurrentY}px)`
  }

  function onHandleTouchEnd() {
    if (!dragging) return
    dragging = false
    if (dragCurrentY > 80) {
      if (panelEl) panelEl.style.transform = ''
      haptic(15)
      dismiss()
    } else {
      if (panelEl) panelEl.style.transform = ''
    }
    dragCurrentY = 0
  }

  // All items: system tabs + apps
  const allItems = $derived(() => {
    const items = SYSTEM_TABS.filter(t => !t.comingSoon).map(t => ({
      view: t.view,
      label: t.label,
      icon: t.icon,
      type: 'system' as const,
    }))
    for (const app of apps.items) {
      items.push({
        view: 'page:' + app.name,
        label: app.display_name || app.name,
        icon: app.icon || 'package',
        type: 'app' as const,
      })
    }
    return items
  })

  const filtered = $derived(() => {
    const items = allItems()
    if (!searchQuery.trim()) return items
    const q = searchQuery.toLowerCase()
    return items.filter(i => i.label.toLowerCase().includes(q))
  })

  function goTo(view: string) {
    haptic()
    nav.navigateTo(view)
    dismiss()
  }

  function handleSearchKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      const items = filtered()
      if (items.length > 0) {
        goTo(items[0].view)
      } else if (fileResults.length > 0) {
        openFile(fileResults[0])
      } else if (docResults.length > 0) {
        openDoc(docResults[0])
      }
    }
  }
</script>

{#if open || visible}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="menu-overlay" class:closing onclick={dismiss}></div>
  <div class="menu-panel" class:closing bind:this={panelEl}>
    <div class="sheet-handle"
      ontouchstart={onHandleTouchStart}
      ontouchmove={onHandleTouchMove}
      ontouchend={onHandleTouchEnd}
    ></div>

    <div class="menu-search">
      <Search size={16} />
      <input
        bind:this={searchInput}
        bind:value={searchQuery}
        onkeydown={handleSearchKeydown}
        type="text"
        placeholder="Search..."
        class="menu-search-input"
      />
    </div>

    <div class="menu-scroll">
    <div class="menu-grid">
      {#each filtered() as item}
        <button
          class="menu-grid-item"
          class:active={nav.currentView === item.view}
          onclick={() => goTo(item.view)}
        >
          <div class="menu-grid-icon">
            {#if item.type === 'app'}
              {#if getIcon(item.icon)}
                <svelte:component this={getIcon(item.icon)} size={24} />
              {:else}
                <Package size={24} />
              {/if}
            {:else if ICON_MAP[item.icon]}
              <svelte:component this={ICON_MAP[item.icon]} size={24} />
            {/if}
          </div>
          <span>{item.label}</span>
          {#if nav.badges[item.view]}
            <span class="menu-badge">{nav.badges[item.view]}</span>
          {/if}
        </button>
      {/each}
    </div>

    <!-- Lens search results -->
    {#if searchLoading}
      <div class="search-status">Searching...</div>
    {/if}

    {#if fileResults.length > 0}
      <div class="search-section">
        <div class="search-section-label">Files</div>
        {#each fileResults.slice(0, 8) as f}
          <button class="search-result" onclick={() => openFile(f)}>
            <svelte:component this={f.is_dir ? Folder : fileIcon(f.name)} size={16} />
            <div class="search-result-info">
              <span class="search-result-name">{f.name}</span>
              <span class="search-result-path">{f.path}</span>
            </div>
          </button>
        {/each}
      </div>
    {/if}

    {#if docResults.length > 0}
      <div class="search-section">
        <div class="search-section-label">Docs</div>
        {#each docResults.slice(0, 5) as d}
          <button class="search-result" onclick={() => openDoc(d)}>
            <BookOpen size={16} />
            <div class="search-result-info">
              <span class="search-result-name">{d.title || d.name}</span>
            </div>
          </button>
        {/each}
      </div>
    {/if}
    </div><!-- .menu-scroll -->
  </div>
{/if}

<style>
  .menu-overlay {
    display: none;
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 28;
    -webkit-tap-highlight-color: transparent;
    animation: overlayIn 0.2s ease;
  }

  .menu-overlay.closing {
    animation: overlayOut 0.2s ease forwards;
  }

  .menu-panel {
    display: none;
    position: fixed;
    bottom: 0;
    left: 0;
    right: 0;
    max-height: 85vh;
    overflow: hidden;
    background: var(--bg-card);
    border-radius: 16px 16px 0 0;
    z-index: 29;
    padding: 0 16px calc(16px + env(safe-area-inset-bottom, 0px));
    animation: sheetUp 0.2s ease;
    box-shadow: 0 -4px 32px rgba(0, 0, 0, 0.3);
    display: none;
    flex-direction: column;
  }

  .menu-scroll {
    flex: 1;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    overscroll-behavior: contain;
    min-height: 0;
  }

  .menu-panel.closing {
    animation: sheetDown 0.2s ease forwards;
  }

  .sheet-handle {
    width: 36px;
    height: 5px;
    background: var(--text-dim);
    opacity: 0.3;
    border-radius: 3px;
    margin: 10px auto 12px;
    touch-action: none;
    cursor: grab;
    padding: 8px 0;
    box-sizing: content-box;
  }

  @keyframes sheetUp {
    from { transform: translateY(100%); }
    to { transform: translateY(0); }
  }

  @keyframes sheetDown {
    from { transform: translateY(0); }
    to { transform: translateY(100%); }
  }

  @keyframes overlayIn {
    from { opacity: 0; }
    to { opacity: 1; }
  }

  @keyframes overlayOut {
    from { opacity: 1; }
    to { opacity: 0; }
  }

  .menu-grid {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 8px;
    margin-top: 12px;
  }

  .menu-grid-item {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 6px;
    padding: 12px 4px;
    background: none;
    border: none;
    border-radius: 12px;
    color: var(--text-dim);
    cursor: pointer;
    font-family: inherit;
    font-size: var(--font-xs, 11px);
    font-weight: 500;
    -webkit-tap-highlight-color: transparent;
    position: relative;
    transition: background 0.15s;
    min-height: 72px;
  }

  .menu-grid-item:active {
    background: color-mix(in srgb, var(--text) 8%, transparent);
  }

  .menu-grid-item.active {
    color: var(--accent);
  }

  .menu-grid-item.active .menu-grid-icon {
    background: var(--accent);
    color: var(--bg-card);
  }

  .menu-grid-icon {
    width: 48px;
    height: 48px;
    border-radius: 14px;
    background: var(--bg-input, color-mix(in srgb, var(--text) 6%, transparent));
    display: flex;
    align-items: center;
    justify-content: center;
    transition: background 0.15s, color 0.15s;
  }

  .menu-badge {
    position: absolute;
    top: 6px;
    right: calc(50% - 28px);
    background: var(--accent);
    color: var(--on-accent, #fff);
    font-size: var(--font-xs, 11px);
    font-weight: 700;
    min-width: 16px;
    height: 16px;
    border-radius: 8px;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0 4px;
  }

  .menu-search {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 12px;
    background: var(--bg-input, color-mix(in srgb, var(--text) 6%, transparent));
    border-radius: 10px;
    color: var(--text-dim);
  }

  .menu-search-input {
    flex: 1;
    background: none;
    border: none;
    color: var(--text);
    font-family: inherit;
    font-size: var(--font-sm, 13px);
    outline: none;
  }

  .menu-search-input::placeholder {
    color: var(--text-dim);
  }

  /* Search results */
  .search-status {
    padding: 12px 4px;
    color: var(--text-dim);
    font-size: var(--font-xs, 11px);
    text-align: center;
  }

  .search-section {
    margin-top: 12px;
    border-top: 1px solid var(--border);
    padding-top: 8px;
  }

  .search-section-label {
    font-size: var(--font-xs, 11px);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-dim);
    padding: 4px 8px;
  }

  .search-result {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 10px 8px;
    background: none;
    border: none;
    border-radius: 8px;
    color: var(--text);
    font-family: inherit;
    font-size: var(--font-sm, 13px);
    cursor: pointer;
    text-align: left;
    -webkit-tap-highlight-color: transparent;
    transition: background 0.1s;
  }

  .search-result:active {
    background: color-mix(in srgb, var(--text) 8%, transparent);
  }

  .search-result-info {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .search-result-name {
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .search-result-path {
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  @media (max-width: 768px) {
    .menu-overlay {
      display: block;
    }
    .menu-panel {
      display: flex;
    }
  }
</style>
