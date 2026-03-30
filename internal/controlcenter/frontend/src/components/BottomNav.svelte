<script lang="ts">
  import { nav, SYSTEM_TABS } from '../stores/nav.svelte'
  import { apps } from '../stores/apps.svelte'
  import {
    MessageCircle, Home, Terminal, Layers, CalendarClock,
    Users, SlidersHorizontal, Shield, Lock, Store, Settings, ScrollText,
    BookOpen, Zap, FolderOpen, Package, Search
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

  let { open = $bindable(false) }: { open?: boolean } = $props()
  let searchQuery = $state('')
  let searchInput: HTMLInputElement
  let panelEl: HTMLDivElement

  // Drag-to-dismiss
  let dragStartY = 0
  let dragCurrentY = 0
  let dragging = false

  function onTouchStart(e: TouchEvent) {
    const target = e.target as HTMLElement
    if (target.closest('.menu-search-input')) return
    dragStartY = e.touches[0].clientY
    dragging = true
  }

  function onTouchMove(e: TouchEvent) {
    if (!dragging) return
    dragCurrentY = e.touches[0].clientY - dragStartY
    if (dragCurrentY < 0) dragCurrentY = 0
    if (panelEl) panelEl.style.transform = `translateY(${dragCurrentY}px)`
  }

  function onTouchEnd() {
    if (!dragging) return
    dragging = false
    if (dragCurrentY > 100) {
      open = false
      searchQuery = ''
    }
    if (panelEl) panelEl.style.transform = ''
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
    nav.navigateTo(view)
    open = false
    searchQuery = ''
  }

  function handleSearchKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      const items = filtered()
      if (items.length > 0) {
        goTo(items[0].view)
      }
    }
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="menu-overlay" onclick={() => { open = false; searchQuery = '' }}></div>
  <div class="menu-panel"
    ontouchstart={onTouchStart}
    ontouchmove={onTouchMove}
    ontouchend={onTouchEnd}
    bind:this={panelEl}
  >
    <div class="sheet-handle"></div>

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
  }

  .menu-panel {
    display: none;
    position: fixed;
    bottom: 0;
    left: 0;
    right: 0;
    max-height: 85vh;
    overflow-y: auto;
    background: var(--bg-card);
    border-radius: 16px 16px 0 0;
    z-index: 29;
    padding: 0 16px calc(16px + env(safe-area-inset-bottom, 0px));
    animation: sheetUp 0.25s ease;
    box-shadow: 0 -4px 32px rgba(0, 0, 0, 0.3);
    touch-action: none;
  }

  .sheet-handle {
    width: 36px;
    height: 5px;
    background: var(--text-dim);
    opacity: 0.3;
    border-radius: 3px;
    margin: 10px auto 12px;
  }

  @keyframes sheetUp {
    from { transform: translateY(100%); }
    to { transform: translateY(0); }
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
    font-size: 0.68rem;
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
    font-size: 0.55rem;
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
    font-size: 0.85rem;
    outline: none;
  }

  .menu-search-input::placeholder {
    color: var(--text-dim);
  }

  @media (max-width: 768px) {
    .menu-overlay {
      display: block;
    }
    .menu-panel {
      display: block;
    }
  }
</style>
